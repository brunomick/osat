package remote

import (
	"bytes"
	"context"
	"errors"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"

	"osat/internal/i18n"
)

// Buscar archivos en el servidor por nombre (y opcionalmente por contenido), dentro de una carpeta y
// sus subcarpetas. Con `find` del servidor si se puede (los resultados llegan a medida que sale); si no,
// recorriendo las carpetas por SFTP. Lo que escribe el usuario siempre va entre comillas (shellQuote) o
// se compara en Go: nunca se ejecuta.

// searchLimit es el máximo de resultados; es una variable solo para que los tests puedan bajarlo.
var searchLimit = 1000

const (
	searchBatch   = 150 * time.Millisecond // cada cuánto se mandan al frontend los resultados nuevos
	searchMaxLine = 64 << 10               // una línea más larga que esto no es una ruta: se descarta
)

// SearchDone es lo que llega en "search:done:<id>" cuando termina la búsqueda (también si se detuvo).
type SearchDone struct {
	Count     int    `json:"count"`
	Truncated bool   `json:"truncated"` // había más que searchLimit
	Error     string `json:"error,omitempty"`
	// Se pidió buscar por contenido pero el servidor no puede: se buscó solo por nombre.
	ContentUnavailable bool `json:"contentUnavailable,omitempty"`
}

// SearchCaps dice qué puede hacer la búsqueda en este servidor (para deshabilitar el contenido antes de buscar).
type SearchCaps struct {
	Content bool   `json:"content"`
	Note    string `json:"note,omitempty"` // qué falta, para mostrar tal cual
}

// findCaps es lo que hay en el servidor, que se averigua una vez por sesión.
type findCaps struct {
	exec   bool // deja ejecutar comandos
	find   bool
	printf bool // el find soporta -printf (GNU; BusyBox no)
	grep   bool
}

func (c findCaps) caps() SearchCaps {
	switch {
	case !c.exec:
		return SearchCaps{Note: i18n.T("remote.searchNoExec")}
	case !c.find:
		return SearchCaps{Note: i18n.T("remote.searchNoFind")}
	case !c.grep:
		return SearchCaps{Note: i18n.T("remote.searchNoGrep")}
	}
	return SearchCaps{Content: true}
}

// findCaps averigua (y recuerda) qué hay en el servidor. No usa `| head` ni esconde errores: el estado
// de una tubería es el del último comando, y así un find que no existe parecería "cero resultados".
func (s *Session) findCaps(ctx context.Context) (findCaps, error) {
	s.capsMu.Lock()
	defer s.capsMu.Unlock()
	if s.caps != nil {
		return *s.caps, nil
	}
	var c findCaps
	sh := s.shellOrNil()
	if sh.usable() {
		// ok: el comando salió con 0. errNoShell no es un error acá: simplemente no hay exec.
		ok := func(cmd string) (bool, error) {
			status, _, err := sh.run(ctx, cmd)
			if errors.Is(err, errNoShell) {
				return false, nil
			}
			return err == nil && status == 0, err
		}
		var err error
		if c.find, err = ok("command -v find"); err != nil {
			return findCaps{}, err
		}
		c.exec = sh.usable()
		if c.find {
			if c.printf, err = ok("LC_ALL=C find / -maxdepth 0 -printf ''"); err != nil {
				return findCaps{}, err
			}
			if c.grep, err = ok("command -v grep"); err != nil {
				return findCaps{}, err
			}
		}
	}
	s.caps = &c
	return c, nil
}

// findCommand arma el comando de búsqueda. dir, pattern y contains van siempre entre comillas.
// El límite de resultados se aplica en Go (se deja de leer y se corta el comando).
func findCommand(dir, pattern, contains string, printf bool) string {
	if pattern == "" {
		pattern = "*"
	}
	base := "LC_ALL=C find " + shellQuote(dir)
	switch {
	case contains != "":
		return base + " -type f -iname " + shellQuote(pattern) + " -size -10M -exec grep -IlF -- " + shellQuote(contains) + " {} + 2>/dev/null"
	case printf:
		// 2>/dev/null solo esconde las carpetas sin permiso.
		return base + " -iname " + shellQuote(pattern) + ` -printf '%y %s %p\n' 2>/dev/null`
	}
	return base + " -iname " + shellQuote(pattern) + " 2>/dev/null"
}

// Cómo salen las líneas del comando (ver findCommand).
type findOut int

const (
	outTyped findOut = iota // "%y %s %p": tipo, tamaño y ruta
	outPaths                // solo rutas: no se sabe el tipo ni el tamaño
	outFiles                // archivos que contienen el texto: se sabe que son archivos, no el tamaño
)

// parseFindLine convierte una línea del comando en un resultado. Descarta lo que no tiene la forma
// esperada (p. ej. lo que imprima el .bashrc del servidor) o no está dentro de root.
//
// Tamaño -1 = no se sabe. Mode "?" = no se sabe si es archivo o carpeta (el frontend lo resuelve al abrirlo).
func parseFindLine(line, root string, kind findOut) (FileEntry, bool) {
	e := FileEntry{Size: -1}
	p := line
	switch kind {
	case outTyped:
		y, rest, ok := strings.Cut(line, " ")
		size, p2, ok2 := strings.Cut(rest, " ")
		n, err := strconv.ParseInt(size, 10, 64)
		if !ok || !ok2 || len(y) != 1 || err != nil {
			return e, false
		}
		p, e.Size, e.IsDir, e.IsLink = p2, n, y == "d", y == "l"
		if y == "l" {
			e.Mode = "?"
		}
	case outPaths:
		e.Mode = "?"
	}
	prefix := strings.TrimSuffix(root, "/") + "/"
	if !strings.HasPrefix(p, prefix) || len(p) == len(prefix) {
		return e, false
	}
	e.Path, e.Name = p, path.Base(p)
	return e, true
}

// lineWriter parte lo que escribe un comando en líneas.
type lineWriter struct {
	buf []byte
	fn  func(line string)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.fn(strings.TrimSuffix(string(w.buf[:i]), "\r"))
		w.buf = w.buf[i+1:]
	}
	if len(w.buf) > searchMaxLine {
		w.buf = nil
	}
	return len(p), nil
}

// ---- resultados en tandas ----

// searchSink junta los resultados, aplica el límite y los manda al frontend en tandas.
type searchSink struct {
	m  *Manager
	id string

	mu        sync.Mutex
	batch     []FileEntry
	count     int
	truncated bool
}

// add suma un resultado. false = ya no hace falta seguir (se llegó al límite).
func (k *searchSink) add(e FileEntry) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.truncated {
		return false
	}
	if k.count >= searchLimit {
		k.truncated = true // hay uno más que el límite: ese no se muestra
		return false
	}
	k.count++
	k.batch = append(k.batch, e)
	return true
}

func (k *searchSink) flush() {
	k.mu.Lock()
	batch := k.batch
	k.batch = nil
	k.mu.Unlock()
	if len(batch) > 0 {
		k.m.emit("search:results:"+k.id, batch)
	}
}

func (k *searchSink) totals() (int, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.count, k.truncated
}

// ---- API ----

type search struct {
	sessionID string
	cancel    context.CancelFunc
}

// SearchCaps dice si en este servidor se puede buscar por contenido y, si no, por qué.
func (m *Manager) SearchCaps(sessionID string) (SearchCaps, error) {
	s, err := m.get(sessionID)
	if err != nil {
		return SearchCaps{}, err
	}
	if s.sftp == nil {
		return SearchCaps{}, i18n.Err("remote.theSessionIsNotSftp")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := s.findCaps(ctx)
	if err != nil {
		return SearchCaps{}, err
	}
	return c.caps(), nil
}

// SearchStart busca en dir y sus subcarpetas archivos cuyo nombre coincida con namePattern (comodines,
// sin distinguir mayúsculas; vacío = todos) y, si contains no es vacío, que contengan ese texto. Vuelve
// enseguida con la carpeta real donde busca (las rutas de los resultados salen de ahí) y los resultados
// llegan como eventos "search:results:<searchID>" (en tandas) y "search:done:<searchID>" (al final):
// el frontend genera el id y se suscribe antes de llamar, así no pierde los primeros.
func (m *Manager) SearchStart(searchID, sessionID, dir, namePattern, contains string) (string, error) {
	if searchID == "" {
		return "", i18n.Err("remote.theSearchIdentifierIsMissing")
	}
	for _, v := range []string{dir, namePattern, contains} {
		if strings.ContainsRune(v, 0) {
			return "", i18n.Err("remote.theSearchCanTContain")
		}
	}
	if strings.ContainsAny(contains, "\r\n") {
		return "", i18n.Err("remote.theTextToSearchFor")
	}
	s, err := m.get(sessionID)
	if err != nil {
		return "", err
	}
	if s.sftp == nil {
		return "", i18n.Err("remote.theSessionIsNotSftp")
	}
	if dir == "" {
		dir = "."
	}
	// Con RealPath: si la carpeta es un enlace simbólico (como /var/www), find y Walk no entran.
	root, err := s.sftp.RealPath(dir)
	if err != nil {
		return "", err
	}
	if !path.IsAbs(root) {
		return "", i18n.Err("remote.couldnTResolveTheFolder", dir)
	}
	fi, err := s.sftp.Stat(root)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		return "", i18n.Err("remote.qIsNotAFolder", root)
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.searchMu.Lock()
	if _, dup := m.searches[searchID]; dup {
		m.searchMu.Unlock()
		cancel()
		return "", i18n.Err("remote.thereIsAlreadyASearch")
	}
	m.searches[searchID] = &search{sessionID: sessionID, cancel: cancel}
	m.searchMu.Unlock()

	go m.runSearch(ctx, cancel, searchID, s, root, namePattern, contains)
	return root, nil
}

// SearchStop corta una búsqueda en curso (en el servidor también). Si ya terminó, no hace nada.
func (m *Manager) SearchStop(searchID string) {
	m.searchMu.Lock()
	sr := m.searches[searchID]
	m.searchMu.Unlock()
	if sr != nil {
		sr.cancel()
	}
}

// stopSearches corta las búsquedas de una sesión que se cierra.
func (m *Manager) stopSearches(sessionID string) {
	m.searchMu.Lock()
	defer m.searchMu.Unlock()
	for _, sr := range m.searches {
		if sr.sessionID == sessionID {
			sr.cancel()
		}
	}
}

func (m *Manager) runSearch(ctx context.Context, cancel context.CancelFunc, id string, s *Session, root, pattern, contains string) {
	defer cancel()
	k := &searchSink{m: m, id: id}
	stopTick := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tk := time.NewTicker(searchBatch)
		defer tk.Stop()
		for {
			select {
			case <-tk.C:
				k.flush()
			case <-stopTick:
				return
			}
		}
	}()

	done := m.doSearch(ctx, k, s, root, pattern, contains)

	close(stopTick)
	wg.Wait()
	k.flush()
	done.Count, done.Truncated = k.totals()
	m.searchMu.Lock()
	delete(m.searches, id)
	m.searchMu.Unlock()
	m.emit("search:done:"+id, done)
}

// doSearch elige el camino (find o SFTP) y lo corre. Cancelar no es un error.
func (m *Manager) doSearch(ctx context.Context, k *searchSink, s *Session, root, pattern, contains string) (done SearchDone) {
	caps, err := s.findCaps(ctx)
	if err != nil {
		done.Error = searchErrText(ctx, err)
		return
	}
	if contains != "" && !caps.caps().Content {
		contains = ""
		done.ContentUnavailable = true
	}
	if caps.exec && caps.find {
		sh := s.shellOrNil()
		err = findSearch(ctx, sh, k, root, pattern, contains, caps.printf)
		if !errors.Is(err, errNoShell) {
			done.Error = searchErrText(ctx, err)
			return
		}
		// El servidor dejó de aceptar comandos a mitad de camino: sigue por SFTP, solo por nombre.
		done.ContentUnavailable = done.ContentUnavailable || contains != ""
	}
	done.Error = searchErrText(ctx, walkSearch(ctx, s.sftp, k, root, pattern))
	return
}

func searchErrText(ctx context.Context, err error) string {
	if err == nil || ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return ""
	}
	return err.Error()
}

// findSearch corre find en el servidor y va entregando lo que sale. Al llegar al límite corta el comando.
func findSearch(ctx context.Context, sh *shell, k *searchSink, root, pattern, contains string, printf bool) error {
	kind := outPaths
	switch {
	case contains != "":
		kind = outFiles
	case printf:
		kind = outTyped
	}
	cctx, stop := context.WithCancel(ctx)
	defer stop()
	w := &lineWriter{fn: func(line string) {
		if e, ok := parseFindLine(line, root, kind); ok && !k.add(e) {
			stop()
		}
	}}
	status, _, err := sh.stream(cctx, findCommand(root, pattern, contains, printf), nil, w)
	switch {
	case err != nil:
		return err
	case status == 126 || status == 127:
		return i18n.Err("remote.findIsNotAvailableOn")
	}
	return nil // find sale con 1 si alguna carpeta no se pudo leer: no es un error de la búsqueda
}

// walkSearch es el respaldo sin find: recorre las carpetas por SFTP (en orden, una por vez).
// Las carpetas que no se pueden leer se saltean.
func walkSearch(ctx context.Context, cl *sftp.Client, k *searchSink, root, pattern string) error {
	pat := strings.ToLower(pattern)
	if pat == "" {
		pat = "*"
	}
	if _, err := path.Match(pat, ""); err != nil {
		return i18n.Err("remote.invalidPatternQ", pattern)
	}
	w := cl.Walk(root)
	for w.Step() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if w.Err() != nil || w.Path() == root {
			continue
		}
		fi := w.Stat()
		if ok, _ := path.Match(pat, strings.ToLower(fi.Name())); !ok {
			continue
		}
		e := FileEntry{Name: fi.Name(), Path: w.Path(), Size: fi.Size(), Mode: lsMode(fi.Mode()), IsDir: fi.IsDir(), ModTime: fi.ModTime().Unix()}
		e.IsLink = e.Mode[0] == 'l'
		if e.IsLink {
			e.Mode = "?" // no se sabe adónde apunta
		}
		if !k.add(e) {
			return nil
		}
	}
	return nil
}

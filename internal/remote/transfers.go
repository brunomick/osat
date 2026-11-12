package remote

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/pkg/sftp"

	"osat/internal/applog"
	"osat/internal/i18n"
)

var errTooBig = i18n.Err("remote.theFileIsTooBig")

const (
	DirUp   = "up"
	DirDown = "down"

	StatusQueued   = "queued"
	StatusRunning  = "running"
	StatusDone     = "done"
	StatusError    = "error"
	StatusCanceled = "canceled"
)

// Transfer es el estado de una transferencia, emitido en "transfer:update".
type Transfer struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Direction string `json:"direction"`
	Name      string `json:"name"`
	Source    string `json:"source"`
	Target    string `json:"target"`
	Total     int64  `json:"total"`
	Done      int64  `json:"done"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
	Started   int64  `json:"started"`
	ConnID    string `json:"connId"`
	// Falló porque se cortó la conexión y quedó el pedazo ya transferido: Reintentar sigue desde
	// ResumeFrom (en las subidas, como mínimo), si es seguro (ver resume.go).
	Resumable  bool   `json:"resumable,omitempty"`
	ResumeFrom int64  `json:"resumeFrom,omitempty"`
	Warning    string `json:"warning,omitempty"` // p. ej. que al reintentar se empezó de cero
}

// runFunc hace la transferencia por cl. resume: es un reintento después de un corte y conviene
// seguir desde el pedazo que quedó.
type runFunc func(ctx context.Context, cl *sftp.Client, tr *transfer, resume bool) error

type transfer struct {
	mu     sync.Mutex
	info   Transfer
	done   atomic.Int64
	cancel context.CancelFunc
	run    runFunc // se guarda para Reintentar
	drop   func()  // borra el pedazo de un archivo reanudable (lo fija run, ver resume.go)
	// Política de conflictos efectiva con la que corrió: ConflictOverwrite o ConflictSkip ("preguntar"
	// sin conflictos cuenta como omitir). Con omitir, own recuerda lo que escribió (ver owned).
	policy string
	own    *owned
}

type transfers struct {
	m    *Manager
	mu   sync.Mutex
	all  map[string]*transfer
	sema chan struct{}
}

func newTransfers(m *Manager) *transfers {
	return &transfers{m: m, all: map[string]*transfer{}, sema: make(chan struct{}, 4)}
}

func (t *transfers) emit(tr *transfer) {
	tr.mu.Lock()
	tr.info.Done = tr.done.Load()
	info := tr.info
	tr.mu.Unlock()
	t.m.emit("transfer:update", info)
}

func (t *transfers) setStatus(tr *transfer, status string, err error) {
	tr.mu.Lock()
	tr.info.Status = status
	if err != nil {
		tr.info.Error = err.Error()
	}
	tr.mu.Unlock()
	t.emit(tr)
}

// start arranca una transferencia. policy: la política de conflictos ya resuelta (con "preguntar"
// solo se llega acá si no había conflictos).
func (t *transfers) start(info Transfer, cl *sftp.Client, policy string, run runFunc) string {
	info.ID = uuid.NewString()
	info.Started = time.Now().Unix()
	if s, err := t.m.get(info.SessionID); err == nil {
		info.ConnID = s.ConnID
	}
	tr := &transfer{info: info, run: run, policy: ConflictOverwrite}
	if policy != ConflictOverwrite {
		tr.policy, tr.own = ConflictSkip, &owned{paths: map[string]bool{}}
	}
	t.mu.Lock()
	t.all[info.ID] = tr
	t.mu.Unlock()
	t.launch(tr, cl, false, false)
	return info.ID
}

func (t *transfers) get(id string) *transfer {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.all[id]
}

// launch corre la transferencia en segundo plano (o la vuelve a correr: retry; resume = seguir
// desde el pedazo).
func (t *transfers) launch(tr *transfer, cl *sftp.Client, retry, resume bool) {
	ctx, cancel := context.WithCancel(context.Background())
	tr.mu.Lock()
	tr.cancel = cancel
	tr.info.Status = StatusQueued
	info := tr.info // para el log (un reintento puede haber cambiado la sesión)
	tr.mu.Unlock()
	tr.own.begin(retry)
	t.emit(tr)
	conn := t.m.sessionConn(info.SessionID)

	go func() {
		defer applog.Recover("transferencia")
		defer cancel()
		select {
		case t.sema <- struct{}{}:
		case <-ctx.Done():
			tr.mu.Lock()
			drop := tr.drop // un reintento cancelado en la cola: el pedazo del intento anterior sobra
			tr.mu.Unlock()
			if drop != nil {
				drop()
			}
			t.setStatus(tr, StatusCanceled, nil)
			slog.Info("transferencia cancelada", "dir", info.Direction, "source", info.Source, "target", info.Target, "conn", conn)
			return
		}
		defer func() { <-t.sema }()

		t.setStatus(tr, StatusRunning, nil)
		began := time.Now()
		slog.Info("transferencia iniciada", "dir", info.Direction, "source", info.Source, "target", info.Target, "bytes", info.Total, "conn", conn)
		stop := make(chan struct{})
		go func() { // progreso cada 200 ms
			tk := time.NewTicker(200 * time.Millisecond)
			defer tk.Stop()
			for {
				select {
				case <-tk.C:
					t.emit(tr)
				case <-stop:
					return
				}
			}
		}()
		err := tr.run(ctx, cl, tr, resume)
		close(stop)
		if n := tr.own.keptCount(); n > 0 {
			tr.warn(keptWarning(n))
		}
		// Cancelado vs. corte: solo cancela el usuario (o cerrar la pestaña). El pedazo de un archivo
		// reanudable se conserva únicamente si se cortó la conexión.
		tr.mu.Lock()
		sid, drop := tr.info.SessionID, tr.drop
		tr.mu.Unlock()
		cut := err != nil && ctx.Err() == nil && t.m.connCut(sid, err)
		if err != nil && !cut && drop != nil {
			drop()
		}
		switch {
		case ctx.Err() != nil:
			t.setStatus(tr, StatusCanceled, nil)
			slog.Info("transferencia cancelada", "dir", info.Direction, "source", info.Source, "target", info.Target, "bytes", tr.done.Load(), "conn", conn)
		case err != nil:
			if cut && drop != nil {
				tr.mu.Lock()
				tr.info.Resumable = true
				tr.info.ResumeFrom = tr.done.Load()
				if tr.info.Direction == DirUp {
					// Con la conexión caída no se puede mirar el pedazo remoto: lo mínimo que va a quedar (ver uploadOffset).
					tr.info.ResumeFrom = max(0, tr.info.ResumeFrom-resumeInFlight-resumeBack)
				}
				tr.mu.Unlock()
			}
			t.setStatus(tr, StatusError, err)
			slog.Warn("transferencia con error", "dir", info.Direction, "source", info.Source, "target", info.Target, "bytes", tr.done.Load(), "conn", conn, "err", err)
		default:
			t.setStatus(tr, StatusDone, nil)
			slog.Info("transferencia terminada", "dir", info.Direction, "source", info.Source, "target", info.Target, "bytes", tr.done.Load(), "duration", time.Since(began).Round(time.Millisecond), "conn", conn)
		}
	}()
}

func (t *transfers) cancel(id string) {
	if tr := t.get(id); tr != nil {
		tr.mu.Lock()
		cancel := tr.cancel
		tr.mu.Unlock()
		cancel()
	}
}

func (t *transfers) cancelSession(sessionID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, tr := range t.all {
		tr.mu.Lock()
		if tr.info.SessionID == sessionID {
			tr.cancel()
		}
		tr.mu.Unlock()
	}
}

func (t *transfers) clearFinished() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, tr := range t.all {
		tr.mu.Lock()
		s, drop := tr.info.Status, tr.drop
		resumable := tr.info.Resumable
		tr.mu.Unlock()
		if s == StatusDone || s == StatusError || s == StatusCanceled {
			if resumable && drop != nil {
				go drop() // sin la fila ya no se puede reanudar: el pedazo sobra
			}
			delete(t.all, id)
		}
	}
}

// ---- helpers de copia con progreso y cancelación ----

type progressReader struct {
	ctx  context.Context
	r    io.Reader
	n    *atomic.Int64
	size int64
}

func (p *progressReader) Read(b []byte) (int, error) {
	if err := p.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := p.r.Read(b)
	p.n.Add(int64(n))
	return n, err
}

// Size permite a pkg/sftp usar escrituras concurrentes (más rápido).
func (p *progressReader) Size() int64 { return p.size }

type progressWriter struct {
	ctx context.Context
	w   io.Writer
	n   *atomic.Int64
}

func (p *progressWriter) Write(b []byte) (int, error) {
	if err := p.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := p.w.Write(b)
	p.n.Add(int64(n))
	return n, err
}

func uploadFile(ctx context.Context, cl *sftp.Client, local, remote string, ctr *atomic.Int64) error {
	src, err := os.Open(local)
	if err != nil {
		return err
	}
	defer src.Close()
	st, err := src.Stat()
	if err != nil {
		return err
	}
	// En Windows los permisos locales no significan nada (todo es 0666): dejamos
	// que el umask del servidor decida en vez de crear archivos escribibles por todos.
	mode := st.Mode().Perm()
	if runtime.GOOS == "windows" {
		mode = 0
	}
	return writeRemote(ctx, cl, remote, src, st.Size(), mode, ctr)
}

func downloadFile(ctx context.Context, cl *sftp.Client, remote, local string, ctr *atomic.Int64) error {
	src, err := cl.Open(remote)
	if err != nil {
		return err
	}
	defer src.Close()
	// Bajamos a un temporal y renombramos al final: si falla o se cancela, un
	// archivo local que ya existía queda intacto.
	tmp := local + ".osat-part"
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		return err
	}
	dst, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, err = src.WriteTo(&progressWriter{ctx: ctx, w: dst, n: ctr})
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, local)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// Qué hacer si el destino ya existe (archivo, o carpeta que se combinaría).
const (
	ConflictAsk       = "ask"       // devolver CodeConflict para que el usuario elija
	ConflictOverwrite = "overwrite" // reemplazar
	ConflictSkip      = "skip"      // omitir los que ya existen
)

// checkConflicts aplica la política: con ask devuelve un *Error si algún
// destino existe; con skip devuelve qué índices omitir.
func checkConflicts(policy string, names []string, exists func(i int) bool) (map[int]bool, error) {
	if policy == ConflictOverwrite {
		return nil, nil
	}
	skip := map[int]bool{}
	var found []string
	for i := range names {
		if exists(i) {
			skip[i] = true
			found = append(found, names[i])
		}
	}
	if len(found) > 0 && policy != ConflictSkip {
		return nil, newErr(CodeConflict, "Algunos elementos ya existen en el destino.",
			map[string]string{"kind": "items", "names": strings.Join(found, "\n"), "count": strconv.Itoa(len(found))})
	}
	return skip, nil
}

// Upload sube archivos o carpetas locales a remoteDir. Devuelve los IDs.
func (m *Manager) Upload(sessionID string, localPaths []string, remoteDir, policy string) ([]string, error) {
	cl, err := m.sftpOf(sessionID)
	if err != nil {
		return nil, err
	}
	stats := make([]os.FileInfo, len(localPaths))
	names := make([]string, len(localPaths))
	for i, lp := range localPaths {
		if stats[i], err = os.Stat(lp); err != nil {
			return nil, err
		}
		names[i] = filepath.Base(lp)
	}
	skip, err := checkConflicts(policy, names, func(i int) bool {
		_, err := cl.Lstat(path.Join(remoteDir, names[i]))
		return err == nil
	})
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(localPaths))
	for i, lp := range localPaths {
		if skip[i] {
			continue
		}
		lp, st, name := lp, stats[i], names[i]
		target := path.Join(remoteDir, name)
		info := Transfer{SessionID: sessionID, Direction: DirUp, Name: name, Source: lp, Target: target}

		if !st.IsDir() {
			info.Total = st.Size()
			ids = append(ids, m.transfers.start(info, cl, policy, func(ctx context.Context, cl *sftp.Client, tr *transfer, resume bool) error {
				if !tr.own.may(target, func() bool { _, err := cl.Lstat(target); return err == nil }) {
					tr.setDrop(nil)
					_ = cl.Remove(partPathFor(target)) // el pedazo de un intento anterior, si quedó
					tr.done.Store(st.Size())
					return nil
				}
				if st.Size() > resumeMin {
					return uploadPart(ctx, cl, lp, target, st, tr, resume)
				}
				return uploadFile(ctx, cl, lp, target, &tr.done)
			}))
			continue
		}

		var total int64
		_ = filepath.WalkDir(lp, func(_ string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				if fi, err := d.Info(); err == nil {
					total += fi.Size()
				}
			}
			return nil
		})
		info.Total = total
		ids = append(ids, m.transfers.start(info, cl, policy, func(ctx context.Context, cl *sftp.Client, tr *transfer, _ bool) error {
			return filepath.WalkDir(lp, func(p string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				rel, _ := filepath.Rel(lp, p)
				rp := path.Join(target, filepath.ToSlash(rel))
				if d.IsDir() {
					return cl.MkdirAll(rp)
				}
				if !d.Type().IsRegular() {
					return nil
				}
				if !tr.own.may(rp, func() bool { _, err := cl.Lstat(rp); return err == nil }) {
					if fi, err := d.Info(); err == nil {
						tr.done.Add(fi.Size())
					}
					return nil
				}
				if err := uploadFile(ctx, cl, p, rp, &tr.done); err != nil {
					return err
				}
				tr.own.wrote(rp)
				return nil
			})
		}))
	}
	slog.Info("subida pedida", "items", len(localPaths), "omitidos", len(localPaths)-len(ids), "dest", remoteDir, "conn", m.sessionConn(sessionID))
	return ids, nil
}

// Download baja archivos o carpetas remotas a localDir. Devuelve los IDs.
func (m *Manager) Download(sessionID string, remotePaths []string, localDir, policy string) ([]string, error) {
	cl, err := m.sftpOf(sessionID)
	if err != nil {
		return nil, err
	}
	stats := make([]os.FileInfo, len(remotePaths))
	names := make([]string, len(remotePaths))
	for i, rp := range remotePaths {
		if stats[i], err = cl.Stat(rp); err != nil {
			return nil, err
		}
		if names[i], err = localName(path.Base(path.Clean(rp))); err != nil {
			return nil, err
		}
	}
	skip, err := checkConflicts(policy, names, func(i int) bool {
		_, err := os.Lstat(filepath.Join(localDir, names[i]))
		return err == nil
	})
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(remotePaths))
	for i, rp := range remotePaths {
		if skip[i] {
			continue
		}
		rp, st, name := path.Clean(rp), stats[i], names[i]
		target := filepath.Join(localDir, name)
		info := Transfer{SessionID: sessionID, Direction: DirDown, Name: name, Source: rp, Target: target}

		if !st.IsDir() {
			info.Total = st.Size()
			ids = append(ids, m.transfers.start(info, cl, policy, func(ctx context.Context, cl *sftp.Client, tr *transfer, resume bool) error {
				if !tr.own.may(target, func() bool { _, err := os.Lstat(target); return err == nil }) {
					tr.setDrop(nil)
					os.Remove(target + ".osat-part") // el pedazo de un intento anterior, si quedó
					tr.done.Store(st.Size())
					return nil
				}
				if st.Size() > resumeMin {
					return downloadPart(ctx, cl, rp, target, st, tr, resume)
				}
				return downloadFile(ctx, cl, rp, target, &tr.done)
			}))
			continue
		}

		var total int64
		w := cl.Walk(rp)
		for w.Step() {
			if w.Err() == nil && !w.Stat().IsDir() {
				total += w.Stat().Size()
			}
		}
		info.Total = total
		ids = append(ids, m.transfers.start(info, cl, policy, func(ctx context.Context, cl *sftp.Client, tr *transfer, _ bool) error {
			return downloadTree(ctx, cl, rp, target, &tr.done, tr.own)
		}))
	}
	slog.Info("descarga pedida", "items", len(remotePaths), "omitidos", len(remotePaths)-len(ids), "dest", localDir, "conn", m.sessionConn(sessionID))
	return ids, nil
}

// downloadTree baja la carpeta remota rp (recursiva) a target. own (nil = escribir todo) decide qué
// archivos de target se pueden escribir al reintentar con la política "omitir".
func downloadTree(ctx context.Context, cl *sftp.Client, rp, target string, ctr *atomic.Int64, own *owned) error {
	w := cl.Walk(rp)
	for w.Step() {
		if err := w.Err(); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		lp, err := localPathUnder(target, rp, w.Path())
		if err != nil {
			return err
		}
		if w.Stat().IsDir() {
			if err := os.MkdirAll(lp, 0o755); err != nil {
				return err
			}
			continue
		}
		if !w.Stat().Mode().IsRegular() {
			continue
		}
		if !own.may(lp, func() bool { _, err := os.Lstat(lp); return err == nil }) {
			ctr.Add(w.Stat().Size())
			continue
		}
		if err := downloadFile(ctx, cl, w.Path(), lp, ctr); err != nil {
			return err
		}
		own.wrote(lp)
	}
	return nil
}

const dragTempMaxAge = 24 * time.Hour

// DownloadToTemp baja las rutas remotas a una carpeta temporal nueva y
// devuelve las rutas locales de primer nivel (para arrastrarlas al sistema).
// Los temporales no se borran al terminar el arrastre (el Explorador puede
// seguir copiando en segundo plano): se limpian los de más de un día.
func (m *Manager) DownloadToTemp(sessionID string, remotePaths []string) ([]string, error) {
	cl, err := m.sftpOf(sessionID)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(os.TempDir(), "osat-drag")
	cleanOldDirs(root, dragTempMaxAge)
	dir := filepath.Join(root, uuid.NewString())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	ctx := context.Background()
	var ctr atomic.Int64
	out := make([]string, 0, len(remotePaths))
	for _, rp := range remotePaths {
		rp = path.Clean(rp)
		st, err := cl.Stat(rp)
		if err != nil {
			return nil, err
		}
		name, err := localName(path.Base(rp))
		if err != nil {
			return nil, err
		}
		target := filepath.Join(dir, name)
		if st.IsDir() {
			err = downloadTree(ctx, cl, rp, target, &ctr, nil)
		} else {
			err = downloadFile(ctx, cl, rp, target, &ctr)
		}
		if err != nil {
			os.RemoveAll(dir)
			return nil, err
		}
		out = append(out, target)
	}
	return out, nil
}

// cleanOldDirs borra las subcarpetas de root modificadas hace más de maxAge.
func cleanOldDirs(root string, maxAge time.Duration) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if fi, err := e.Info(); err == nil && e.IsDir() && time.Since(fi.ModTime()) > maxAge {
			os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
}

func (m *Manager) CancelTransfer(id string) { m.transfers.cancel(id) }
func (m *Manager) ClearTransfers()          { m.transfers.clearFinished() }

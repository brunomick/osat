package remote

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Los comandos se arman con todo entre comillas, comparando el string exacto: lo que escribe el usuario
// nunca se interpreta.
func TestFindCommand(t *testing.T) {
	cases := []struct {
		dir, pattern, contains string
		printf                 bool
		want                   string
	}{
		{"/srv/www", "*.conf", "", true, `LC_ALL=C find '/srv/www' -iname '*.conf' -printf '%y %s %p\n' 2>/dev/null`},
		{"/srv/www", "", "", true, `LC_ALL=C find '/srv/www' -iname '*' -printf '%y %s %p\n' 2>/dev/null`},
		{"/srv/www", "nginx*", "", false, `LC_ALL=C find '/srv/www' -iname 'nginx*' 2>/dev/null`},
		{"/srv/www", "*.php", "TODO", true, `LC_ALL=C find '/srv/www' -type f -iname '*.php' -size -10M -exec grep -IlF -- 'TODO' {} + 2>/dev/null`},
		{"/srv/www", "", "TODO", false, `LC_ALL=C find '/srv/www' -type f -iname '*' -size -10M -exec grep -IlF -- 'TODO' {} + 2>/dev/null`},
		// Comillas, ";", "$()", "-" al principio y espacios no se interpretan.
		{"/home/o'brien", "; rm -rf ~", "", true, `LC_ALL=C find '/home/o'\''brien' -iname '; rm -rf ~' -printf '%y %s %p\n' 2>/dev/null`},
		{"/srv/a b", "$(reboot)", "-rf 'x' `id`", false, `LC_ALL=C find '/srv/a b' -type f -iname '$(reboot)' -size -10M -exec grep -IlF -- '-rf '\''x'\'' ` + "`id`" + `' {} + 2>/dev/null`},
		{"/srv", "-delete", "", false, `LC_ALL=C find '/srv' -iname '-delete' 2>/dev/null`},
	}
	for _, c := range cases {
		if got := findCommand(c.dir, c.pattern, c.contains, c.printf); got != c.want {
			t.Errorf("findCommand(%q, %q, %q, %v)\n got  %s\n want %s", c.dir, c.pattern, c.contains, c.printf, got, c.want)
		}
	}
}

func TestParseFindLine(t *testing.T) {
	cases := []struct {
		line, root string
		kind       findOut
		want       FileEntry
		ok         bool
	}{
		{"f 120 /srv/www/index.html", "/srv/www", outTyped, FileEntry{Name: "index.html", Path: "/srv/www/index.html", Size: 120}, true},
		{"d 4096 /srv/www/css", "/srv/www", outTyped, FileEntry{Name: "css", Path: "/srv/www/css", Size: 4096, IsDir: true}, true},
		{"l 11 /srv/www/link", "/srv/www", outTyped, FileEntry{Name: "link", Path: "/srv/www/link", Size: 11, IsLink: true, Mode: "?"}, true},
		{"f 7 /srv/www/con espacios.txt", "/srv/www", outTyped, FileEntry{Name: "con espacios.txt", Path: "/srv/www/con espacios.txt", Size: 7}, true},
		{"f 7 /srv/www/x", "/", outTyped, FileEntry{Name: "x", Path: "/srv/www/x", Size: 7}, true},
		{"/srv/www/a/b.txt", "/srv/www", outPaths, FileEntry{Name: "b.txt", Path: "/srv/www/a/b.txt", Size: -1, Mode: "?"}, true},
		{"/srv/www/a/b.txt", "/srv/www", outFiles, FileEntry{Name: "b.txt", Path: "/srv/www/a/b.txt", Size: -1}, true},
		// Lo que no es un resultado: la carpeta misma, otra ruta, ruido del servidor, líneas mal formadas.
		{"d 4096 /srv/www", "/srv/www", outTyped, FileEntry{}, false},
		{"f 1 /srv/wwwx/a", "/srv/www", outTyped, FileEntry{}, false},
		{"f 1 /otra/a", "/srv/www", outTyped, FileEntry{}, false},
		{"Bienvenido a mi servidor", "/srv/www", outTyped, FileEntry{}, false},
		{"ff 1 /srv/www/a", "/srv/www", outTyped, FileEntry{}, false},
		{"f x /srv/www/a", "/srv/www", outTyped, FileEntry{}, false},
		{"Last login: hoy", "/srv/www", outPaths, FileEntry{}, false},
	}
	for _, c := range cases {
		got, ok := parseFindLine(c.line, c.root, c.kind)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parseFindLine(%q, %q) = %+v, %v; quería %+v, %v", c.line, c.root, got, ok, c.want, c.ok)
		}
	}
}

// ---- servidor de prueba ----

// evLog junta los eventos de las búsquedas.
type evLog struct {
	mu      sync.Mutex
	results []FileEntry
	batches int
	done    chan SearchDone
}

func (l *evLog) emit(ev string, data ...interface{}) {
	switch {
	case strings.HasPrefix(ev, "search:results:"):
		l.mu.Lock()
		l.results = append(l.results, data[0].([]FileEntry)...)
		l.batches++
		l.mu.Unlock()
	case strings.HasPrefix(ev, "search:done:"):
		l.done <- data[0].(SearchDone)
	}
}

func (l *evLog) got() []FileEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]FileEntry(nil), l.results...)
}

func (l *evLog) wait(t *testing.T) SearchDone {
	t.Helper()
	select {
	case d := <-l.done:
		return d
	case <-time.After(15 * time.Second):
		t.Fatal("la búsqueda no terminó a tiempo")
		return SearchDone{}
	}
}

// fakeServer simula find, grep y el shell: responde a la detección y entrega a out los comandos de búsqueda.
type fakeServer struct {
	find, gnu, grep bool
	out             func(cmd string, ch ssh.Channel) int // un comando "LC_ALL=C find '…"

	mu   sync.Mutex
	cmds []string
}

func (f *fakeServer) run(cmd string, ch ssh.Channel) int {
	f.mu.Lock()
	f.cmds = append(f.cmds, cmd)
	f.mu.Unlock()
	status := func(ok bool) int {
		if ok {
			return 0
		}
		return 1
	}
	switch {
	case cmd == "command -v find":
		return status(f.find)
	case cmd == "LC_ALL=C find / -maxdepth 0 -printf ''":
		return status(f.gnu)
	case cmd == "command -v grep":
		return status(f.grep)
	case strings.HasPrefix(cmd, "LC_ALL=C find '"):
		return f.out(cmd, ch)
	}
	io.WriteString(ch.Stderr(), "comando inesperado\n")
	return 2
}

func (f *fakeServer) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.cmds...)
}

func (f *fakeServer) count(cmd string) (n int) {
	for _, c := range f.commands() {
		if c == cmd {
			n++
		}
	}
	return
}

// searchRig: un Manager con la sesión SFTP "srv" (con un árbol en memoria) y, si f no es nil, exec falso.
func searchRig(t *testing.T, f *fakeServer) (*Manager, *evLog, *sftp.Client) {
	t.Helper()
	log := &evLog{done: make(chan SearchDone, 4)}
	m := NewManager(nil, nil, log.emit)
	t.Cleanup(m.CloseAll)
	cl := memSFTP(t)
	var client *ssh.Client
	if f == nil {
		client = forwardingServer(t) // acepta la conexión pero no abre canales de comandos
	} else {
		client = execServer(t, f.run)
	}
	m.register(&Session{ID: "srv", Kind: KindSFTP, ConnID: "c", sftp: cl, shared: newSharedClient(client)})
	for _, d := range []string{"/srv", "/srv/www", "/srv/www/sub", "/srv/www/sub/deep", "/srv/www/node"} {
		if err := cl.Mkdir(d); err != nil {
			t.Fatal(err)
		}
	}
	return m, log, cl
}

func names(es []FileEntry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Path
	}
	return out
}

func sameStrings(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("%s:\n%q\nquería\n%q", what, got, want)
	}
}

func typedOut(lines ...string) func(string, ssh.Channel) int {
	return func(_ string, ch ssh.Channel) int {
		for _, l := range lines {
			io.WriteString(ch, l+"\n")
		}
		return 1 // find sale con 1 si alguna carpeta no se pudo leer
	}
}

// ---- búsquedas con find ----

func TestSearchWithFind(t *testing.T) {
	f := &fakeServer{find: true, gnu: true, grep: true, out: typedOut(
		"Bienvenido al servidor", // ruido del .bashrc
		"d 4096 /srv/www/sub",
		"f 120 /srv/www/sub/a.conf",
		"l 11 /srv/www/enlace.conf",
		"f 1 /otro/lado.conf", // fuera de la carpeta: no es un resultado
		"f 9 /srv/www/con espacios.conf",
	)}
	m, log, _ := searchRig(t, f)

	root, err := m.SearchStart("s1", "srv", "/srv/../srv/www/", "*.conf", "")
	if err != nil || root != "/srv/www" {
		t.Fatalf("SearchStart: %q %v", root, err)
	}
	done := log.wait(t)
	got := log.got()
	sameStrings(t, "resultados", names(got), []string{"/srv/www/sub", "/srv/www/sub/a.conf", "/srv/www/enlace.conf", "/srv/www/con espacios.conf"})
	if !got[0].IsDir || got[1].IsDir || got[1].Size != 120 || got[1].Name != "a.conf" || !got[2].IsLink || got[2].Mode != "?" {
		t.Fatalf("tipos: %+v", got)
	}
	if done.Count != 4 || done.Truncated || done.Error != "" || done.ContentUnavailable {
		t.Fatalf("done: %+v", done)
	}
	sameStrings(t, "comandos", f.commands(), []string{
		"command -v find",
		"LC_ALL=C find / -maxdepth 0 -printf ''",
		"command -v grep",
		findCommand("/srv/www", "*.conf", "", true),
	})

	// Lo que se detectó se recuerda: la segunda búsqueda solo corre find.
	if _, err := m.SearchStart("s2", "srv", "/srv/www", "x", "TODO"); err != nil {
		t.Fatal(err)
	}
	log.wait(t)
	if n := f.count("command -v find"); n != 1 {
		t.Fatalf("la detección se repitió: %d veces", n)
	}
	if last := f.commands()[len(f.commands())-1]; last != findCommand("/srv/www", "x", "TODO", true) {
		t.Fatalf("búsqueda por contenido: %s", last)
	}
}

// Nada de lo que escribe el usuario se interpreta: llega entero y entre comillas al comando.
func TestSearchDangerousInput(t *testing.T) {
	f := &fakeServer{find: true, gnu: true, grep: true, out: typedOut()}
	m, log, cl := searchRig(t, f)
	cl.Mkdir("/srv/www/o'brien")
	pattern, contains := "; rm -rf ~ && $(reboot)", "`id` 'x' -rf"
	if _, err := m.SearchStart("s", "srv", "/srv/www/o'brien", pattern, contains); err != nil {
		t.Fatal(err)
	}
	log.wait(t)
	last := f.commands()[len(f.commands())-1]
	want := `LC_ALL=C find '/srv/www/o'\''brien' -type f -iname '; rm -rf ~ && $(reboot)' -size -10M -exec grep -IlF -- '` + "`id`" + ` '\''x'\'' -rf' {} + 2>/dev/null`
	if last != want {
		t.Fatalf("comando:\n%s\nquería\n%s", last, want)
	}
}

// Un find sin -printf (BusyBox) devuelve solo rutas: no se sabe el tipo ni el tamaño.
func TestSearchWithoutPrintf(t *testing.T) {
	f := &fakeServer{find: true, gnu: false, grep: true, out: typedOut("/srv/www/sub", "/srv/www/a.txt")}
	m, log, _ := searchRig(t, f)
	if _, err := m.SearchStart("s", "srv", "/srv/www", "", ""); err != nil {
		t.Fatal(err)
	}
	log.wait(t)
	got := log.got()
	if len(got) != 2 || got[0].Mode != "?" || got[0].Size != -1 || got[0].IsDir || got[1].Name != "a.txt" {
		t.Fatalf("resultados: %+v", got)
	}
	if last := f.commands()[len(f.commands())-1]; last != findCommand("/srv/www", "", "", false) {
		t.Fatalf("comando: %s", last)
	}
}

// Los archivos que salen de grep son archivos, sin tamaño.
func TestSearchContentResults(t *testing.T) {
	f := &fakeServer{find: true, gnu: true, grep: true, out: typedOut("/srv/www/a.php", "/srv/www/sub/b.php")}
	m, log, _ := searchRig(t, f)
	m.SearchStart("s", "srv", "/srv/www", "*.php", "TODO")
	done := log.wait(t)
	got := log.got()
	if done.Count != 2 || len(got) != 2 || got[0].IsDir || got[0].Mode != "" || got[0].Size != -1 {
		t.Fatalf("done=%+v resultados=%+v", done, got)
	}
}

// Al llegar al límite se deja de leer y se corta el comando en el servidor.
func TestSearchLimit(t *testing.T) {
	ended := make(chan struct{})
	f := &fakeServer{find: true, gnu: true, grep: true, out: func(_ string, ch ssh.Channel) int {
		defer close(ended)
		for i := 0; ; i++ {
			var b strings.Builder
			for j := 0; j < 100; j++ {
				fmt.Fprintf(&b, "f 1 /srv/www/f%d_%d\n", i, j)
			}
			if _, err := io.WriteString(ch, b.String()); err != nil {
				return 1
			}
			time.Sleep(2 * time.Millisecond)
		}
	}}
	m, log, _ := searchRig(t, f)
	m.SearchStart("s", "srv", "/srv/www", "", "")
	done := log.wait(t)
	if done.Count != 1000 || !done.Truncated || done.Error != "" || len(log.got()) != 1000 {
		t.Fatalf("done=%+v resultados=%d", done, len(log.got()))
	}
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("el comando siguió corriendo en el servidor")
	}
}

// Justo el límite no es "truncado": hacía falta uno más.
func TestSearchExactlyTheLimit(t *testing.T) {
	old := searchLimit
	searchLimit = 3
	t.Cleanup(func() { searchLimit = old })
	f := &fakeServer{find: true, gnu: true, grep: true, out: typedOut("f 1 /srv/www/a", "f 1 /srv/www/b", "f 1 /srv/www/c")}
	m, log, _ := searchRig(t, f)
	m.SearchStart("s", "srv", "/srv/www", "", "")
	if d := log.wait(t); d.Count != 3 || d.Truncated {
		t.Fatalf("done: %+v", d)
	}
}

// Los resultados salen en tandas a medida que llegan, y Detener corta la búsqueda.
func TestSearchStop(t *testing.T) {
	ended := make(chan struct{})
	f := &fakeServer{find: true, gnu: true, grep: true, out: func(_ string, ch ssh.Channel) int {
		defer close(ended)
		for i := 0; ; i++ {
			if _, err := fmt.Fprintf(ch, "f 1 /srv/www/f%d\n", i); err != nil {
				return 1
			}
			time.Sleep(20 * time.Millisecond)
		}
	}}
	m, log, _ := searchRig(t, f)
	m.SearchStart("s", "srv", "/srv/www", "", "")
	waitFor(t, "dos tandas de resultados", func() bool {
		log.mu.Lock()
		defer log.mu.Unlock()
		return log.batches >= 2
	})
	select {
	case <-log.done:
		t.Fatal("la búsqueda terminó sola")
	default:
	}
	m.SearchStop("s")
	done := log.wait(t)
	if done.Error != "" || done.Truncated || done.Count < 2 || done.Count != len(log.got()) {
		t.Fatalf("done=%+v resultados=%d", done, len(log.got()))
	}
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("el comando siguió corriendo en el servidor")
	}
	m.SearchStop("s") // ya terminó: no hace nada
	m.SearchStop("no-existe")
}

// Cerrar la sesión corta sus búsquedas (Manager.Close llama a stopSearches; acá no se cierra la sesión
// entera porque el cliente SFTP en memoria no se puede cerrar sin cortar antes sus pipes).
func TestSearchStopsWhenSessionCloses(t *testing.T) {
	ended := make(chan struct{})
	f := &fakeServer{find: true, gnu: true, grep: true, out: func(_ string, ch ssh.Channel) int {
		defer close(ended)
		for {
			if _, err := io.WriteString(ch, "f 1 /srv/www/x\n"); err != nil {
				return 1
			}
			time.Sleep(20 * time.Millisecond)
		}
	}}
	m, log, _ := searchRig(t, f)
	m.SearchStart("s", "srv", "/srv/www", "", "")
	waitFor(t, "resultados", func() bool { return len(log.got()) > 0 })
	m.stopSearches("otra")
	select {
	case <-log.done:
		t.Fatal("se cortó la búsqueda de otra sesión")
	case <-time.After(100 * time.Millisecond):
	}
	m.stopSearches("srv")
	log.wait(t)
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("el comando siguió corriendo en el servidor")
	}
}

// ---- respaldo por SFTP ----

func fillTree(t *testing.T, cl *sftp.Client) {
	t.Helper()
	put(t, cl, "/srv/www/a.CONF", "a")
	put(t, cl, "/srv/www/b.txt", "bb")
	put(t, cl, "/srv/www/sub/c.conf", "ccc")
	put(t, cl, "/srv/www/sub/deep/d.txt", "d")
	put(t, cl, "/srv/www/node/NGINX.conf", "n")
	put(t, cl, "/srv/www/; rm -rf ~", "x")
	put(t, cl, "/srv/afuera.conf", "no está en la carpeta buscada")
}

// Sin find (o sin exec) se recorre por SFTP: por nombre, sin mayúsculas, y el contenido no está disponible.
func TestSearchWalkFallback(t *testing.T) {
	servers := map[string]*fakeServer{
		"sin exec": nil,
		"sin find": {find: false, out: typedOut()},
	}
	for name, f := range servers {
		t.Run(name, func(t *testing.T) {
			m, log, cl := searchRig(t, f)
			fillTree(t, cl)

			caps, err := m.SearchCaps("srv")
			if err != nil || caps.Content || caps.Note == "" {
				t.Fatalf("caps: %+v %v", caps, err)
			}

			m.SearchStart("s1", "srv", "/srv/www", "*.CoNf", "")
			done := log.wait(t)
			sameStrings(t, "*.conf", names(log.got()), []string{"/srv/www/a.CONF", "/srv/www/node/NGINX.conf", "/srv/www/sub/c.conf"})
			if done.Count != 3 || done.Truncated || done.Error != "" || done.ContentUnavailable {
				t.Fatalf("done: %+v", done)
			}
			if g := log.got(); g[0].IsDir || g[0].Size != 1 || g[0].Name != "a.CONF" {
				t.Fatalf("entrada: %+v", g[0])
			}

			// Un nombre como "; rm -rf ~" se busca literalmente.
			log.mu.Lock()
			log.results = nil
			log.mu.Unlock()
			m.SearchStart("s2", "srv", "/srv/www", "; rm -rf ~", "")
			log.wait(t)
			sameStrings(t, "nombre literal", names(log.got()), []string{"/srv/www/; rm -rf ~"})

			// Con contenido: se busca solo por nombre y se avisa.
			log.mu.Lock()
			log.results = nil
			log.mu.Unlock()
			m.SearchStart("s3", "srv", "/srv/www/sub", "*.txt", "algo")
			done = log.wait(t)
			sameStrings(t, "con contenido", names(log.got()), []string{"/srv/www/sub/deep/d.txt"})
			if !done.ContentUnavailable {
				t.Fatalf("done: %+v", done)
			}
			if f != nil {
				// Se averiguó que no hay find y no se intentó correr uno.
				for _, c := range f.commands() {
					if strings.HasPrefix(c, "LC_ALL=C find '") {
						t.Fatalf("corrió find: %s", c)
					}
				}
			}
		})
	}
}

func TestSearchWalkLimit(t *testing.T) {
	old := searchLimit
	searchLimit = 2
	t.Cleanup(func() { searchLimit = old })
	m, log, cl := searchRig(t, nil)
	fillTree(t, cl)
	m.SearchStart("s", "srv", "/srv/www", "*", "")
	done := log.wait(t)
	if done.Count != 2 || !done.Truncated || len(log.got()) != 2 {
		t.Fatalf("done=%+v resultados=%d", done, len(log.got()))
	}
}

func TestSearchWalkStop(t *testing.T) {
	m, log, cl := searchRig(t, nil)
	for i := 0; i < 300; i++ {
		put(t, cl, fmt.Sprintf("/srv/www/node/f%03d", i), "x")
	}
	m.SearchStart("s", "srv", "/srv/www", "*", "")
	m.SearchStop("s")
	done := log.wait(t)
	if done.Error != "" || done.Truncated || done.Count >= 300 {
		t.Fatalf("done: %+v", done)
	}
}

// ---- capacidades y errores ----

func TestSearchCaps(t *testing.T) {
	for name, tc := range map[string]struct {
		f       *fakeServer
		content bool
		note    string
	}{
		"todo":     {&fakeServer{find: true, gnu: true, grep: true}, true, ""},
		"sin grep": {&fakeServer{find: true, gnu: true}, false, "grep"},
		"sin find": {&fakeServer{}, false, "find"},
		"sin exec": {nil, false, "ejecutar comandos"},
	} {
		t.Run(name, func(t *testing.T) {
			m, _, _ := searchRig(t, tc.f)
			caps, err := m.SearchCaps("srv")
			if err != nil || caps.Content != tc.content || (tc.note == "") != (caps.Note == "") || !strings.Contains(caps.Note, tc.note) {
				t.Fatalf("caps: %+v %v", caps, err)
			}
		})
	}
}

func TestSearchSinGrepSigueConFind(t *testing.T) {
	f := &fakeServer{find: true, gnu: true, out: typedOut("f 1 /srv/www/a.txt")}
	m, log, _ := searchRig(t, f)
	m.SearchStart("s", "srv", "/srv/www", "*.txt", "algo")
	done := log.wait(t)
	if !done.ContentUnavailable || done.Count != 1 {
		t.Fatalf("done: %+v", done)
	}
	if last := f.commands()[len(f.commands())-1]; last != findCommand("/srv/www", "*.txt", "", true) {
		t.Fatalf("comando: %s", last)
	}
}

func TestSearchStartErrors(t *testing.T) {
	m, log, cl := searchRig(t, nil)
	put(t, cl, "/srv/www/a.txt", "a")
	for name, call := range map[string]func() error{
		"sin id":          func() error { _, err := m.SearchStart("", "srv", "/srv/www", "", ""); return err },
		"sesión":          func() error { _, err := m.SearchStart("x", "nada", "/srv/www", "", ""); return err },
		"no existe":       func() error { _, err := m.SearchStart("x", "srv", "/srv/nada", "", ""); return err },
		"no es carpeta":   func() error { _, err := m.SearchStart("x", "srv", "/srv/www/a.txt", "", ""); return err },
		"nulo en nombre":  func() error { _, err := m.SearchStart("x", "srv", "/srv/www", "a\x00b", ""); return err },
		"salto de línea":  func() error { _, err := m.SearchStart("x", "srv", "/srv/www", "", "uno\ndos"); return err },
		"nulo en carpeta": func() error { _, err := m.SearchStart("x", "srv", "/srv\x00", "", ""); return err },
	} {
		if call() == nil {
			t.Errorf("%s: tendría que fallar", name)
		}
	}
	select {
	case d := <-log.done:
		t.Fatalf("una búsqueda inválida no tiene que arrancar: %+v", d)
	case <-time.After(100 * time.Millisecond):
	}

	// Un id repetido mientras la primera sigue en curso.
	f := &fakeServer{find: true, gnu: true, grep: true, out: func(_ string, ch ssh.Channel) int {
		time.Sleep(300 * time.Millisecond)
		return 0
	}}
	m2, log2, _ := searchRig(t, f)
	if _, err := m2.SearchStart("dup", "srv", "/srv/www", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := m2.SearchStart("dup", "srv", "/srv/www", "", ""); err == nil {
		t.Fatal("un id repetido tendría que fallar")
	}
	log2.wait(t)
}

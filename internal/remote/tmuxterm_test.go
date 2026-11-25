package remote

import (
	"bytes"
	"strings"
	"testing"
)

type fakeTmux struct {
	in     bytes.Buffer
	out    bytes.Buffer
	names  []string
	closed bool
}

func newFakeCtl(readOnly bool) (*tmuxCtl, *fakeTmux) {
	f := &fakeTmux{}
	c := newTmuxCtl(&f.in, "$3", readOnly, func(b []byte) { f.out.Write(b) }, func(n string) { f.names = append(f.names, n) })
	return c, f
}

func feedLines(c *tmuxCtl, lines ...string) {
	for _, l := range lines {
		c.line([]byte(l))
	}
}

func TestTmuxTermReplaysHistoryThenOutput(t *testing.T) {
	c, f := newFakeCtl(false)
	c.start(90, 20)
	if got := f.in.String(); got != "refresh-client -C 90,20\n"+
		"display-message -p -t '$3' '#{pane_id} #{cursor_x} #{cursor_y} #{pane_height} #{alternate_on}'\n"+
		"capture-pane -p -e -S -2000 -t '$3'\n" {
		t.Fatalf("comandos al conectar:\n%s", got)
	}
	feedLines(c,
		"%begin 1 1 0", "%end 1 1 0", // el del propio attach
		"%output %0 antes-de-capturar", // ya está en la captura: no se repite
		"%begin 1 2 1", "%end 1 2 1",   // refresh-client
		"%begin 1 3 1", "%0 4 1 2 0", "%end 1 3 1", // display-message
		"%begin 1 4 1", "viejo", "$ ls", "%end 1 4 1", // capture-pane: historial + 2 líneas visibles
		`%output %0 nuevo\015\012`,
		"%output %9 de otro panel",
	)
	want := "\x1b[2J\x1b[3J\x1b[H" + "viejo\r\n$ ls" + "\x1b[2;5H" + "nuevo\r\n"
	if got := f.out.String(); got != want {
		t.Fatalf("salida:\n%q\n%q", got, want)
	}
}

func TestTmuxTermAlternateScreen(t *testing.T) {
	c, f := newFakeCtl(false)
	c.start(80, 2)
	feedLines(c,
		"%begin 1 2 1", "%end 1 2 1",
		"%begin 1 3 1", "%0 0 0 2 1", "%end 1 3 1",
		"%begin 1 4 1", "historia", "vim 1", "vim 2", "%end 1 4 1",
	)
	want := "\x1b[2J\x1b[3J\x1b[H" + "\x1b[?1049h\x1b[H" + "vim 1\r\nvim 2" + "\x1b[1;1H"
	if got := f.out.String(); got != want {
		t.Fatalf("salida:\n%q\n%q", got, want)
	}
}

func TestTmuxTermWriteSendsKeys(t *testing.T) {
	c, f := newFakeCtl(false)
	c.write([]byte("ls\r"))
	if got := f.in.String(); got != "send-keys -t '$3' -H 6c 73 0d\n" {
		t.Fatalf("got %q", got)
	}
	f.in.Reset()
	c.resize(100, 30)
	if got := f.in.String(); got != "refresh-client -C 100,30\n" {
		t.Fatalf("got %q", got)
	}
}

func TestTmuxTermReadOnlyNoResize(t *testing.T) {
	c, f := newFakeCtl(true)
	c.start(90, 20)
	if strings.Contains(f.in.String(), "refresh-client") {
		t.Fatalf("solo mirar no cambia el tamaño: %q", f.in.String())
	}
	f.in.Reset()
	c.write([]byte("x"))
	c.resize(10, 10)
	if f.in.Len() != 0 {
		t.Fatalf("solo mirar no manda nada: %q", f.in.String())
	}
	// Sin refresh-client, la primera respuesta es la del display-message.
	feedLines(c, "%begin 1 3 1", "%0 0 0 1 0", "%end 1 3 1", "%begin 1 4 1", "hola", "%end 1 4 1")
	if got := f.out.String(); !strings.Contains(got, "hola") {
		t.Fatalf("got %q", got)
	}
}

func TestTmuxTermRenameAndExit(t *testing.T) {
	c, f := newFakeCtl(false)
	feedLines(c, "%session-renamed $3 nuevo nombre", "%session-renamed $4 de otra", "%exit")
	if len(f.names) != 1 || f.names[0] != "nuevo nombre" {
		t.Fatalf("names %q", f.names)
	}
	if !c.exited {
		t.Fatal("exit debe marcar el fin")
	}
}

func TestTmuxGone(t *testing.T) {
	for _, s := range []string{"can't find session: $3", "no server running on /tmp/tmux-1000/default", "error connecting to /tmp/tmux-0/default (No such file or directory)"} {
		if !tmuxGone(s) {
			t.Errorf("%q", s)
		}
	}
	if tmuxGone("open terminal failed: not a terminal") {
		t.Error("otro error no es una sesión que ya no existe")
	}
}

func TestTmuxTermCursorWithShorterOrTallerPane(t *testing.T) {
	// Solo mirar desde una terminal de 10 filas un panel de 3: el panel son las últimas 3 líneas escritas.
	c, f := newFakeCtl(true)
	c.start(80, 10)
	feedLines(c,
		"%begin 1 3 1", "%0 2 1 3 0", "%end 1 3 1",
		"%begin 1 4 1", "h1", "h2", "v1", "v2", "v3", "%end 1 4 1",
	)
	// 5 líneas escritas: el panel empieza en la fila 3; el cursor (y=1) va a la 4.
	if got := f.out.String(); !strings.HasSuffix(got, "\x1b[4;3H") {
		t.Fatalf("got %q", got)
	}
	// Un panel más alto que la terminal: el cursor no se pasa de la última fila.
	c2, f2 := newFakeCtl(true)
	c2.start(80, 2)
	feedLines(c2, "%begin 1 3 1", "%0 0 4 5 0", "%end 1 3 1", "%begin 1 4 1", "a", "b", "c", "d", "e", "%end 1 4 1")
	if got := f2.out.String(); !strings.HasSuffix(got, "\x1b[2;1H") {
		t.Fatalf("got %q", got)
	}
}

func TestTmuxTermReadOnlyWriteFails(t *testing.T) {
	c, _ := newFakeCtl(true)
	if _, err := c.Write([]byte("ls\r")); err == nil {
		t.Fatal("solo mirar: escribir tiene que fallar (el puente con Claude lo informa)")
	}
}

func TestControlParserEndInsideCapture(t *testing.T) {
	var p controlParser
	var replies []controlReply
	p.onReply = func(r controlReply) { replies = append(replies, r) }
	for _, l := range []string{"%begin 9 40 1", "%end 1 1 1", "%error de una pantalla", "%end 9 40 1"} {
		p.feed([]byte(l))
	}
	if len(replies) != 1 || len(replies[0].lines) != 2 {
		t.Fatalf("got %+v", replies)
	}
}

func TestValidTmuxID(t *testing.T) {
	for id, ok := range map[string]bool{"$0": true, "$12": true, "$": false, "": false, "3": false, "$1'; run-shell x": false} {
		if validTmuxID(id) != ok {
			t.Errorf("%q", id)
		}
	}
}

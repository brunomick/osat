package remote

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkg/sftp"

	"osat/internal/textenc"
)

// delayLine entrega lo que se escribe recién después de `delay`, como un
// enlace con latencia: los mensajes no se encolan uno detrás del otro, cada
// uno llega `delay` después de enviado (así los pedidos en paralelo valen).
type delayLine struct {
	mu     sync.Mutex
	closed bool
	ch     chan delayed
	done   chan struct{}
}

type delayed struct {
	b  []byte
	at time.Time
}

func newDelayLine(w io.WriteCloser) *delayLine {
	d := &delayLine{ch: make(chan delayed, 1024), done: make(chan struct{})}
	go func() {
		defer close(d.done)
		defer w.Close()
		for m := range d.ch {
			time.Sleep(time.Until(m.at))
			if _, err := w.Write(m.b); err != nil {
				return
			}
		}
	}()
	return d
}

// send falla (en vez de entrar en pánico) si el enlace ya se cerró: pasa con
// escrituras tardías, como el cierre asíncrono de readText.
func (d *delayLine) send(b []byte, delay time.Duration) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return io.ErrClosedPipe
	}
	d.ch <- delayed{append([]byte(nil), b...), time.Now().Add(delay)}
	return nil
}

func (d *delayLine) close() {
	d.mu.Lock()
	if !d.closed {
		d.closed = true
		close(d.ch)
	}
	d.mu.Unlock()
	<-d.done
}

type lagWriter struct {
	d     *delayLine
	delay time.Duration
}

func (w lagWriter) Write(b []byte) (int, error) {
	if err := w.d.send(b, w.delay); err != nil {
		return 0, err
	}
	return len(b), nil
}
func (w lagWriter) Close() error { w.d.close(); return nil }

// laggySFTP es memSFTP con `oneWay` de demora en cada sentido (RTT = 2×oneWay).
func laggySFTP(t *testing.T, oneWay time.Duration) *sftp.Client {
	t.Helper()
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	toClient := lagWriter{newDelayLine(sw), oneWay}
	toServer := lagWriter{newDelayLine(cw), oneWay}
	srv := sftp.NewRequestServer(struct {
		io.Reader
		io.WriteCloser
	}{sr, toClient}, sftp.InMemHandler())
	go srv.Serve()
	cl, err := sftp.NewClientPipe(cr, toServer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		toClient.Close()
		toServer.Close()
		srv.Close()
		cl.Close()
	})
	return cl
}

// Listar una carpeta con muchos symlinks no puede costar una ida y vuelta
// por symlink: antes eran ~5 + N viajes, ahora tienen que ser unos pocos.
func TestListDirRoundTrips(t *testing.T) {
	const oneWay = 10 * time.Millisecond
	const rtt = 2 * oneWay
	cl := laggySFTP(t, oneWay)

	if err := cl.MkdirAll("/d/real"); err != nil {
		t.Fatal(err)
	}
	put(t, cl, "/d/a.txt", "hola")
	const links = 20
	for i := 0; i < links; i++ {
		target := "/d/a.txt"
		if i%2 == 0 {
			target = "/d/real"
		}
		if err := cl.Symlink(target, fmt.Sprintf("/d/link%02d", i)); err != nil {
			t.Fatal(err)
		}
	}

	start := time.Now()
	l, err := listDir(cl, "/d/")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if l.Path != "/d" {
		t.Errorf("Path = %q, quería /d", l.Path)
	}
	if len(l.Entries) != links+2 {
		t.Fatalf("%d entradas, quería %d", len(l.Entries), links+2)
	}
	dirs := 0
	for _, e := range l.Entries {
		if e.IsLink && e.IsDir {
			dirs++
		}
	}
	if dirs != links/2 {
		t.Errorf("%d symlinks a carpetas, quería %d", dirs, links/2)
	}
	trips := float64(elapsed) / float64(rtt)
	t.Logf("listDir: %v ≈ %.1f idas y vueltas", elapsed, trips)
	if trips > 10 {
		t.Errorf("listDir hizo ≈ %.1f idas y vueltas; con %d symlinks en serie serían más de %d", trips, links, links)
	}
}

// Leer un archivo de texto para el editor tiene que costar unos pocos viajes
// fijos, no uno por cada pedazo que va pidiendo io.ReadAll.
func TestReadTextRoundTrips(t *testing.T) {
	const oneWay = 10 * time.Millisecond
	const rtt = 2 * oneWay
	cl := laggySFTP(t, oneWay)

	// 50 KB: más que un paquete SFTP (32 KB), así que la lectura se parte.
	var sb strings.Builder
	for i := 0; sb.Len() < 50*1024; i++ {
		fmt.Fprintf(&sb, "línea %05d: ñandú\n", i)
	}
	want := sb.String()
	put(t, cl, "/texto.txt", want)

	start := time.Now()
	got, err := readText(cl, "/texto.txt")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("contenido distinto: %d bytes, quería %d", len(got), len(want))
	}
	trips := float64(elapsed) / float64(rtt)
	t.Logf("readText 50 KB: %v ≈ %.1f idas y vueltas", elapsed, trips)
	if trips > 5 {
		t.Errorf("readText hizo ≈ %.1f idas y vueltas, quería ≤ 5", trips)
	}

	put(t, cl, "/vacio.txt", "")
	if s, err := readText(cl, "/vacio.txt"); err != nil || s != "" {
		t.Errorf("vacío: %q, %v", s, err)
	}
	put(t, cl, "/ansi.cpp", "informaci\xf3n")
	if s, enc, err := readTextEnc(cl, "/ansi.cpp"); err != nil || s != "información" || enc != textenc.Windows1252 {
		t.Errorf("Windows-1252: %q, %v, %v", s, enc, err)
	}
	put(t, cl, "/bin", "a\x00b")
	if _, err := readText(cl, "/bin"); err != errNotText {
		t.Errorf("binario: err = %v", err)
	}
	put(t, cl, "/grande", strings.Repeat("x", maxEditSize+1))
	if _, err := readText(cl, "/grande"); err != errTooBig {
		t.Errorf("grande: err = %v", err)
	}
}

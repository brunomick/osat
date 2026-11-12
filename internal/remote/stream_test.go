package remote

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func newTestShell(t *testing.T, run func(cmd string, ch ssh.Channel) int) *shell {
	t.Helper()
	return &shell{c: execServer(t, run), off: &atomic.Bool{}}
}

// syncBuffer es un bytes.Buffer que se puede leer mientras el canal escribe.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// El stdin llega entero al comando, el stdout vuelve completo y el código de salida y stderr se informan.
func TestShellStreamStdinAndStatus(t *testing.T) {
	var gotCmd string
	sh := newTestShell(t, func(cmd string, ch ssh.Channel) int {
		gotCmd = cmd
		in, _ := io.ReadAll(ch) // termina cuando el cliente cierra stdin
		ch.Write([]byte(strings.ToUpper(string(in))))
		io.WriteString(ch.Stderr(), "  se rompió\n")
		return 3
	})
	var out syncBuffer
	status, stderr, err := sh.stream(context.Background(), "cat", strings.NewReader("hola\nmundo\n"), &out)
	if err != nil {
		t.Fatal(err)
	}
	if status != 3 || stderr != "se rompió" || out.String() != "HOLA\nMUNDO\n" || gotCmd != "cat" {
		t.Fatalf("status=%d stderr=%q out=%q cmd=%q", status, stderr, out.String(), gotCmd)
	}
}

// Sin stdin ni stdout el comando corre igual (stdin vacío, salida descartada).
func TestShellStreamNilStreams(t *testing.T) {
	sh := newTestShell(t, func(cmd string, ch ssh.Channel) int {
		in, _ := io.ReadAll(ch)
		ch.Write(in)
		return 0
	})
	status, stderr, err := sh.stream(context.Background(), "true", nil, nil)
	if err != nil || status != 0 || stderr != "" {
		t.Fatalf("status=%d stderr=%q err=%v", status, stderr, err)
	}
}

// La salida se entrega a medida que llega: el primer pedazo se ve mientras el comando todavía no terminó.
func TestShellStreamDeliversOutputAsItArrives(t *testing.T) {
	release := make(chan struct{})
	sh := newTestShell(t, func(cmd string, ch ssh.Channel) int {
		ch.Write([]byte("uno\n"))
		<-release
		ch.Write([]byte("dos\n"))
		return 0
	})
	var out syncBuffer
	done := make(chan error, 1)
	go func() {
		_, _, err := sh.stream(context.Background(), "x", nil, &out)
		done <- err
	}()
	waitFor(t, "primer pedazo", func() bool { return out.String() == "uno\n" })
	select {
	case <-done:
		t.Fatal("el comando no había terminado y stream ya volvió")
	default:
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if out.String() != "uno\ndos\n" {
		t.Fatalf("salida: %q", out.String())
	}
}

// Cancelar corta el comando: stream vuelve enseguida con el error del contexto, el servidor ve
// cerrarse el canal y, ya devuelto, nada más se escribe en el destino.
func TestShellStreamCancel(t *testing.T) {
	ended := make(chan struct{})
	sh := newTestShell(t, func(cmd string, ch ssh.Channel) int {
		defer close(ended)
		for {
			if _, err := ch.Write([]byte("x")); err != nil {
				return 1
			}
			time.Sleep(5 * time.Millisecond)
		}
	})
	// stdin abierto: un comando de verdad (un `find` largo) no termina porque stdin se acabe.
	pr, pw := io.Pipe()
	t.Cleanup(func() { pw.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	var out syncBuffer
	var written atomic.Int32
	type result struct {
		err     error
		elapsed time.Duration
	}
	done := make(chan result, 1)
	go func() {
		start := time.Now()
		_, _, err := sh.stream(ctx, "find /", pr, writerFunc(func(p []byte) (int, error) {
			written.Add(1)
			return out.Write(p)
		}))
		done <- result{err, time.Since(start)}
	}()
	waitFor(t, "salida", func() bool { return out.String() != "" })
	cancel()

	var res result
	select {
	case res = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelar no cortó el comando")
	}
	if !errors.Is(res.err, context.Canceled) {
		t.Fatalf("error: %v", res.err)
	}
	after := written.Load()
	select {
	case <-ended:
	case <-time.After(3 * time.Second):
		t.Fatal("el servidor no vio cerrarse el canal")
	}
	if written.Load() != after {
		t.Fatal("se escribió en el destino después de cancelar")
	}
	if sh.off.Load() {
		t.Fatal("cancelar no debe marcar el shell como inusable")
	}
}

type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// Si el destino falla al escribir se informa ese error, no "el servidor no deja ejecutar comandos".
func TestShellStreamWriterError(t *testing.T) {
	sh := newTestShell(t, func(cmd string, ch ssh.Channel) int {
		ch.Write([]byte("algo"))
		return 0
	})
	boom := errors.New("disco lleno")
	_, _, err := sh.stream(context.Background(), "x", nil, writerFunc(func([]byte) (int, error) { return 0, boom }))
	if !errors.Is(err, boom) {
		t.Fatalf("error: %v", err)
	}
	if sh.off.Load() {
		t.Fatal("un error del destino no debe marcar el shell como inusable")
	}
}

// Un servidor que no abre canales de comandos da errNoShell y se recuerda.
func TestShellStreamNoExec(t *testing.T) {
	sh := &shell{c: forwardingServer(t), off: &atomic.Bool{}}
	_, _, err := sh.stream(context.Background(), "x", nil, io.Discard)
	if !errors.Is(err, errNoShell) || sh.usable() {
		t.Fatalf("err=%v usable=%v", err, sh.usable())
	}
}

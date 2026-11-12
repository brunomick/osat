package portfwd

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// echoServer contesta en mayúsculas lo que le mandan, hasta que el cliente cierra su envío.
func echoServer(t *testing.T) (addr string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				b, _ := io.ReadAll(c)
				c.Write([]byte(strings.ToUpper(string(b))))
			}()
		}
	}()
	return ln.Addr().String(), func() { ln.Close() }
}

func dialTo(addr string) Dial {
	return func(ctx context.Context) (net.Conn, error) { return (&net.Dialer{}).DialContext(ctx, "tcp", addr) }
}

func roundTrip(t *testing.T, port int, msg string) string {
	t.Helper()
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	c.Write([]byte(msg))
	c.(*net.TCPConn).CloseWrite() // el servidor contesta cuando termina de leer: tiene que llegar el cierre de envío
	b, err := io.ReadAll(c)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestForwardsConnectionsAndCountsThem(t *testing.T) {
	addr, stop := echoServer(t)
	defer stop()
	l, err := Listen(0, dialTo(addr))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	if got := roundTrip(t, l.Port(), "hola redis"); got != "HOLA REDIS" {
		t.Fatalf("respuesta: %q", got)
	}
	roundTrip(t, l.Port(), "otra")

	deadline := time.Now().Add(2 * time.Second)
	for l.Stats().Active != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	s := l.Stats()
	if s.Total != 2 || s.Active != 0 || s.BytesOut != int64(len("hola redis")+len("otra")) || s.BytesIn != s.BytesOut || s.LastErr != "" {
		t.Fatalf("stats: %+v", s)
	}
}

func TestListensOnlyOnLoopback(t *testing.T) {
	l, err := Listen(0, dialTo("127.0.0.1:1"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	for _, ln := range l.lns {
		if !ln.Addr().(*net.TCPAddr).IP.IsLoopback() {
			t.Fatalf("escucha fuera del loopback: %s", ln.Addr())
		}
	}
}

func TestDialFailureIsReportedAndRecovers(t *testing.T) {
	addr, stop := echoServer(t)
	defer stop()
	fail := true
	l, err := Listen(0, func(ctx context.Context) (net.Conn, error) {
		if fail {
			return nil, errors.New("no hay nada escuchando en redis:6379")
		}
		return dialTo(addr)(ctx)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(l.Port())))
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(2 * time.Second))
	io.ReadAll(c) // el reenvío cierra la conexión
	c.Close()
	if s := l.Stats(); s.LastErr == "" || !strings.Contains(s.LastErr, "redis:6379") {
		t.Fatalf("debería contar por qué falló: %+v", s)
	}

	fail = false
	if got := roundTrip(t, l.Port(), "ok"); got != "OK" {
		t.Fatalf("respuesta: %q", got)
	}
	if s := l.Stats(); s.LastErr != "" {
		t.Fatalf("el error viejo debería borrarse: %+v", s)
	}
}

func TestClosingCutsOpenConnectionsAndFreesThePort(t *testing.T) {
	addr, stop := echoServer(t)
	defer stop()
	l, err := Listen(0, dialTo(addr))
	if err != nil {
		t.Fatal(err)
	}
	port := l.Port()
	open, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer open.Close()
	time.Sleep(50 * time.Millisecond) // que el reenvío la tome

	done := make(chan struct{})
	go func() { l.Close(); l.Close(); close(done) }() // idempotente
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close se colgó con una conexión abierta")
	}
	open.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := open.Read(make([]byte, 1)); err == nil {
		t.Fatal("la conexión abierta tenía que cortarse")
	}
	if _, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second); err == nil {
		t.Fatal("el puerto debería estar libre")
	}
}

func TestPortInUseHasAReadableError(t *testing.T) {
	l, err := Listen(0, dialTo("127.0.0.1:1"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	_, err = Listen(l.Port(), dialTo("127.0.0.1:1"))
	if err == nil || !strings.Contains(err.Error(), "otro programa") || !strings.Contains(err.Error(), strconv.Itoa(l.Port())) {
		t.Fatalf("error: %v", err)
	}
}

func TestFreePort(t *testing.T) {
	l, err := Listen(0, dialTo("127.0.0.1:1"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if got := FreePort(l.Port()); got == l.Port() || got == 0 {
		t.Fatalf("el puerto %d está tomado, devolvió %d", l.Port(), got)
	}
}

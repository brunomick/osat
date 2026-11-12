package vncproxy

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// tcpServer hace de servidor VNC: entrega cada conexión que le llega.
type tcpServer struct {
	ln    net.Listener
	conns chan net.Conn
}

func newTCPServer(t *testing.T) *tcpServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &tcpServer{ln: ln, conns: make(chan net.Conn, 4)}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { c.Close() })
			s.conns <- c
		}
	}()
	return s
}

func (s *tcpServer) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

func (s *tcpServer) accept(t *testing.T) net.Conn {
	t.Helper()
	select {
	case c := <-s.conns:
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("el servidor no recibió la conexión")
		return nil
	}
}

func newProxy(t *testing.T) *Proxy {
	t.Helper()
	p := New()
	t.Cleanup(p.CloseAll)
	return p
}

func dialWS(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	ws, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close() })
	return ws
}

// readEOF verifica que el otro lado cerró la conexión.
func readEOF(t *testing.T, c net.Conn) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("la conexión TCP no se cerró: %v", err)
	}
}

func TestRelaysBothWays(t *testing.T) {
	srv := newTCPServer(t)
	p := newProxy(t)
	tk, err := p.Open("127.0.0.1", srv.port(), nil)
	if err != nil {
		t.Fatal(err)
	}
	// noVNC pide el subprotocolo "binary".
	d := websocket.Dialer{Subprotocols: []string{"binary"}}
	ws, _, err := d.Dial(tk.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close() })
	if ws.Subprotocol() != "binary" {
		t.Fatalf("subprotocolo %q", ws.Subprotocol())
	}
	c := srv.accept(t)

	// Bytes que no son texto UTF-8 válido, para comprobar que viajan en binario.
	toServer := []byte{0x00, 0xff, 0xfe, 'R', 'F', 'B', 0x80}
	if err := ws.WriteMessage(websocket.BinaryMessage, toServer); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(toServer))
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(c, got); err != nil || !bytes.Equal(got, toServer) {
		t.Fatalf("el servidor recibió %v, %v", got, err)
	}

	toClient := []byte("RFB 003.008\n\xff\x00")
	if _, err := c.Write(toClient); err != nil {
		t.Fatal(err)
	}
	var recv []byte
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	for len(recv) < len(toClient) {
		typ, m, err := ws.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if typ != websocket.BinaryMessage {
			t.Fatalf("mensaje de tipo %d, quería binario", typ)
		}
		recv = append(recv, m...)
	}
	if !bytes.Equal(recv, toClient) {
		t.Fatalf("el cliente recibió %q", recv)
	}
}

func TestTokenSingleUse(t *testing.T) {
	srv := newTCPServer(t)
	p := newProxy(t)
	tk, err := p.Open("127.0.0.1", srv.port(), nil)
	if err != nil {
		t.Fatal(err)
	}
	dialWS(t, tk.URL)
	srv.accept(t)
	if ws, _, err := websocket.DefaultDialer.Dial(tk.URL, nil); err == nil {
		ws.Close()
		t.Fatal("un segundo WebSocket con el mismo token se aceptó")
	}
}

func TestBadTokenRejected(t *testing.T) {
	srv := newTCPServer(t)
	p := newProxy(t)
	tk, err := p.Open("127.0.0.1", srv.port(), nil)
	if err != nil {
		t.Fatal(err)
	}
	// Otra ruta: 403, y la sesión sigue libre para el token verdadero.
	bad := tk.URL[:len(tk.URL)-1] + "x"
	if tk.URL[len(tk.URL)-1] == 'x' {
		bad = tk.URL[:len(tk.URL)-1] + "y"
	}
	ws, resp, err := websocket.DefaultDialer.Dial(bad, nil)
	if err == nil {
		ws.Close()
		t.Fatal("se aceptó un token incorrecto")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("respuesta = %v, quería 403", resp)
	}
	// Host distinto (DNS rebinding): 403 aunque el token sea el verdadero.
	req, _ := http.NewRequest("GET", "http"+tk.URL[len("ws"):], nil)
	req.Host = "evil.example:80"
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusForbidden {
		t.Fatalf("con otro Host: %d, quería 403", r.StatusCode)
	}
	// Un pedido con el token pero que no es WebSocket tampoco lo gasta.
	r, err = http.Get("http" + tk.URL[len("ws"):])
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusBadRequest {
		t.Fatalf("sin WebSocket: %d, quería 400", r.StatusCode)
	}
	dialWS(t, tk.URL)
	srv.accept(t)
}

func TestOpenRefusedFailsFast(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close() // puerto cerrado
	p := newProxy(t)
	start := time.Now()
	_, err = p.Open("127.0.0.1", port, nil)
	if err == nil {
		t.Fatal("conectó a un puerto cerrado")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("tardó %v en fallar", d)
	}
	if err.Error() != "el servidor rechazó la conexión (¿está corriendo el servidor VNC en ese puerto?)" {
		t.Fatalf("error = %q", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.sessions) != 0 {
		t.Fatal("quedó una sesión registrada")
	}
}

func TestUnclaimedTicketExpires(t *testing.T) {
	old := connectWindow
	connectWindow = 100 * time.Millisecond
	t.Cleanup(func() { connectWindow = old })
	srv := newTCPServer(t)
	p := newProxy(t)
	tk, err := p.Open("127.0.0.1", srv.port(), nil)
	if err != nil {
		t.Fatal(err)
	}
	readEOF(t, srv.accept(t))
	if ws, _, err := websocket.DefaultDialer.Dial(tk.URL, nil); err == nil {
		ws.Close()
		t.Fatal("el ticket vencido se aceptó")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.sessions) != 0 {
		t.Fatal("la sesión vencida sigue registrada")
	}
}

func TestCloseClosesTCP(t *testing.T) {
	srv := newTCPServer(t)
	p := newProxy(t)

	// Con el WebSocket conectado.
	tk, err := p.Open("127.0.0.1", srv.port(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ws := dialWS(t, tk.URL)
	c := srv.accept(t)
	p.Close(tk.ID)
	readEOF(t, c)
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := ws.ReadMessage(); err == nil {
		t.Fatal("el WebSocket sigue abierto")
	}

	// Sin que nadie haya llegado.
	tk, err = p.Open("127.0.0.1", srv.port(), nil)
	if err != nil {
		t.Fatal(err)
	}
	c = srv.accept(t)
	p.Close(tk.ID)
	readEOF(t, c)

	// Al cerrar osat.
	tk, err = p.Open("127.0.0.1", srv.port(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ws = dialWS(t, tk.URL)
	c = srv.accept(t)
	p.CloseAll()
	readEOF(t, c)
	if _, err := p.Open("127.0.0.1", srv.port(), nil); err == nil {
		t.Fatal("abrió una sesión después de CloseAll")
	}
}

// Si el servidor corta, el WebSocket se cierra y la sesión desaparece.
func TestServerHangupClosesSession(t *testing.T) {
	srv := newTCPServer(t)
	p := newProxy(t)
	tk, err := p.Open("127.0.0.1", srv.port(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ws := dialWS(t, tk.URL)
	srv.accept(t).Close()
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := ws.ReadMessage(); err == nil {
		t.Fatal("el WebSocket sigue abierto")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		p.mu.Lock()
		n := len(p.sessions)
		p.mu.Unlock()
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("la sesión cortada sigue registrada")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// fakeVia hace de bastión: registra qué dirección le piden y abre la
// conexión hacia dest (el servidor de prueba), sea cual sea esa dirección.
type fakeVia struct {
	dest string
	err  error

	mu     sync.Mutex
	asked  []string
	closed bool
}

func (v *fakeVia) Dial(network, addr string) (net.Conn, error) {
	v.mu.Lock()
	v.asked = append(v.asked, addr)
	v.mu.Unlock()
	if v.err != nil {
		return nil, v.err
	}
	return net.Dial(network, v.dest)
}

func (v *fakeVia) Close() error {
	v.mu.Lock()
	v.closed = true
	v.mu.Unlock()
	return nil
}

func (v *fakeVia) state() (asked []string, closed bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]string(nil), v.asked...), v.closed
}

func TestViaUsedForDial(t *testing.T) {
	srv := newTCPServer(t)
	p := newProxy(t)
	v := &fakeVia{dest: srv.ln.Addr().String()}
	// "localhost" es el del servidor al que se llega por el bastión, no el de esta PC:
	// si se usara net.Dial, 5901 no tendría a nadie escuchando.
	tk, err := p.Open("localhost", 5901, v)
	if err != nil {
		t.Fatal(err)
	}
	if asked, closed := v.state(); len(asked) != 1 || asked[0] != "localhost:5901" || closed {
		t.Fatalf("el bastión recibió %v (cerrado: %v)", asked, closed)
	}
	ws := dialWS(t, tk.URL)
	c := srv.accept(t)
	if err := ws.WriteMessage(websocket.BinaryMessage, []byte("hola")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 4)
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(c, got); err != nil || string(got) != "hola" {
		t.Fatalf("el servidor recibió %q, %v", got, err)
	}
	// Cerrar la sesión cierra el bastión.
	p.Close(tk.ID)
	if _, closed := v.state(); !closed {
		t.Fatal("cerrar la sesión no cerró el bastión")
	}
}

func TestViaClosedOnOpenError(t *testing.T) {
	p := newProxy(t)
	v := &fakeVia{err: errors.New("connection refused")}
	if _, err := p.Open("localhost", 5901, v); err == nil {
		t.Fatal("no dio error")
	}
	if _, closed := v.state(); !closed {
		t.Fatal("el bastión quedó abierto después del error")
	}
}

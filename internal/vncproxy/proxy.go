// Package vncproxy hace de puente entre noVNC (que corre en el WebView y solo
// habla WebSocket) y un servidor VNC: pasa los bytes en los dos sentidos,
// en mensajes binarios, como websockify. El WebView no puede abrir TCP, y el
// salto por una conexión SSH también lo abre este paquete.
//
// Cada sesión tiene su propio puerto en 127.0.0.1 y una ruta secreta de un
// solo uso; el destino lo fija osat (no el pedido), así que el proxy no sirve
// de relé hacia otros hosts.
package vncproxy

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"osat/internal/i18n"
)

// Variables (no const) para poder acortarlas en los tests.
var (
	// Si el frontend no se conecta en este tiempo, la sesión se descarta.
	connectWindow = 30 * time.Second
	dialTimeout   = 15 * time.Second
)

// Ticket es lo que noVNC necesita para conectarse.
type Ticket struct {
	ID  string `json:"id"`
	URL string `json:"url"` // ws://127.0.0.1:<puerto>/<token>
}

// Via es el salto (ProxyJump) por el que se llega al servidor: Dial abre la
// conexión TCP desde el bastión (un *ssh.Client cumple la interfaz) y Close
// lo cierra cuando termina la sesión.
type Via interface {
	Dial(network, addr string) (net.Conn, error)
	Close() error
}

type Proxy struct {
	mu       sync.Mutex
	sessions map[string]*session
	shut     bool // CloseAll ya corrió (la app se está cerrando)
}

func New() *Proxy { return &Proxy{sessions: map[string]*session{}} }

// Open conecta con el servidor (así un "conexión rechazada" se informa
// enseguida) y deja la conexión esperando a que llegue el WebSocket. via es
// opcional: si está, el servidor se alcanza desde el bastión (host es el que
// ve el bastión, por ejemplo localhost). La sesión se queda con via: lo cierra
// al terminar, o enseguida si hay error.
func (p *Proxy) Open(host string, port int, via Via) (Ticket, error) {
	t, err := p.open(host, port, via)
	if err != nil && via != nil {
		via.Close()
	}
	return t, err
}

func (p *Proxy) open(host string, port int, via Via) (Ticket, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	var conn net.Conn
	var err error
	if via != nil {
		conn, err = dialVia(via, addr)
	} else if conn, err = net.DialTimeout("tcp", addr, dialTimeout); err != nil {
		err = friendlyDial(err)
	}
	if err != nil {
		return Ticket{}, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		conn.Close()
		return Ticket{}, err
	}
	tok := make([]byte, 32)
	if _, err := rand.Read(tok); err != nil {
		ln.Close()
		conn.Close()
		return Ticket{}, err
	}
	s := &session{
		p: p, id: uuid.NewString(), addr: addr, token: hex.EncodeToString(tok),
		ln: ln, via: via, conn: conn,
	}
	s.srv = &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
	// El timer existe antes de que la sesión sea visible: Close lo usa.
	s.timer = time.AfterFunc(connectWindow, func() {
		if !s.started() {
			p.Close(s.id)
		}
	})
	p.mu.Lock()
	if p.shut { // CloseAll corrió durante la conexión
		p.mu.Unlock()
		s.timer.Stop()
		ln.Close()
		conn.Close()
		return Ticket{}, i18n.Err("vnc.osatClosing")
	}
	p.sessions[s.id] = s
	p.mu.Unlock()
	go s.srv.Serve(ln)
	slog.Info("vnc: sesión preparada", "id", s.id, "addr", addr)
	return Ticket{ID: s.id, URL: "ws://" + ln.Addr().String() + "/" + s.token}, nil
}

func (p *Proxy) Close(id string) {
	p.mu.Lock()
	s := p.sessions[id]
	delete(p.sessions, id)
	p.mu.Unlock()
	if s != nil {
		s.close()
	}
}

func (p *Proxy) CloseAll() {
	p.mu.Lock()
	all := p.sessions
	p.sessions = map[string]*session{}
	p.shut = true
	p.mu.Unlock()
	for _, s := range all {
		s.close()
	}
}

// dialVia es Dial con tiempo límite: abrir el canal depende de que el
// bastión llegue al servidor.
func dialVia(via Via, addr string) (net.Conn, error) {
	type result struct {
		conn net.Conn
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		c, err := via.Dial("tcp", addr)
		ch <- result{c, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, i18n.Err("vnc.jumpFailed", addr, r.err)
		}
		return r.conn, nil
	case <-time.After(dialTimeout):
		go func() {
			if r := <-ch; r.conn != nil {
				r.conn.Close()
			}
		}()
		return nil, i18n.Err("vnc.jumpTimeout")
	}
}

// friendlyDial traduce los errores de conexión más comunes a un texto claro.
// 10061 es WSAECONNREFUSED, que en Windows no coincide con syscall.ECONNREFUSED.
func friendlyDial(err error) error {
	var dns *net.DNSError
	var ne net.Error
	var errno syscall.Errno
	switch {
	case errors.As(err, &dns):
		return i18n.Err("vnc.notFound")
	case errors.As(err, &errno) && (errno == syscall.ECONNREFUSED || errno == 10061):
		return i18n.Err("vnc.refused")
	case errors.As(err, &ne) && ne.Timeout():
		return i18n.Err("vnc.timeout")
	}
	return err
}

type session struct {
	p               *Proxy
	id, addr, token string

	ln    net.Listener
	srv   *http.Server
	timer *time.Timer
	via   Via // salto por el que se llega al servidor (nil = directo)
	conn  net.Conn

	mu     sync.Mutex
	used   bool
	closed bool
	ws     *websocket.Conn
}

func (s *session) started() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.used
}

func (s *session) close() {
	slog.Info("vnc: sesión cerrada", "id", s.id, "addr", s.addr)
	s.mu.Lock()
	s.closed = true
	ws := s.ws
	s.mu.Unlock()
	s.timer.Stop()
	s.srv.Close() // cierra el listener; la conexión secuestrada por el WebSocket sigue
	if ws != nil {
		ws.Close()
	}
	s.conn.Close()
	if s.via != nil {
		s.via.Close()
	}
}

var upgrader = websocket.Upgrader{
	// noVNC pide el subprotocolo "binary" si el servidor lo ofrece.
	Subprotocols: []string{"binary"},
	// Lo que autoriza es la ruta secreta de un solo uso, no el Origin (que
	// cambia según el WebView: http://wails.localhost, wails://wails...).
	CheckOrigin: func(*http.Request) bool { return true },
}

func (s *session) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Host exacto: una página que llegue por DNS rebinding manda otro Host.
	if r.Host != s.ln.Addr().String() ||
		subtle.ConstantTimeCompare([]byte(r.URL.Path), []byte("/"+s.token)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.mu.Lock()
	if s.used || s.closed {
		s.mu.Unlock()
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.used = true
	s.mu.Unlock()
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		// No era un WebSocket: la sesión sigue libre (y el timer la descarta si nadie llega).
		s.mu.Lock()
		s.used = false
		s.mu.Unlock()
		return
	}
	s.ln.Close() // un solo cliente por sesión
	s.mu.Lock()
	s.ws = ws
	closed := s.closed
	s.mu.Unlock()
	if closed {
		ws.Close()
		return
	}
	s.relay(ws)
}

// relay reenvía en los dos sentidos hasta que se corta uno, y entonces cierra la sesión.
func (s *session) relay(ws *websocket.Conn) {
	done := make(chan struct{}, 2)
	go func() { // WebSocket → servidor
		for {
			_, r, err := ws.NextReader()
			if err != nil {
				break
			}
			if _, err := io.Copy(s.conn, r); err != nil {
				break
			}
		}
		done <- struct{}{}
	}()
	go func() { // servidor → WebSocket
		buf := make([]byte, 64<<10)
		for {
			n, err := s.conn.Read(buf)
			if n > 0 && ws.WriteMessage(websocket.BinaryMessage, buf[:n]) != nil {
				break
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}()
	<-done
	s.p.Close(s.id) // cuando un lado se corta, se cierran los dos
}

// Package rdpproxy hace de puente entre el cliente RDP del frontend (IronRDP,
// compilado a WASM) y el servidor: el WebView no puede abrir TCP ni TLS, así
// que habla RDCleanPath por un WebSocket local y este paquete abre la conexión
// real. Así el escritorio remoto no depende de ningún cliente RDP del sistema.
//
// Cada sesión tiene su propio puerto en 127.0.0.1 y una ruta secreta de un
// solo uso; el destino lo fija osat (no el pedido), así que el proxy no sirve
// de relé hacia otros hosts.
package rdpproxy

import (
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"osat/internal/i18n"
	"osat/internal/remote"
)

// Si el frontend no se conecta en este tiempo, la sesión se descarta.
const connectWindow = 30 * time.Second

// Ticket es lo que el frontend necesita para que IronRDP se conecte.
type Ticket struct {
	ID          string `json:"id"`
	ProxyURL    string `json:"proxyUrl"`
	Token       string `json:"token"`
	Destination string `json:"destination"`
}

type Proxy struct {
	Certs *KnownCerts

	mu       sync.Mutex
	sessions map[string]*session
	shut     bool // CloseAll ya corrió (la app se está cerrando)
}

func New(knownCertsPath string) (*Proxy, error) {
	kc, err := NewKnownCerts(knownCertsPath)
	if err != nil {
		return nil, err
	}
	return &Proxy{Certs: kc, sessions: map[string]*session{}}, nil
}

// Open sondea el servidor (que hable RDP con TLS y que su certificado sea de
// confianza) y prepara la sesión. Los errores de certificado vuelven como
// *remote.Error para que el frontend pregunte y reintente.
func (p *Proxy) Open(host string, port int) (Ticket, error) { return p.OpenVia(host, port, nil) }

// OpenVia es Open llegando al servidor a través de un salto (via, opcional).
// La sesión se queda con via: lo cierra al terminar, o enseguida si hay error.
func (p *Proxy) OpenVia(host string, port int, via Via) (Ticket, error) {
	t, err := p.open(host, port, via)
	if err != nil && via != nil {
		via.Close()
	}
	return t, err
}

func (p *Proxy) open(host string, port int, via Via) (Ticket, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	var fp string
	conn, cc, err := dialServer(addr, host, probeCR, func(cs tls.ConnectionState) error {
		if err := p.Certs.check(addr, host, cs.PeerCertificates); err != nil {
			return err
		}
		fp = Fingerprint(cs.PeerCertificates[0].Raw)
		return nil
	}, via)
	if err != nil {
		return Ticket{}, unwrapCertError(err)
	}
	// Por un OneShot no se puede volver a conectar: la sesión usa esta conexión.
	var probe *tls.Conn
	if _, ok := via.(OneShot); ok {
		probe = conn
	} else {
		conn.Close()
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		if probe != nil {
			probe.Close()
		}
		return Ticket{}, err
	}
	tok := make([]byte, 32)
	if _, err := rand.Read(tok); err != nil {
		ln.Close()
		if probe != nil {
			probe.Close()
		}
		return Ticket{}, err
	}
	s := &session{
		id: uuid.NewString(), addr: addr, host: host, fingerprint: fp,
		token: hex.EncodeToString(tok), ln: ln, via: via, probe: probe, probeCC: cc,
	}
	s.srv = &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
	// El timer existe antes de que la sesión sea visible: Close lo usa.
	s.timer = time.AfterFunc(connectWindow, func() {
		if !s.started() {
			p.Close(s.id)
		}
	})
	p.mu.Lock()
	if p.shut { // CloseAll corrió durante el sondeo
		p.mu.Unlock()
		s.timer.Stop()
		ln.Close()
		if probe != nil {
			probe.Close()
		}
		return Ticket{}, i18n.Err("app.osatIsClosing")
	}
	p.sessions[s.id] = s
	p.mu.Unlock()
	go s.srv.Serve(ln)
	slog.Info("escritorio remoto: sesión preparada", "id", s.id, "addr", addr)
	return Ticket{
		ID:          s.id,
		ProxyURL:    "ws://" + ln.Addr().String() + "/" + s.token,
		Token:       s.token,
		Destination: addr,
	}, nil
}

// unwrapCertError devuelve el *remote.Error de check tal cual: el handshake
// lo envuelve y el frontend necesita el "OSAT:{json}" entero.
func unwrapCertError(err error) error {
	var re *remote.Error
	if errors.As(err, &re) {
		return re
	}
	return err
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

type session struct {
	id, addr, host, fingerprint, token string

	ln    net.Listener
	srv   *http.Server
	timer *time.Timer
	via   Via // salto por el que se llega al servidor (nil = directo)

	mu      sync.Mutex
	probe   *tls.Conn // con un OneShot: la conexión del sondeo, para la sesión
	probeCC []byte    // y su Connection Confirm
	used    bool
	closed  bool
	ws      *websocket.Conn
	server  *tls.Conn
}

func (s *session) started() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.used
}

func (s *session) close() {
	slog.Info("escritorio remoto: sesión cerrada", "id", s.id, "addr", s.addr)
	s.mu.Lock()
	s.closed = true
	ws, server, probe := s.ws, s.server, s.probe
	s.probe = nil
	s.mu.Unlock()
	if probe != nil { // nadie llegó a usarla
		probe.Close()
	}
	s.timer.Stop()
	s.srv.Close() // cierra el listener; las conexiones secuestradas por el WebSocket siguen
	if ws != nil {
		ws.Close()
	}
	if server != nil {
		server.Close()
	}
	if s.via != nil {
		s.via.Close()
	}
}

var upgrader = websocket.Upgrader{
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
	s.run(ws)
}

// run atiende el pedido RDCleanPath y después reenvía en los dos sentidos.
func (s *session) run(ws *websocket.Conn) {
	defer ws.Close()
	req, err := readRequest(ws)
	if err != nil {
		s.sendError(ws, cleanPathErr{code: errGeneral, http: http.StatusBadRequest, tls: -1}, nil)
		return
	}
	if subtle.ConstantTimeCompare([]byte(req.ProxyAuth), []byte(s.token)) != 1 {
		s.sendError(ws, cleanPathErr{code: errGeneral, http: http.StatusUnauthorized, tls: -1}, nil)
		return
	}
	if req.PreconnectionBlob != "" {
		s.sendError(ws, cleanPathErr{code: errGeneral, http: http.StatusBadRequest, tls: -1}, nil)
		return
	}
	s.mu.Lock()
	probe, probeCC := s.probe, s.probeCC
	s.probe = nil
	s.mu.Unlock()
	if probe != nil {
		// OneShot: no se vuelve a conectar, se sigue por la conexión del
		// sondeo, si lo que eligió el servidor ahí es algo que IronRDP pidió.
		requested, selected := negProtocols(req.X224ConnectionPDU, 0x01), negProtocols(probeCC, 0x02)
		if selected == 0 || requested&selected != selected {
			probe.Close()
			slog.Warn("escritorio remoto: IronRDP pidió otros protocolos que el sondeo",
				"addr", s.addr, "pedidos", fmt.Sprintf("0x%x", requested), "sondeo", fmt.Sprintf("0x%x", selected))
			s.sendError(ws, cleanPathErr{code: errGeneral, http: http.StatusBadGateway, tls: -1}, nil)
			return
		}
		slog.Info("escritorio remoto: sigue por la conexión del sondeo", "id", s.id, "addr", s.addr,
			"pedidos", fmt.Sprintf("0x%x", requested), "sondeo", fmt.Sprintf("0x%x", selected))
		s.relay(ws, probe, probeCC)
		return
	}
	// El destino del pedido se ignora: va siempre al de la conexión guardada,
	// con el certificado que se aprobó en el sondeo.
	server, cc, err := dialServer(s.addr, s.host, req.X224ConnectionPDU, func(cs tls.ConnectionState) error {
		if Fingerprint(cs.PeerCertificates[0].Raw) != s.fingerprint {
			return i18n.Err("app.theServerCertificateChangedSince")
		}
		return nil
	}, s.via)
	if err != nil {
		var ne *negError
		var de *netError
		switch {
		case errors.As(err, &ne):
			s.sendError(ws, cleanPathErr{code: errNegotiation, tls: -1}, ne.cc)
		case errors.As(err, &de) && de.wsa != 0:
			s.sendError(ws, cleanPathErr{code: errGeneral, wsa: de.wsa, tls: -1}, nil)
		default:
			s.sendError(ws, cleanPathErr{code: errGeneral, http: http.StatusBadGateway, tls: -1}, nil)
		}
		slog.Warn("escritorio remoto: no se pudo conectar con el servidor", "addr", s.addr, "err", err)
		return
	}
	s.relay(ws, server, cc)
}

// relay manda la respuesta RDCleanPath y después reenvía en los dos sentidos.
func (s *session) relay(ws *websocket.Conn, server *tls.Conn, cc []byte) {
	defer server.Close()
	s.mu.Lock()
	s.server = server
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return
	}

	var chain [][]byte
	for _, c := range server.ConnectionState().PeerCertificates {
		chain = append(chain, c.Raw)
	}
	serverAddr := server.RemoteAddr().String()
	if s.via != nil { // un túnel no tiene dirección propia (0.0.0.0:0)
		serverAddr = s.addr
	}
	if err := ws.WriteMessage(websocket.BinaryMessage, responsePDU(cc, chain, serverAddr)); err != nil {
		return
	}
	ws.SetReadLimit(0) // desde acá viajan imágenes del portapapeles, que pueden pesar MB

	type end struct {
		side string // quién cortó primero
		err  error
	}
	var toServer, toClient atomic.Int64
	done := make(chan end, 2)
	go func() { // WebSocket → servidor
		var err error
		for {
			var r io.Reader
			if _, r, err = ws.NextReader(); err != nil {
				break
			}
			n, cerr := io.Copy(server, r)
			toServer.Add(n)
			if err = cerr; err != nil {
				break
			}
		}
		done <- end{"cliente", err}
	}()
	go func() { // servidor → WebSocket
		buf := make([]byte, 64<<10)
		var err error
		for {
			var n int
			n, err = server.Read(buf)
			if n > 0 {
				toClient.Add(int64(n))
				if werr := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					err = werr
					break
				}
			}
			if err != nil {
				break
			}
		}
		done <- end{"servidor", err}
	}()
	e := <-done // cuando un lado se corta, el defer cierra los dos
	// Para diagnosticar cortes al conectar: quién cortó y cuánto había pasado.
	slog.Info("escritorio remoto: reenvío terminado", "id", s.id, "addr", s.addr, "tls", tls.VersionName(server.ConnectionState().Version), "cortó", e.side,
		"err", e.err, "alServidor", toServer.Load(), "alCliente", toClient.Load())
}

// readRequest junta mensajes hasta tener la PDU entera (el cliente puede
// partirla) y la decodifica.
func readRequest(ws *websocket.Conn) (*request, error) {
	ws.SetReadLimit(maxRequestLen)
	_ = ws.SetReadDeadline(time.Now().Add(dialTimeout))
	defer ws.SetReadDeadline(time.Time{})
	var buf []byte
	for {
		_, m, err := ws.ReadMessage()
		if err != nil {
			return nil, err
		}
		buf = append(buf, m...)
		n, err := pduLen(buf)
		if err != nil {
			return nil, err
		}
		if n > maxRequestLen {
			return nil, fmt.Errorf("RDCleanPath demasiado grande (%d bytes)", n)
		}
		if n > 0 && len(buf) >= n {
			if len(buf) > n {
				return nil, errors.New("RDCleanPath inválido: sobran bytes")
			}
			return parseRequest(buf)
		}
	}
}

func (s *session) sendError(ws *websocket.Conn, e cleanPathErr, x224 []byte) {
	_ = ws.WriteMessage(websocket.BinaryMessage, errorPDU(e, x224))
}

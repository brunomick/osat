package rdgateway

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/gorilla/websocket"
)

// fakeGateway es un RD Gateway mínimo para los tests: NTLM (solo mira el
// usuario del AUTHENTICATE), upgrade a WebSocket y MS-TSGU con eco de DATA.
type fakeGateway struct {
	user        string        // usuario aceptado
	schemes     []string      // esquemas del 401 (por defecto NTLM)
	authDelay   time.Duration // demora antes de TUNNEL_AUTH_RESPONSE
	authCode    uint32
	channelCode uint32
	fragment    bool          // parte los paquetes en mensajes de 3 bytes y junta otros
	closeAfter  bool          // manda CLOSE_CHANNEL apenas crea el canal
	ping        bool          // manda un ping de WebSocket antes de contestar el handshake
	createDelay time.Duration // demora antes de TUNNEL_RESPONSE
	dropAtAuth  bool          // corta la conexión en vez de contestar TUNNEL_AUTH (push rechazado)
	dropAtNTLM  bool          // corta la conexión al recibir el AUTHENTICATE (red inestable)
	stall       chan struct{} // después de abrir el canal deja de leer hasta que se cierre

	srv       *httptest.Server
	tunnels   atomic.Int32
	ponged    atomic.Bool
	negotiate []byte        // el último NEGOTIATE (para verificar el MIC)
	authErr   error         // por qué rechazó el último AUTHENTICATE
	closed    chan struct{} // se cierra cuando un túnel termina
	mu        sync.Mutex
	chHost    string
	chPort    int
}

func newFakeGateway(t *testing.T, g *fakeGateway) (*fakeGateway, Config) {
	t.Helper()
	if g.user == "" {
		g.user = "Nombre.Apellido"
	}
	if g.schemes == nil {
		g.schemes = []string{"NTLM"}
	}
	g.closed = make(chan struct{})
	g.srv = httptest.NewTLSServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.srv.Close)
	host, port, _ := net.SplitHostPort(g.srv.Listener.Addr().String())
	p, _ := strconv.Atoi(port)
	return g, Config{
		Host: host, Port: p, Domain: "EMPRESA", User: "Nombre.Apellido", Password: "pw", ClientName: "PC",
		VerifyConnection: func(tls.ConnectionState) error { return nil },
	}
}

func (g *fakeGateway) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/remoteDesktopGateway/" {
		http.NotFound(w, r)
		return
	}
	// Como el gateway real: si el pedido no le gusta, corta sin responder.
	if r.Method != "RDG_OUT_DATA" || r.Header.Get("RDG-Connection-Id") == "" || r.Header.Get("RDG-Correlation-Id") == "" {
		if c, _, err := w.(http.Hijacker).Hijack(); err == nil {
			c.Close()
		}
		return
	}
	auth := r.Header.Get("Authorization")
	msg, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(auth, "NTLM "))
	switch {
	case !strings.HasPrefix(auth, "NTLM ") || len(msg) < 12 || msg[8] == 1:
		g.mu.Lock()
		g.negotiate = msg
		g.mu.Unlock()
		for _, s := range g.schemes {
			if s == "NTLM" {
				w.Header().Add("WWW-Authenticate", "NTLM "+base64.StdEncoding.EncodeToString(fakeChallenge()))
			} else {
				w.Header().Add("WWW-Authenticate", s)
			}
		}
		w.WriteHeader(http.StatusUnauthorized)
		return
	case g.dropAtNTLM:
		if c, _, err := w.(http.Hijacker).Hijack(); err == nil {
			c.Close()
		}
		return
	case ntlmUser(msg) != g.user:
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	g.mu.Lock()
	neg := g.negotiate
	g.mu.Unlock()
	if err := verifyNTLM(neg, fakeChallenge(), msg, "pw", g.srv.Certificate().Raw); err != nil {
		g.mu.Lock()
		g.authErr = err
		g.mu.Unlock()
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	r.Method = http.MethodGet // el Upgrader de gorilla solo acepta GET
	ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
	if err != nil {
		return
	}
	ws.SetPongHandler(func(string) error { g.ponged.Store(true); return nil })
	g.tunnels.Add(1)
	defer func() {
		ws.Close()
		close(g.closed)
	}()
	g.run(ws)
}

// fakeChallenge: un CHALLENGE NTLM mínimo (UNICODE | NTLM | ESS | TARGET_INFO, sin TargetInfo).
// Como el del gateway real: TargetInfo con dominio y marca de tiempo (que
// hace que el cliente agregue el MIC).
func fakeChallenge() []byte {
	ti := append(avPair(avNbDomainName, utf16le("EMPRESA")), avPair(avTimestamp, []byte{1, 2, 3, 4, 5, 6, 7, 8})...)
	ti = append(ti, avPair(avEOL, nil)...)
	b := make([]byte, 48, 48+len(ti))
	copy(b, "NTLMSSP\x00")
	binary.LittleEndian.PutUint32(b[8:], 2)
	binary.LittleEndian.PutUint32(b[16:], 48) // offset de TargetName (vacío)
	binary.LittleEndian.PutUint32(b[20:], 0xa2898205)
	copy(b[24:], "12345678")
	binary.LittleEndian.PutUint16(b[40:], uint16(len(ti)))
	binary.LittleEndian.PutUint16(b[42:], uint16(len(ti)))
	binary.LittleEndian.PutUint32(b[44:], 48)
	return append(b, ti...)
}

// verifyNTLM comprueba un AUTHENTICATE como un servidor: la respuesta NTLMv2
// con la contraseña, el channel binding del certificado y el MIC.
func verifyNTLM(neg, chal, auth []byte, password string, cert []byte) error {
	field := func(at int) []byte {
		n, off := int(binary.LittleEndian.Uint16(auth[at:])), int(binary.LittleEndian.Uint32(auth[at+4:]))
		return auth[off : off+n]
	}
	fromUTF16 := func(b []byte) string {
		u := make([]uint16, len(b)/2)
		for i := range u {
			u[i] = binary.LittleEndian.Uint16(b[2*i:])
		}
		return string(utf16.Decode(u))
	}
	nt := field(20)
	if len(nt) < 16+28 {
		return errors.New("respuesta NT corta")
	}
	key := ntowfv2(fromUTF16(field(36)), fromUTF16(field(28)), password)
	proof := hmacMD5(key, chal[24:32], nt[16:])
	if !bytes.Equal(proof, nt[:16]) {
		return errors.New("NTProofStr no coincide")
	}
	ti := map[uint16][]byte{}
	for rest := nt[16+28:]; len(rest) >= 4; {
		id, l := binary.LittleEndian.Uint16(rest), int(binary.LittleEndian.Uint16(rest[2:]))
		if id == avEOL {
			break
		}
		ti[id] = rest[4 : 4+l]
		rest = rest[4+l:]
	}
	if !bytes.Equal(ti[avChannelBindings], channelBindings(cert)) {
		return errors.New("channel binding no coincide")
	}
	mic := append([]byte(nil), auth[72:88]...)
	zeroed := append([]byte(nil), auth...)
	copy(zeroed[72:88], make([]byte, 16))
	sessionKey := hmacMD5(key, proof)
	if !bytes.Equal(mic, hmacMD5(sessionKey, neg, chal, zeroed)) {
		return errors.New("MIC no coincide")
	}
	return nil
}

// ntlmUser: el usuario de un AUTHENTICATE (UTF-16LE, campo en el byte 36).
func ntlmUser(m []byte) string {
	if len(m) < 44 || m[8] != 3 {
		return ""
	}
	n, off := int(binary.LittleEndian.Uint16(m[36:])), int(binary.LittleEndian.Uint32(m[40:]))
	if off+n > len(m) {
		return ""
	}
	u := make([]uint16, n/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(m[off+2*i:])
	}
	return string(utf16.Decode(u))
}

func (g *fakeGateway) send(ws *websocket.Conn, pkts ...[]byte) error {
	var all []byte
	for _, p := range pkts {
		all = append(all, p...)
	}
	if !g.fragment || len(pkts) > 1 {
		return ws.WriteMessage(websocket.BinaryMessage, all)
	}
	for len(all) > 0 {
		n := min(3, len(all))
		if err := ws.WriteMessage(websocket.BinaryMessage, all[:n]); err != nil {
			return err
		}
		all = all[n:]
	}
	return nil
}

func codeBody(code uint32, extra int) []byte {
	return append(binary.LittleEndian.AppendUint32(nil, code), make([]byte, extra)...)
}

func (g *fakeGateway) run(ws *websocket.Conn) {
	in := &gorillaStream{ws: ws}
	for {
		p, err := readPacket(in)
		if err != nil {
			return
		}
		switch p.typ {
		case pktHandshakeRequest:
			if g.ping {
				ws.WriteControl(websocket.PingMessage, []byte("hola"), time.Now().Add(time.Second))
			}
			g.send(ws, encode(pktHandshakeResponse, []byte{0, 0, 0, 0, 1, 0, 0, 0, 0, 0}))
		case pktTunnelCreate:
			time.Sleep(g.createDelay)
			g.send(ws, encode(pktTunnelResponse, append([]byte{1, 0}, codeBody(0, 4)...)))
		case pktTunnelAuth:
			if g.dropAtAuth {
				return
			}
			time.Sleep(g.authDelay)
			g.send(ws, encode(pktTunnelAuthResponse, codeBody(g.authCode, 4)))
		case pktChannelCreate:
			port := int(binary.LittleEndian.Uint16(p.body[2:]))
			n := int(binary.LittleEndian.Uint16(p.body[6:]))
			u := make([]uint16, n/2-1)
			for i := range u {
				u[i] = binary.LittleEndian.Uint16(p.body[8+2*i:])
			}
			g.mu.Lock()
			g.chHost, g.chPort = string(utf16.Decode(u)), port
			g.mu.Unlock()
			g.send(ws, encode(pktChannelResponse, codeBody(g.channelCode, 4)))
			if g.closeAfter {
				g.send(ws, encodeCloseChannel(0))
			}
			if g.stall != nil {
				<-g.stall
				return
			}
		case pktData:
			d, _ := dataPayload(p)
			if g.fragment {
				g.send(ws, encode(pktKeepalive, nil), encodeData(d)) // dos paquetes en un mensaje
			} else {
				g.send(ws, encodeData(d))
			}
		case pktCloseChannel:
			g.send(ws, encodeCloseChannelResponse(0))
			return
		}
	}
}

// gorillaStream lee el WebSocket del falso como un flujo de bytes.
type gorillaStream struct {
	ws  *websocket.Conn
	cur io.Reader
}

func (s *gorillaStream) Read(p []byte) (int, error) {
	for {
		if s.cur == nil {
			_, r, err := s.ws.NextReader()
			if err != nil {
				return 0, err
			}
			s.cur = r
		}
		n, err := s.cur.Read(p)
		if err == io.EOF {
			s.cur = nil
			if n == 0 {
				continue
			}
			err = nil
		}
		return n, err
	}
}

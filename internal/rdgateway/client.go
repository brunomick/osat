// Package rdgateway es un cliente de RD Gateway (MS-TSGU) por WebSocket con
// autenticación NTLM: abre un túnel hasta el gateway y, por él, un canal TCP
// hasta el equipo. *Tunnel cumple rdpproxy.Via, así que el escritorio remoto
// llega al equipo a través del gateway igual que a través de un salto SSH.
package rdgateway

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
)

const gatewayPath = "/remoteDesktopGateway/"

type Config struct {
	Host     string // gateway
	Port     int
	Domain   string
	User     string
	Password string
	// ClientName: nombre de esta PC; va en TUNNEL_AUTH.
	ClientName string
	// VerifyConnection decide si el certificado del gateway sirve (el TLS en
	// sí no verifica: InsecureSkipVerify + este callback, como rdpproxy).
	VerifyConnection func(tls.ConnectionState) error
}

// Tunnel es un túnel autorizado por el gateway. Abre un solo canal.
type Tunnel struct {
	gw   string // host:puerto, para los errores
	ws   *wsConn
	used atomic.Bool
	once sync.Once
}

// Connect abre el túnel: TLS, NTLM, upgrade a WebSocket y el handshake de
// MS-TSGU hasta la autorización del túnel, que es el paso que espera el
// doble factor. ctx limita todo (si vence esperando la autorización, el
// error es KindTimeout).
func Connect(ctx context.Context, cfg Config) (*Tunnel, error) {
	gw := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	failErr := func(e *Error) (*Tunnel, error) {
		e.Gateway = gw
		slog.Warn("rd gateway: falló", e.LogAttrs()...)
		return nil, e
	}
	start := time.Now()
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", gw)
	if err != nil {
		return failErr(dialError(err))
	}
	step := StepTLS // el paso en curso, para los errores
	fail := func(k Kind, code uint32, err error) (*Tunnel, error) {
		return failErr(&Error{Kind: k, Step: step, Code: code, Err: err})
	}
	// Si ctx vence en cualquier paso, se corta la conexión y el paso falla.
	stop := context.AfterFunc(ctx, func() { raw.Close() })
	ok := false
	defer func() {
		if !ok {
			stop()
			raw.Close()
		}
	}()

	var verifyErr error
	conn := tls.Client(raw, &tls.Config{
		InsecureSkipVerify: true, // lo verifica VerifyConnection
		VerifyConnection: func(cs tls.ConnectionState) error {
			if cfg.VerifyConnection != nil {
				verifyErr = cfg.VerifyConnection(cs)
			}
			return verifyErr
		},
		ServerName: cfg.Host,
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"},
	})
	if err := conn.HandshakeContext(ctx); err != nil {
		if verifyErr != nil {
			return nil, verifyErr // el *remote.Error del certificado, tal cual
		}
		return failErr(tlsError(ctx, err))
	}
	slog.Debug("rd gateway: TLS", "gateway", gw, "ms", time.Since(start).Milliseconds(),
		"version", tls.VersionName(conn.ConnectionState().Version))

	br := bufio.NewReader(conn)
	req := newUpgradeRequest(cfg.Host, cfg.Port)
	slog.Debug("rd gateway: pedido", "gateway", gw, "connId", req.connID, "corrId", req.corrID)
	neg := ntlmNegotiate()
	step = StepHTTP
	challenge, ce := req.ntlmChallenge(conn, br, neg)
	if ce != nil {
		ce.Step = step
		switch {
		case ctx.Err() != nil:
			ce.Kind, ce.Reason, ce.Err = KindConnectionLost, "timeout", ctx.Err()
		case ce.Kind == KindConnectionLost: // el TLS había conectado: el gateway cortó sin contestar
			ce.Reason = "noAnswer"
		}
		return failErr(ce)
	}
	step = StepAuth
	slog.Debug("rd gateway: desafío NTLM recibido", "gateway", gw, "ms", time.Since(start).Milliseconds())
	cbt := channelBindings(conn.ConnectionState().PeerCertificates[0].Raw)
	auth, err := ntlmAuthenticate(neg, challenge, cfg.Domain, cfg.User, cfg.Password, cfg.ClientName, "HTTP/"+cfg.Host, cbt)
	if err != nil {
		return fail(KindProtocol, 0, err)
	}
	// El NTLM vale para esta conexión: el upgrade va por la misma.
	resp, key, err := req.do(conn, br, "NTLM "+base64.StdEncoding.EncodeToString(auth))
	switch {
	case err != nil && ctx.Err() != nil:
		return failErr(&Error{Kind: KindConnectionLost, Step: step, Reason: "timeout", Err: ctx.Err()})
	case err != nil:
		return fail(KindConnectionLost, 0, fmt.Errorf("autenticación NTLM: %w", err))
	case resp.StatusCode == http.StatusUnauthorized:
		// NTLM por HTTP no dice por qué: contraseña, cuenta bloqueada o vencida dan lo mismo.
		return failErr(&Error{Kind: KindAuth, Step: step, Status: resp.StatusCode})
	case resp.StatusCode != http.StatusSwitchingProtocols:
		return failErr(&Error{Kind: KindHTTP, Step: step, Status: resp.StatusCode, Err: httpStatusErr(resp)})
	case resp.Header.Get("Sec-Websocket-Accept") != wsAcceptKey(key):
		return fail(KindProtocol, 0, errors.New("respuesta de WebSocket inválida"))
	}
	slog.Debug("rd gateway: WebSocket abierto", "gateway", gw, "ms", time.Since(start).Milliseconds())

	t := &Tunnel{gw: gw, ws: &wsConn{conn: conn, br: br}}
	steps := []struct {
		name     string
		req      []byte
		resp     uint16
		failKind Kind
	}{
		{StepHandshake, encodeHandshakeRequest(), pktHandshakeResponse, KindProtocol},
		{StepTunnel, encodeTunnelCreate(), pktTunnelResponse, KindNotAuthorized},
		{StepTunnelAuth, encodeTunnelAuth(cfg.ClientName), pktTunnelAuthResponse, KindNotAuthorized},
	}
	for _, s := range steps {
		step = s.name
		code, err := t.roundTrip(s.req, s.resp)
		// Crear y autorizar el túnel son los pasos que pueden esperar el
		// doble factor: vencer ahí es "no se aprobó a tiempo", y que el
		// gateway corte (en vez de contestar con un código) es un rechazo.
		approval := s.resp != pktHandshakeResponse
		switch {
		case err != nil && ctx.Err() != nil && approval:
			return fail(KindTimeout, 0, ctx.Err())
		case err != nil && ctx.Err() != nil:
			return failErr(&Error{Kind: KindConnectionLost, Step: step, Reason: "timeout", Err: ctx.Err()})
		case err != nil && approval:
			return failErr(&Error{Kind: KindNotAuthorized, Step: step, Reason: "dropped", Err: err})
		case err != nil:
			return fail(KindProtocol, 0, err)
		case code&0xFFFF == eProxyNAPAccessDenied&0xFFFF:
			return failErr(&Error{Kind: KindNotAuthorized, Step: step, Reason: "cap", Code: code})
		case code != 0:
			return fail(s.failKind, code, nil)
		}
		slog.Debug("rd gateway: paso", "gateway", gw, "paso", s.name, "ms", time.Since(start).Milliseconds())
	}
	if !stop() { // ctx venció justo al terminar: la conexión ya se cerró
		return failErr(&Error{Kind: KindTimeout, Step: StepTunnelAuth, Err: ctx.Err()})
	}
	ok = true
	slog.Info("rd gateway: túnel autorizado", "gateway", gw, "ms", time.Since(start).Milliseconds())
	return t, nil
}

// upgradeRequest arma el pedido HTTP como los clientes de Windows (y
// FreeRDP). El gateway corta la conexión sin responder si no le gusta: tiene
// que ser RDG_OUT_DATA también para el upgrade a WebSocket, con
// Content-Length: 0 y RDG-Connection-Id / RDG-Correlation-Id escritos así
// (Go normalizaría Rdg-Connection-Id). Por eso se escribe a mano.
type upgradeRequest struct {
	host           string // tal como va en Host
	connID, corrID string
}

func newUpgradeRequest(host string, port int) *upgradeRequest {
	if port != 443 {
		host = net.JoinHostPort(host, strconv.Itoa(port))
	}
	guid := func() string { return "{" + strings.ToUpper(uuid.NewString()) + "}" }
	return &upgradeRequest{host: host, connID: guid(), corrID: guid()}
}

func (r *upgradeRequest) bytes(key, auth string) []byte {
	var b strings.Builder
	line := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	b.WriteString("RDG_OUT_DATA " + gatewayPath + " HTTP/1.1\r\n")
	line("Pragma", "no-cache")
	line("Connection", "Upgrade")
	line("Upgrade", "websocket")
	line("Sec-Websocket-Version", "13")
	line("Sec-Websocket-Key", key)
	line("Content-Length", "0")
	line("Authorization", auth)
	line("Accept", "*/*")
	line("Cache-Control", "no-cache")
	line("User-Agent", "MS-RDGateway/1.0")
	line("Host", r.host)
	line("RDG-Connection-Id", r.connID)
	line("RDG-Correlation-Id", r.corrID)
	b.WriteString("\r\n")
	return []byte(b.String())
}

// do manda el pedido con auth y lee la respuesta (sin cuerpo: el gateway
// manda Content-Length: 0, y un 101 no lo tiene). Devuelve también la clave
// del WebSocket, para verificar el 101.
func (r *upgradeRequest) do(conn net.Conn, br *bufio.Reader, auth string) (*http.Response, string, error) {
	k := make([]byte, 16)
	rand.Read(k)
	key := base64.StdEncoding.EncodeToString(k)
	if _, err := conn.Write(r.bytes(key, auth)); err != nil {
		return nil, "", err
	}
	resp, err := http.ReadResponse(br, &http.Request{Method: "RDG_OUT_DATA"})
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	return resp, key, nil
}

// ntlmChallenge manda el NEGOTIATE y devuelve el CHALLENGE del 401. La
// conexión queda lista para el pedido siguiente.
func (r *upgradeRequest) ntlmChallenge(conn net.Conn, br *bufio.Reader, neg []byte) ([]byte, *Error) {
	resp, _, err := r.do(conn, br, "NTLM "+base64.StdEncoding.EncodeToString(neg))
	if err != nil {
		return nil, &Error{Kind: KindConnectionLost, Err: fmt.Errorf("negociación NTLM: %w", err)}
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return nil, &Error{Kind: KindHTTP, Status: resp.StatusCode, Err: httpStatusErr(resp)}
	}
	var schemes []string
	for _, v := range resp.Header.Values("WWW-Authenticate") {
		scheme, data, _ := strings.Cut(v, " ")
		schemes = append(schemes, scheme)
		if strings.EqualFold(scheme, "NTLM") && data != "" {
			if br.Buffered() > 0 || resp.Close {
				return nil, &Error{Kind: KindProtocol, Err: errors.New("el gateway cerró la conexión después del desafío NTLM")}
			}
			b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(data))
			if err != nil {
				return nil, &Error{Kind: KindProtocol, Err: err}
			}
			return b, nil
		}
	}
	slog.Warn("rd gateway: sin NTLM", "esquemas", strings.Join(schemes, ", "))
	return nil, &Error{Kind: KindNoNTLM, Err: fmt.Errorf("esquemas ofrecidos: %s", strings.Join(schemes, ", "))}
}

// httpStatusErr: el estado HTTP y el servidor que lo mandó, para los detalles.
func httpStatusErr(resp *http.Response) error {
	if s := resp.Header.Get("Server"); s != "" {
		return fmt.Errorf("HTTP %s (Server: %s)", resp.Status, s)
	}
	return fmt.Errorf("HTTP %s", resp.Status)
}

// dialError clasifica la falla al abrir el TCP con el gateway: el nombre no
// se resolvió, el puerto rechazó la conexión o no hubo respuesta.
func dialError(err error) *Error {
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return &Error{Kind: KindDNS, Step: StepDNS, Err: err}
	}
	e := &Error{Kind: KindUnreachable, Step: StepTCP, Err: err}
	var ne net.Error
	switch {
	case errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, syscall.Errno(10061)):
		e.Reason = "refused"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		e.Reason = "timeout"
	}
	return e
}

// tlsError clasifica la falla del TLS con el gateway (el TCP ya conectó):
// rechazado con una alerta (versión o cifrados) o cortado sin respuesta (algo
// en el medio, como un proxy que inspecciona HTTPS).
func tlsError(ctx context.Context, err error) *Error {
	e := &Error{Kind: KindTLS, Step: StepTLS, Err: err}
	var oe *net.OpError
	switch {
	case ctx.Err() != nil:
		e.Reason, e.Err = "timeout", ctx.Err()
	case errors.As(err, &oe) && oe.Op == "remote error":
		e.Reason = "rejected"
	case errors.Is(err, io.EOF), errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.ECONNABORTED),
		errors.Is(err, syscall.Errno(10054)), errors.Is(err, syscall.Errno(10053)):
		e.Reason = "closed"
	}
	return e
}

func (t *Tunnel) write(p []byte) error { return t.ws.writeFrame(opBinary, p) }

// next lee el próximo paquete que no sea KEEPALIVE ni SERVICE_MESSAGE.
func (t *Tunnel) next() (packet, error) {
	for {
		p, err := readPacket(t.ws)
		if err != nil {
			return p, err
		}
		switch p.typ {
		case pktKeepalive:
		case pktServiceMessage:
			slog.Debug("rd gateway: mensaje del servicio", "gateway", t.gw)
		default:
			return p, nil
		}
	}
}

// roundTrip manda req y devuelve el código de la respuesta, que tiene que ser de tipo want.
func (t *Tunnel) roundTrip(req []byte, want uint16) (uint32, error) {
	if err := t.write(req); err != nil {
		return 0, err
	}
	p, err := t.next()
	if err != nil {
		return 0, err
	}
	if p.typ != want {
		return 0, fmt.Errorf("se esperaba el paquete 0x%02X y llegó 0x%02X", want, p.typ)
	}
	return responseCode(p)
}

// Dial abre el canal hasta addr ("host:puerto"). Una sola vez por túnel.
func (t *Tunnel) Dial(network, addr string) (net.Conn, error) {
	if t.used.Swap(true) {
		return nil, ErrChannelUsed
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return nil, err
	}
	code, err := t.roundTrip(encodeChannelCreate(host, n), pktChannelResponse)
	e := &Error{Kind: KindTargetUnreachable, Step: StepChannel, Gateway: t.gw, Target: addr, Code: code}
	switch {
	case err != nil:
		// El gateway cortó en vez de contestar si llegó al equipo.
		e.Kind, e.Reason, e.Err = KindConnectionLost, "channel", err
	case code&0xFFFF == eProxyRAPAccessDenied&0xFFFF:
		e.Kind = KindResourceDenied
	case code&0xFFFF == eProxyTSConnectFailed&0xFFFF:
		e.Reason = "connect"
	case code == 0:
		slog.Debug("rd gateway: canal abierto", "gateway", t.gw, "addr", addr)
		return &channel{t: t}, nil
	}
	slog.Warn("rd gateway: falló el canal al equipo", e.LogAttrs()...)
	return nil, e
}

func (t *Tunnel) Close() error {
	t.once.Do(func() { t.ws.Close() })
	return nil
}

// OneShot marca que el túnel abre una sola conexión (rdpproxy.OneShot).
func (t *Tunnel) OneShot() {}

// channel es la conexión al equipo a través del túnel.
type channel struct {
	t       *Tunnel
	pending []byte
	once    sync.Once
}

func (c *channel) Read(p []byte) (int, error) {
	for len(c.pending) == 0 {
		pkt, err := c.t.next()
		if err != nil {
			return 0, err
		}
		switch pkt.typ {
		case pktData:
			if c.pending, err = dataPayload(pkt); err != nil {
				return 0, err
			}
		case pktCloseChannel:
			c.t.write(encodeCloseChannelResponse(0))
			return 0, io.EOF
		case pktCloseChannelResponse:
			return 0, io.EOF
		default:
			return 0, fmt.Errorf("paquete MS-TSGU inesperado 0x%02X", pkt.typ)
		}
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

func (c *channel) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		n := min(len(p), maxDataPerPkt)
		if err := c.t.write(encodeData(p[:n])); err != nil {
			return total, err
		}
		total += n
		p = p[n:]
	}
	return total, nil
}

func (c *channel) Close() error {
	c.once.Do(func() {
		c.t.ws.closing()
		c.t.write(encodeCloseChannel(0))
		c.t.Close()
	})
	return nil
}

var errNoDeadline = errors.New("rdgateway: el canal no acepta deadlines")

func (c *channel) SetDeadline(time.Time) error      { return errNoDeadline }
func (c *channel) SetReadDeadline(time.Time) error  { return errNoDeadline }
func (c *channel) SetWriteDeadline(time.Time) error { return errNoDeadline }
func (c *channel) LocalAddr() net.Addr              { return tunnelAddr{} }
func (c *channel) RemoteAddr() net.Addr             { return tunnelAddr{} }

type tunnelAddr struct{}

func (tunnelAddr) Network() string { return "rdgateway" }
func (tunnelAddr) String() string  { return "" }

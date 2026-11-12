package rdpproxy

import (
	"crypto/tls"
	"errors"
	"io"
	"net"
	"syscall"
	"time"

	"osat/internal/i18n"
)

// Variable (no const) para poder acortarlo en los tests.
var dialTimeout = 15 * time.Second

// Via es el salto (ProxyJump) por el que se llega al servidor: Dial abre la
// conexión TCP desde el bastión (un *ssh.Client cumple la interfaz) y Close
// lo cierra cuando termina la sesión.
type Via interface {
	Dial(network, addr string) (net.Conn, error)
	Close() error
}

// ErrViaTimeout: el salto no contestó a tiempo si llegó al servidor.
var ErrViaTimeout = i18n.Err("app.theServerIsnTResponding")

// ServerError: falló un paso con el servidor RDP después de abrir el TCP
// (Step: "x224", "negotiation" o "tls"), para poder decir cuál.
type ServerError struct {
	Step string
	Err  error
}

func (e *ServerError) Error() string { return e.Err.Error() }
func (e *ServerError) Unwrap() error { return e.Err }

// dialVia es Dial con tiempo límite: abrir el canal depende de que el
// bastión llegue al servidor. Por un RD Gateway (OneShot) se espera más:
// tarda lo que tarde su propio TCP al equipo en fallar, y así llega su código.
func dialVia(via Via, addr string) (net.Conn, error) {
	wait := dialTimeout
	_, gateway := via.(OneShot)
	if gateway {
		wait = 3 * dialTimeout
	}
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
		if r.err != nil && gateway {
			return nil, r.err // el error del gateway ya dice qué pasó
		}
		if r.err != nil {
			return nil, i18n.Err("app.theJumpHostCouldnT", addr, r.err)
		}
		return r.conn, nil
	case <-time.After(wait):
		go func() {
			if r := <-ch; r.conn != nil {
				r.conn.Close()
			}
		}()
		return nil, ErrViaTimeout
	}
}

// probeCR es un X.224 Connection Request (MS-RDPBCGR 2.2.1.1) que pide
// TLS|CredSSP|CredSSP-EX, para el sondeo previo a conectar.
var probeCR = []byte{
	0x03, 0x00, 0x00, 0x13, // TPKT, 19 bytes
	0x0e, 0xe0, 0x00, 0x00, 0x00, 0x00, 0x00, // X.224 CR
	0x01, 0x00, 0x08, 0x00, 0x0b, 0x00, 0x00, 0x00, // RDP_NEG_REQ
}

// negError: el servidor contestó el X.224 pero no se puede seguir por TLS.
// cc es su respuesta, que el cliente sabe interpretar.
type negError struct {
	cc  []byte
	msg string
}

func (e *negError) Error() string { return e.msg }

// netError: no se pudo llegar al servidor (wsa: código equivalente de Winsock,
// que es lo que entiende el cliente web; 0 si no hay uno).
type netError struct {
	wsa int
	err error
}

func (e *netError) Error() string { return e.err.Error() }
func (e *netError) Unwrap() error { return e.err }

// dialServer abre TCP, manda cr, lee el Connection Confirm y, si el servidor
// eligió TLS o CredSSP, hace el handshake TLS. verify decide si el certificado
// sirve: el TLS en sí no verifica nada porque los servidores RDP suelen usar
// certificados autofirmados. via (opcional) es el salto por el que se llega.
func dialServer(addr, serverName string, cr []byte, verify func(tls.ConnectionState) error, via Via) (*tls.Conn, []byte, error) {
	var conn net.Conn
	var err error
	if via != nil {
		if conn, err = dialVia(via, addr); err != nil {
			return nil, nil, &netError{err: err}
		}
	} else if conn, err = net.DialTimeout("tcp", addr, dialTimeout); err != nil {
		return nil, nil, &netError{wsa: wsaCode(err), err: friendlyDial(err)}
	}
	if conn.SetDeadline(time.Now().Add(dialTimeout)) != nil {
		// Un túnel por un salto no acepta deadlines: se corta al vencer el tiempo.
		timer := time.AfterFunc(dialTimeout, func() { conn.Close() })
		defer timer.Stop()
	}
	fail := func(err error) (*tls.Conn, []byte, error) {
		conn.Close()
		return nil, nil, err
	}
	if _, err := conn.Write(cr); err != nil {
		return fail(&netError{wsa: wsaCode(err), err: err})
	}
	cc, err := readTPKT(conn)
	if err != nil {
		return fail(&ServerError{"x224", &netError{wsa: wsaCode(err), err: i18n.Err("app.theServerDidnTAnswer", err)}})
	}
	if err := checkNegotiation(cc); err != nil {
		return fail(&ServerError{"negotiation", err})
	}
	t := tls.Client(conn, &tls.Config{
		InsecureSkipVerify: true, // lo verifica VerifyConnection
		VerifyConnection:   verify,
		ServerName:         serverName,
		MinVersion:         tls.VersionTLS12,
		// Con TLS 1.3 algunos Windows cortan la sesión RDP con "internal
		// error" antes de la autenticación; los clientes RDP usan 1.2.
		MaxVersion: tls.VersionTLS12,
	})
	if err := t.Handshake(); err != nil {
		return fail(&ServerError{"tls", i18n.Err("app.couldnTEstablishTlsWith", err)})
	}
	_ = conn.SetDeadline(time.Time{})
	return t, cc, nil
}

// readTPKT lee un paquete TPKT (RFC 1006) completo.
func readTPKT(r io.Reader) ([]byte, error) {
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return nil, err
	}
	n := int(hdr[2])<<8 | int(hdr[3])
	if hdr[0] != 3 || n < 11 || n > 512 {
		return nil, i18n.Err("app.invalidXResponse")
	}
	b := make([]byte, n)
	copy(b, hdr)
	if _, err := io.ReadFull(r, b[4:]); err != nil {
		return nil, err
	}
	return b, nil
}

// checkNegotiation mira el RDP_NEG_RSP / RDP_NEG_FAILURE del Connection Confirm.
func checkNegotiation(cc []byte) error {
	if len(cc) < 19 || cc[5]&0xf0 != 0xd0 {
		// Sin datos de negociación: el servidor solo habla la seguridad RDP
		// clásica, sin TLS.
		return &negError{cc, i18n.T("rdp.classicOnly")}
	}
	code := uint32(cc[15]) | uint32(cc[16])<<8 | uint32(cc[17])<<16 | uint32(cc[18])<<24
	switch cc[11] {
	case 0x02: // RDP_NEG_RSP
		if code == 0 {
			return &negError{cc, i18n.T("rdp.classicOnly")}
		}
		return nil
	case 0x03: // RDP_NEG_FAILURE
		msg := i18n.T("rdp.negRejected", code)
		switch code { // MS-RDPBCGR 2.2.1.2.2
		case 2: // SSL_NOT_ALLOWED_BY_SERVER
			msg = i18n.T("rdp.classicOnly")
		case 3: // SSL_CERT_NOT_ON_SERVER
			msg = i18n.T("rdp.noCert")
		case 5: // HYBRID_REQUIRED_BY_SERVER
			msg = i18n.T("rdp.nla")
		case 6: // SSL_WITH_USER_AUTH_REQUIRED_BY_SERVER
			msg = i18n.T("rdp.clientCert")
		}
		return &negError{cc, msg}
	}
	return &negError{cc, i18n.T("rdp.negUnknown")}
}

// wsaCode traduce errores de red a los códigos de Winsock que entiende el
// cliente web (iguales en todos los sistemas).
func wsaCode(err error) int {
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return 11001 // host no encontrado
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return 10060
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch {
		case errno == syscall.ECONNREFUSED || errno == 10061:
			return 10061
		case errno == syscall.ECONNRESET || errno == 10054:
			return 10054
		case errno == syscall.ENETUNREACH || errno == 10051:
			return 10051
		case errno == syscall.EHOSTUNREACH || errno == 10065:
			return 10065
		}
	}
	return 0
}

func friendlyDial(err error) error {
	switch wsaCode(err) {
	case 11001:
		return i18n.Err("app.theServerWasNotFound")
	case 10060:
		return i18n.Err("app.theServerIsnTResponding2")
	case 10061:
		return i18n.Err("app.theServerRefusedTheConnection")
	}
	return err
}

// OneShot: un Via que abre una sola conexión (el RD Gateway: otra sería otra
// aprobación del doble factor). El sondeo deja esa conexión abierta para la sesión.
type OneShot interface{ OneShot() }

// negProtocols: los protocolos de un RDP_NEG_REQ (typ 0x01, en un Connection
// Request) o de un RDP_NEG_RSP (typ 0x02, en un Connection Confirm), que van
// en los últimos 8 bytes del paquete (antes puede haber una cookie). 0 si no hay.
func negProtocols(pdu []byte, typ byte) uint32 {
	if len(pdu) < 19 || pdu[len(pdu)-8] != typ {
		return 0
	}
	b := pdu[len(pdu)-4:]
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

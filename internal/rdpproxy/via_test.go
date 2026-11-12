package rdpproxy

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"osat/internal/rdgateway"
)

// noDeadlineConn imita un túnel SSH: no acepta deadlines.
type noDeadlineConn struct{ net.Conn }

func (noDeadlineConn) SetDeadline(time.Time) error      { return errors.New("deadline not supported") }
func (noDeadlineConn) SetReadDeadline(time.Time) error  { return errors.New("deadline not supported") }
func (noDeadlineConn) SetWriteDeadline(time.Time) error { return errors.New("deadline not supported") }
func (noDeadlineConn) RemoteAddr() net.Addr             { return &net.TCPAddr{IP: net.IPv4zero} }

// fakeVia hace de bastión: abre las conexiones él y cuenta cuántas.
type fakeVia struct {
	dials  atomic.Int32
	closed atomic.Bool
}

func (v *fakeVia) Dial(network, addr string) (net.Conn, error) {
	v.dials.Add(1)
	c, err := net.Dial(network, addr)
	if err != nil {
		return nil, err
	}
	return noDeadlineConn{c}, nil
}

func (v *fakeVia) Close() error { v.closed.Store(true); return nil }

func TestProxyThroughJump(t *testing.T) {
	f := newFakeServer(t, ccTLS)
	p := newProxy(t)
	addr := net.JoinHostPort("127.0.0.1", itoa(f.port()))

	// Certificado desconocido: el error vuelve igual y el salto se cierra.
	v := &fakeVia{}
	_, err := p.OpenVia("127.0.0.1", f.port(), v)
	if code, data := osatErr(t, err); code != "RDPCERT_UNKNOWN" || !v.closed.Load() || v.dials.Load() != 1 {
		t.Fatalf("got %s %v (dials %d, cerrado %v)", code, data, v.dials.Load(), v.closed.Load())
	} else if err := p.Certs.Trust(addr, data["fingerprint"]); err != nil {
		t.Fatal(err)
	}
	<-f.got

	v = &fakeVia{}
	tk, err := p.OpenVia("127.0.0.1", f.port(), v)
	if err != nil {
		t.Fatal(err)
	}
	<-f.got
	ws := dialWS(t, tk.ProxyURL)
	sendRequest(t, ws, tk.Token, probeCR)
	<-f.got
	resp, _ := readPDU(t, ws)
	if !bytes.Equal(resp.X224, ccTLS) || resp.ServerAddr != addr {
		t.Fatalf("respuesta: %+v", resp)
	}
	if v.dials.Load() != 2 { // el sondeo y la sesión, los dos por el salto
		t.Fatalf("dials por el salto = %d", v.dials.Load())
	}
	if v.closed.Load() {
		t.Fatal("el salto se cerró con la sesión abierta")
	}
	p.Close(tk.ID)
	if !v.closed.Load() {
		t.Fatal("cerrar la sesión no cerró el salto")
	}
}

func TestProxyThroughJumpTimeout(t *testing.T) {
	// Un servidor que acepta y nunca contesta, por un túnel sin deadlines.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	prev := dialTimeout
	dialTimeout = 300 * time.Millisecond
	defer func() { dialTimeout = prev }()
	start := time.Now()
	v := &fakeVia{}
	if _, err := newProxy(t).OpenVia("127.0.0.1", ln.Addr().(*net.TCPAddr).Port, v); err == nil {
		t.Fatal("esperaba error de timeout")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("el sondeo por el salto tardó %v", d)
	}
	if !v.closed.Load() {
		t.Fatal("el salto quedó abierto después del error")
	}
}

// oneShotVia es un salto que solo abre una conexión (como el RD Gateway).
type oneShotVia struct{ fakeVia }

func (*oneShotVia) OneShot() {}

// openOneShot acepta el certificado del servidor y abre una sesión por un oneShotVia.
func openOneShot(t *testing.T, p *Proxy, f *fakeServer) (Ticket, *oneShotVia) {
	t.Helper()
	openTrusted(t, p, f)
	v := &oneShotVia{}
	tk, err := p.OpenVia("127.0.0.1", f.port(), v)
	if err != nil {
		t.Fatal(err)
	}
	<-f.got
	return tk, v
}

func TestOneShotDialsOnce(t *testing.T) {
	f := newFakeServer(t, ccTLS)
	p := newProxy(t)
	tk, v := openOneShot(t, p, f)
	ws := dialWS(t, tk.ProxyURL)
	sendRequest(t, ws, tk.Token, probeCR)
	resp, _ := readPDU(t, ws)
	if !bytes.Equal(resp.X224, ccTLS) || len(resp.CertChain) != 1 ||
		!bytes.Equal(resp.CertChain[0], f.certificate().Certificate[0]) {
		t.Fatalf("respuesta: %+v", resp)
	}
	msg := []byte("hola por el sondeo")
	if err := ws.WriteMessage(websocket.BinaryMessage, msg); err != nil {
		t.Fatal(err)
	}
	if _, m, err := ws.ReadMessage(); err != nil || !bytes.Equal(m, msg) {
		t.Fatalf("eco: %q, %v", m, err)
	}
	if v.dials.Load() != 1 {
		t.Fatalf("dials = %d", v.dials.Load())
	}
	select {
	case cr := <-f.got:
		t.Fatalf("el servidor recibió otra conexión: % x", cr)
	default:
	}
}

func TestOneShotProtocolMismatch(t *testing.T) {
	f := newFakeServer(t, ccTLS) // el sondeo negocia TLS (1)
	p := newProxy(t)
	tk, v := openOneShot(t, p, f)
	ws := dialWS(t, tk.ProxyURL)
	cr := append([]byte{}, probeCR...)
	cr[15] = 0x02 // IronRDP pide solo CredSSP
	sendRequest(t, ws, tk.Token, cr)
	if _, e := readPDU(t, ws); e.HTTP == 0 {
		t.Fatal("la sesión siguió con protocolos distintos a los del sondeo")
	}
	if v.dials.Load() != 1 {
		t.Fatalf("dials = %d", v.dials.Load())
	}
}

func TestOneShotServerClosedMeanwhile(t *testing.T) {
	f := newFakeServer(t, ccTLS)
	f.closeAfterTLS = true
	p := newProxy(t)
	tk, _ := openOneShot(t, p, f)
	ws := dialWS(t, tk.ProxyURL)
	sendRequest(t, ws, tk.Token, probeCR)
	readPDU(t, ws)
	_ = ws.SetReadDeadline(time.Now().Add(dialTimeout))
	if _, _, err := ws.ReadMessage(); err == nil {
		t.Fatal("el WebSocket sigue abierto con el servidor cerrado")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("la sesión quedó colgada")
	}
}

func TestCheckGatewayMarks(t *testing.T) {
	p := newProxy(t)
	cert := selfSigned(t, "gw")
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	code, data := osatErr(t, p.Certs.CheckGateway("gw:443", "gw", []*x509.Certificate{leaf}))
	if code != "RDPCERT_UNKNOWN" || data["gateway"] != "1" || data["host"] != "gw:443" {
		t.Fatalf("got %s %v", code, data)
	}
}

// El túnel del RD Gateway es un salto de una sola conexión.
var (
	_ Via     = (*rdgateway.Tunnel)(nil)
	_ OneShot = (*rdgateway.Tunnel)(nil)
)

// Al equipo RDP se le habla TLS 1.2 aunque ofrezca 1.3: algunos Windows
// cortan con "internal error" una sesión RDP sobre TLS 1.3.
func TestDialServerUsesTLS12(t *testing.T) {
	f := newFakeServer(t, ccTLS) // ofrece TLS 1.3
	conn, _, err := dialServer(net.JoinHostPort("127.0.0.1", itoa(f.port())), "127.0.0.1", probeCR,
		func(tls.ConnectionState) error { return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if v := conn.ConnectionState().Version; v != tls.VersionTLS12 {
		t.Fatalf("TLS negociado: %s", tls.VersionName(v))
	}
}

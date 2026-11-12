package rdpproxy

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/json"
	"io"
	"math/big"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// ccTLS: Connection Confirm con RDP_NEG_RSP que elige TLS (protocolo 1).
var ccTLS = []byte{0x03, 0x00, 0x00, 0x13, 0x0e, 0xd0, 0, 0, 0x12, 0x34, 0, 0x02, 0x00, 0x08, 0x00, 0x01, 0, 0, 0}

// ccFailure: RDP_NEG_FAILURE con HYBRID_REQUIRED_BY_SERVER (5).
var ccFailure = []byte{0x03, 0x00, 0x00, 0x13, 0x0e, 0xd0, 0, 0, 0x12, 0x34, 0, 0x03, 0x00, 0x08, 0x00, 0x05, 0, 0, 0}

func selfSigned(t *testing.T, cn string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// fakeServer es un servidor "RDP": responde el X.224 con cc y, si eligió TLS,
// hace eco de lo que llega por TLS. cert se puede cambiar entre conexiones.
type fakeServer struct {
	ln  net.Listener
	cc  []byte
	got chan []byte // el Connection Request de cada conexión

	closeAfterTLS bool // corta apenas termina el TLS (como un servidor que se cansó de esperar)

	mu   sync.Mutex
	cert tls.Certificate
}

func newFakeServer(t *testing.T, cc []byte) *fakeServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeServer{ln: ln, cc: cc, cert: selfSigned(t, "fake-rdp"), got: make(chan []byte, 10)}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c, f.cc, f.certificate())
		}
	}()
	return f
}

func (f *fakeServer) certificate() tls.Certificate {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cert
}

func (f *fakeServer) setCert(c tls.Certificate) {
	f.mu.Lock()
	f.cert = c
	f.mu.Unlock()
}

func (f *fakeServer) serve(c net.Conn, cc []byte, cert tls.Certificate) {
	defer c.Close()
	cr, err := readTPKT(c)
	if err != nil {
		return
	}
	f.got <- cr
	if _, err := c.Write(cc); err != nil || cc[11] != 0x02 {
		return
	}
	t := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{cert}})
	if f.closeAfterTLS {
		_ = t.Handshake()
		return
	}
	_, _ = io.Copy(t, t) // eco
}

func (f *fakeServer) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func osatErr(t *testing.T, err error) (code string, data map[string]string) {
	t.Helper()
	s := strings.TrimPrefix(err.Error(), "OSAT:")
	var e struct {
		Code string            `json:"code"`
		Data map[string]string `json:"data"`
	}
	if s == err.Error() || json.Unmarshal([]byte(s), &e) != nil {
		t.Fatalf("no es un error OSAT: %v", err)
	}
	return e.Code, e.Data
}

func newProxy(t *testing.T) *Proxy {
	t.Helper()
	p, err := New(filepath.Join(t.TempDir(), "rdp_known_certs"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.CloseAll)
	return p
}

// openTrusted acepta el certificado del servidor y abre la sesión.
func openTrusted(t *testing.T, p *Proxy, f *fakeServer) Ticket {
	t.Helper()
	_, err := p.Open("127.0.0.1", f.port())
	if err == nil {
		t.Fatal("un certificado autofirmado se aceptó sin preguntar")
	}
	code, data := osatErr(t, err)
	if code != "RDPCERT_UNKNOWN" || data["subject"] != "fake-rdp" || data["fingerprint"] != Fingerprint(f.certificate().Certificate[0]) {
		t.Fatalf("got %s %v", code, data)
	}
	hostPort := net.JoinHostPort("127.0.0.1", itoa(f.port()))
	if err := p.Certs.Trust(hostPort, "AA:BB"); err == nil {
		t.Fatal("se aceptó una huella distinta de la que se vio")
	}
	if err := p.Certs.Trust(hostPort, data["fingerprint"]); err != nil {
		t.Fatal(err)
	}
	tk, err := p.Open("127.0.0.1", f.port())
	if err != nil {
		t.Fatal(err)
	}
	<-f.got // los dos sondeos: el que pidió confirmar y el que pasó
	<-f.got
	return tk
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func dialWS(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	ws, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close() })
	return ws
}

func sendRequest(t *testing.T, ws *websocket.Conn, auth string, x224 []byte) {
	t.Helper()
	b, err := asn1.Marshal(request{Version: cleanPathVersion, Destination: "otro-host:3389", ProxyAuth: auth, X224ConnectionPDU: x224})
	if err != nil {
		t.Fatal(err)
	}
	// En dos mensajes: el proxy tiene que juntarlos.
	for _, part := range [][]byte{b[:5], b[5:]} {
		if err := ws.WriteMessage(websocket.BinaryMessage, part); err != nil {
			t.Fatal(err)
		}
	}
}

func readPDU(t *testing.T, ws *websocket.Conn) (fullPDU, fullErr) {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, m, err := ws.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var p fullPDU
	if _, err := asn1.Unmarshal(m, &p); err != nil {
		t.Fatal(err)
	}
	var e fullErr
	if len(p.Error.Bytes) > 0 {
		if _, err := asn1.Unmarshal(p.Error.Bytes, &e); err != nil {
			t.Fatal(err)
		}
	}
	return p, e
}

func TestProxyEndToEnd(t *testing.T) {
	f := newFakeServer(t, ccTLS)
	p := newProxy(t)
	tk := openTrusted(t, p, f)
	if tk.Destination != net.JoinHostPort("127.0.0.1", itoa(f.port())) {
		t.Fatalf("destination = %s", tk.Destination)
	}

	ws := dialWS(t, tk.ProxyURL)
	clientCR := append([]byte{}, probeCR...)
	clientCR[15] = 0x03 // otro pedido que el del sondeo: tiene que llegar este
	sendRequest(t, ws, tk.Token, clientCR)

	// Va al servidor de la conexión, no al "otro-host" del pedido.
	if cr := <-f.got; !bytes.Equal(cr, clientCR) {
		t.Fatalf("el servidor recibió % x", cr)
	}
	resp, _ := readPDU(t, ws)
	if !bytes.Equal(resp.X224, ccTLS) || len(resp.CertChain) != 1 ||
		!bytes.Equal(resp.CertChain[0], f.certificate().Certificate[0]) || resp.ServerAddr == "" {
		t.Fatalf("respuesta: %+v", resp)
	}

	// Ya reenvía en crudo: lo que se manda vuelve por el eco TLS.
	msg := bytes.Repeat([]byte("osat"), 50000) // más que un registro TLS
	if err := ws.WriteMessage(websocket.BinaryMessage, msg); err != nil {
		t.Fatal(err)
	}
	var got []byte
	for len(got) < len(msg) {
		_, m, err := ws.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, m...)
	}
	if !bytes.Equal(got, msg) {
		t.Fatal("el eco no coincide")
	}

	// Un segundo cliente no puede usar la misma sesión.
	if _, _, err := websocket.DefaultDialer.Dial(tk.ProxyURL, nil); err == nil {
		t.Fatal("se aceptó un segundo WebSocket")
	}

	// Cerrar la sesión corta el WebSocket.
	p.Close(tk.ID)
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := ws.ReadMessage(); err == nil {
		t.Fatal("el WebSocket sigue abierto después de Close")
	}
}

func TestProxyRejectsWrongPathAndHost(t *testing.T) {
	f := newFakeServer(t, ccTLS)
	p := newProxy(t)
	tk := openTrusted(t, p, f)
	base := strings.TrimSuffix(tk.ProxyURL, tk.Token)
	if _, resp, err := websocket.DefaultDialer.Dial(base+"otro", nil); err == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("ruta incorrecta: %v %v", err, resp)
	}
	// Mismo puerto pero con otro Host (como llegaría por DNS rebinding).
	port := strings.TrimPrefix(strings.Split(tk.ProxyURL, "/")[2], "127.0.0.1")
	h := http.Header{}
	d := websocket.Dialer{NetDial: func(string, string) (net.Conn, error) { return net.Dial("tcp", "127.0.0.1"+port) }}
	_, resp, err := d.Dial("ws://evil.example"+port+"/"+tk.Token, h)
	if err == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("Host incorrecto: %v %v", err, resp)
	}
	// Los intentos rechazados no gastan la sesión.
	dialWS(t, tk.ProxyURL)
}

func TestProxyWrongAuth(t *testing.T) {
	f := newFakeServer(t, ccTLS)
	p := newProxy(t)
	tk := openTrusted(t, p, f)
	ws := dialWS(t, tk.ProxyURL)
	sendRequest(t, ws, "otro-token", probeCR)
	_, e := readPDU(t, ws)
	if e.Code != errGeneral || e.HTTP != http.StatusUnauthorized {
		t.Fatalf("got %+v", e)
	}
	select {
	case <-f.got:
		t.Fatal("con un token incorrecto se conectó al servidor")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestProxyNegotiationFailure(t *testing.T) {
	f := newFakeServer(t, ccFailure)
	p := newProxy(t)
	_, err := p.Open("127.0.0.1", f.port())
	if err == nil || !strings.Contains(err.Error(), "NLA") {
		t.Fatalf("sondeo: %v", err)
	}
}

func TestProxyCertMismatch(t *testing.T) {
	f := newFakeServer(t, ccTLS)
	p := newProxy(t)
	openTrusted(t, p, f)
	old := Fingerprint(f.certificate().Certificate[0])
	f.setCert(selfSigned(t, "otro"))
	_, err := p.Open("127.0.0.1", f.port())
	code, data := osatErr(t, err)
	if code != "RDPCERT_MISMATCH" || data["expected"] != old || data["fingerprint"] != Fingerprint(f.certificate().Certificate[0]) {
		t.Fatalf("got %s %v", code, data)
	}
}

func TestProxyCertChangedAfterProbe(t *testing.T) {
	f := newFakeServer(t, ccTLS)
	p := newProxy(t)
	tk := openTrusted(t, p, f)
	f.setCert(selfSigned(t, "atacante")) // entre el sondeo y la conexión real
	ws := dialWS(t, tk.ProxyURL)
	sendRequest(t, ws, tk.Token, probeCR)
	_, e := readPDU(t, ws)
	if e.Code != errGeneral || e.HTTP != http.StatusBadGateway {
		t.Fatalf("got %+v", e)
	}
}

func TestProxyRefused(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	_, err := newProxy(t).Open("127.0.0.1", port)
	if err == nil || !strings.Contains(err.Error(), "rechazó la conexión") {
		t.Fatalf("got %v", err)
	}
}

func TestProxyOpenAfterCloseAll(t *testing.T) {
	f := newFakeServer(t, ccTLS)
	p := newProxy(t)
	openTrusted(t, p, f)
	p.CloseAll()
	if _, err := p.Open("127.0.0.1", f.port()); err == nil {
		t.Fatal("Open después de CloseAll dejó una sesión abierta")
	}
}

func TestProxyPlainGetDoesNotBurnSession(t *testing.T) {
	f := newFakeServer(t, ccTLS)
	p := newProxy(t)
	tk := openTrusted(t, p, f)
	resp, err := http.Get("http" + strings.TrimPrefix(tk.ProxyURL, "ws"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusSwitchingProtocols {
		t.Fatal("un GET común hizo upgrade")
	}
	dialWS(t, tk.ProxyURL) // la sesión sigue disponible
}

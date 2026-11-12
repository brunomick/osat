//go:build e2e

package rdpproxy

import (
	"crypto/x509"
	"net"
	"os"
	"strconv"
	"testing"
)

// Contra un servidor RDP real (tools/wsl-rdp: xrdp en 127.0.0.1:3390):
//
//	OSAT_RDP_ADDR=127.0.0.1:3390 go test -tags e2e -run E2E -v ./internal/rdpproxy/
//
// Recorre sondeo, confirmación del certificado y el pedido RDCleanPath hasta
// que el proxy entrega el Connection Confirm y la cadena del servidor.
func TestE2ERealServer(t *testing.T) {
	addr := os.Getenv("OSAT_RDP_ADDR")
	if addr == "" {
		addr = "127.0.0.1:3390"
	}
	host, ps, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(ps)
	if c, err := net.Dial("tcp", addr); err != nil {
		t.Skipf("no hay servidor RDP en %s: %v", addr, err)
	} else {
		c.Close()
	}

	p := newProxy(t)
	_, err := p.Open(host, port)
	if err != nil {
		code, data := osatErr(t, err)
		t.Logf("sondeo: %s, certificado %q de %q, huella %s", code, data["subject"], data["issuer"], data["fingerprint"])
		if code != "RDPCERT_UNKNOWN" {
			t.Fatalf("got %s", code)
		}
		if err := p.Certs.Trust(addr, data["fingerprint"]); err != nil {
			t.Fatal(err)
		}
	}
	tk, err := p.Open(host, port)
	if err != nil {
		t.Fatal(err)
	}

	ws := dialWS(t, tk.ProxyURL)
	sendRequest(t, ws, tk.Token, probeCR)
	resp, e := readPDU(t, ws)
	if e.Code != 0 {
		t.Fatalf("el proxy devolvió error: %+v", e)
	}
	if err := checkNegotiation(resp.X224); err != nil {
		t.Fatalf("Connection Confirm: %v", err)
	}
	selected := resp.X224[15]
	t.Logf("protocolo elegido por el servidor: %d (1 = TLS, 2 = CredSSP)", selected)
	if len(resp.CertChain) == 0 {
		t.Fatal("sin cadena de certificados")
	}
	cert, err := x509.ParseCertificate(resp.CertChain[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("certificado del servidor: %q, servidor %s", cert.Subject.CommonName, resp.ServerAddr)
}

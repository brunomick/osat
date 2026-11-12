//go:build e2e

package rdgateway

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

type testLog struct{ t *testing.T }

func (w testLog) Write(p []byte) (int, error) {
	w.t.Log(string(p))
	return len(p), nil
}

// TestE2EGateway: contra un RD Gateway real (lo corre una persona, con la VPN).
// Abre el túnel (puede llegar una notificación al celular), un canal hasta
// el equipo y verifica que conteste un X.224 de RDP.
//
//	OSAT_RDG_HOST=rds.empresa.com OSAT_RDG_DOMAIN=EMPRESA OSAT_RDG_USER=nombre
//	OSAT_RDG_PASSWORD=... OSAT_RDG_TARGET=equipo.empresa.com:3389
//	go test -tags e2e -run E2EGateway -v ./internal/rdgateway/
func TestE2EGateway(t *testing.T) {
	env := map[string]string{}
	for _, k := range []string{"OSAT_RDG_HOST", "OSAT_RDG_DOMAIN", "OSAT_RDG_USER", "OSAT_RDG_PASSWORD", "OSAT_RDG_TARGET"} {
		if env[k] = os.Getenv(k); env[k] == "" {
			t.Skip("falta " + k)
		}
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(testLog{t}, &slog.HandlerOptions{Level: slog.LevelDebug})))
	host, port := env["OSAT_RDG_HOST"], 443
	if h, p, err := net.SplitHostPort(host); err == nil {
		host, port = h, atoi(p)
	}
	name, _ := os.Hostname()
	cfg := Config{
		Host: host, Port: port, Domain: env["OSAT_RDG_DOMAIN"], User: env["OSAT_RDG_USER"],
		Password: env["OSAT_RDG_PASSWORD"], ClientName: name,
		VerifyConnection: func(cs tls.ConnectionState) error {
			c := cs.PeerCertificates[0]
			t.Logf("certificado del gateway: sujeto=%q emisor=%q vence=%s sha256=%X",
				c.Subject.CommonName, c.Issuer.CommonName, c.NotAfter.Format("2006-01-02"), sha256.Sum256(c.Raw))
			return nil
		},
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	t.Log("conectando al gateway (aprobá la notificación en el celular si llega)…")
	tun, err := Connect(ctx, cfg)
	if err != nil {
		t.Fatalf("Connect (%s): %v", time.Since(start), err)
	}
	defer tun.Close()
	t.Logf("túnel autorizado en %s", time.Since(start))

	c, err := tun.Dial("tcp", env["OSAT_RDG_TARGET"])
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	t.Logf("canal abierto en %s", time.Since(start))

	// El X.224 Connection Request del sondeo de rdpproxy (probeCR).
	cr := []byte{0x03, 0x00, 0x00, 0x13, 0x0e, 0xe0, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x01, 0x00, 0x08, 0x00, 0x0b, 0x00, 0x00, 0x00}
	if _, err := c.Write(cr); err != nil {
		t.Fatalf("Write: %v", err)
	}
	b := make([]byte, 4)
	if _, err := io.ReadFull(c, b); err != nil {
		t.Fatalf("leyendo la respuesta del equipo: %v", err)
	}
	if b[0] != 3 {
		t.Fatalf("el equipo no respondió RDP: % X", b)
	}
	t.Logf("el equipo respondió RDP (TPKT de %d bytes) en %s", int(b[2])<<8|int(b[3]), time.Since(start))
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		panic(fmt.Sprintf("puerto inválido %q", s))
	}
	return n
}

// TestE2EGatewayTLS: diagnóstico sin credenciales. Prueba el primer pedido
// (solo el NEGOTIATE de NTLM, como curl) con varias configuraciones de TLS y
// dice cuáles contesta el gateway (401) y cuáles corta. Solo OSAT_RDG_HOST.
func TestE2EGatewayTLS(t *testing.T) {
	gw := os.Getenv("OSAT_RDG_HOST")
	if gw == "" {
		t.Skip("falta OSAT_RDG_HOST")
	}
	host, port := gw, 443
	if h, p, err := net.SplitHostPort(gw); err == nil {
		host, port = h, atoi(p)
	}
	cases := []struct {
		name string
		cfg  *tls.Config
	}{
		{"por defecto (como OSAT)", &tls.Config{NextProtos: []string{"http/1.1"}}},
		{"TLS 1.3 sin post-cuántico", &tls.Config{NextProtos: []string{"http/1.1"}, CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256, tls.CurveP384}}},
		{"TLS 1.2", &tls.Config{NextProtos: []string{"http/1.1"}, MaxVersion: tls.VersionTLS12}},
		{"TLS 1.2 sin ALPN", &tls.Config{MaxVersion: tls.VersionTLS12}},
		{"TLS 1.3 sin ALPN", &tls.Config{}},
	}
	for _, c := range cases {
		cfg := c.cfg
		cfg.ServerName, cfg.MinVersion = host, tls.VersionTLS12
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", net.JoinHostPort(host, strconv.Itoa(port)), cfg)
		if err != nil {
			t.Logf("%-28s → no conectó: %v", c.name, err)
			continue
		}
		cs := conn.ConnectionState()
		conn.SetDeadline(time.Now().Add(10 * time.Second))
		_, k, err := newUpgradeRequest(host, port).ntlmChallenge(conn, bufio.NewReader(conn), ntlmNegotiate())
		res := "401 con desafío NTLM (OK)"
		if err != nil {
			res = fmt.Sprintf("falló (kind %d): %v", k, err)
		}
		t.Logf("%-28s → %s [%s, %s, curva %s, alpn %q]", c.name, res, tls.VersionName(cs.Version),
			tls.CipherSuiteName(cs.CipherSuite), cs.CurveID, cs.NegotiatedProtocol)
		conn.Close()
	}
}

// TestE2EGatewayDiag: diagnóstico sin credenciales reales para cuando el
// pedido con la autenticación NTLM no recibe respuesta. Separa tres causas:
// cualquier segundo pedido en la misma conexión, el tamaño del pedido o el
// contenido NTLM (con un usuario inventado: no bloquea cuentas ni dispara el
// doble factor). Solo OSAT_RDG_HOST.
func TestE2EGatewayDiag(t *testing.T) {
	gw := os.Getenv("OSAT_RDG_HOST")
	if gw == "" {
		t.Skip("falta OSAT_RDG_HOST")
	}
	host, port := gw, 443
	if h, p, err := net.SplitHostPort(gw); err == nil {
		host, port = h, atoi(p)
	}
	dial := func() (*tls.Conn, *bufio.Reader, error) {
		c, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", net.JoinHostPort(host, strconv.Itoa(port)),
			&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}})
		if err != nil {
			return nil, nil, err
		}
		c.SetDeadline(time.Now().Add(25 * time.Second))
		return c, bufio.NewReader(c), nil
	}
	// send manda un pedido (con relleno opcional) y devuelve el estado HTTP.
	send := func(c *tls.Conn, br *bufio.Reader, r *upgradeRequest, auth string, pad int) (string, error) {
		b := r.bytes("dGhlIHNhbXBsZSBub25jZQ==", auth)
		if pad > 0 {
			b = append(b[:len(b)-2], []byte("X-Osat-Relleno: "+strings.Repeat("x", pad)+"\r\n\r\n")...)
		}
		start := time.Now()
		if _, err := c.Write(b); err != nil {
			return "", err
		}
		resp, err := http.ReadResponse(br, &http.Request{Method: "RDG_OUT_DATA"})
		if err != nil {
			return "", fmt.Errorf("%d bytes, sin respuesta tras %s: %w", len(b), time.Since(start).Round(time.Millisecond), err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return fmt.Sprintf("HTTP %d (%d bytes, %s)", resp.StatusCode, len(b), time.Since(start).Round(time.Millisecond)), nil
	}
	report := func(name string, res string, err error) {
		if err != nil {
			t.Logf("%-36s → FALLA: %v", name, err)
		} else {
			t.Logf("%-36s → %s", name, res)
		}
	}
	neg := "NTLM " + base64.StdEncoding.EncodeToString(ntlmNegotiate())

	// (a) Dos pedidos chicos por la misma conexión.
	if c, br, err := dial(); err != nil {
		report("a) conectar", "", err)
	} else {
		r := newUpgradeRequest(host, port)
		res, err := send(c, br, r, neg, 0)
		report("a) 1.er pedido chico", res, err)
		res, err = send(c, br, r, neg, 0)
		report("a) 2.º pedido chico, misma conexión", res, err)
		c.Close()
	}
	// (b) Un solo pedido grande (relleno sin NTLM nuevo).
	if c, br, err := dial(); err != nil {
		report("b) conectar", "", err)
	} else {
		res, err := send(c, br, newUpgradeRequest(host, port), neg, 900)
		report("b) 1.er pedido grande", res, err)
		c.Close()
	}
	// authCase: la autenticación NTLM completa con las credenciales dadas.
	authCase := func(name, domain, user, password, workstation string) {
		c, br, err := dial()
		if err != nil {
			report(name+": conectar", "", err)
			return
		}
		defer c.Close()
		r := newUpgradeRequest(host, port)
		chal, _, err := r.ntlmChallenge(c, br, mustNeg(neg))
		if err != nil {
			report(name+": desafío NTLM", "", err)
			return
		}
		cbt := channelBindings(c.ConnectionState().PeerCertificates[0].Raw)
		auth, err := ntlmAuthenticate(mustNeg(neg), chal, domain, user, password, workstation, "HTTP/"+host, cbt)
		if err != nil {
			report(name+": armar AUTHENTICATE", "", err)
			return
		}
		res, err := send(c, br, r, "NTLM "+base64.StdEncoding.EncodeToString(auth), 0)
		report(name, res, err)
	}
	// (c) Un usuario inventado: no bloquea cuentas ni dispara el doble factor.
	authCase("c) AUTHENTICATE (usuario inventado)", "", "osat-prueba", "x", "PC")
	// (d) El usuario real con una contraseña incorrecta a propósito: suma un
	// intento fallido a la cuenta, pero no llega al doble factor.
	if user := os.Getenv("OSAT_RDG_USER"); user != "" {
		name, _ := os.Hostname()
		authCase("d) usuario real, contraseña incorrecta", os.Getenv("OSAT_RDG_DOMAIN"), user, "osat-diagnostico-incorrecta", name)
	}
}

// mustNeg: el NEGOTIATE de un encabezado "NTLM <base64>".
func mustNeg(h string) []byte {
	b, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(h, "NTLM "))
	return b
}

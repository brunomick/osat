package rdgateway

import (
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

const secret = "S3cr3t-Pa55word"

// configFor: un Config contra un servidor de prueba que no es el falso gateway.
func configFor(t *testing.T, addr string) Config {
	t.Helper()
	host, port, _ := net.SplitHostPort(addr)
	p, _ := strconv.Atoi(port)
	return Config{Host: host, Port: p, Domain: "EMPRESA", User: "u", Password: secret, ClientName: "PC",
		VerifyConnection: func(tls.ConnectionState) error { return nil }}
}

// tlsServer: un servidor TLS que atiende con h (sin falso gateway).
func tlsServer(t *testing.T, h http.HandlerFunc) Config {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	return configFor(t, srv.Listener.Addr().String())
}

// Cada paso que puede fallar da su propio Kind, Step y matiz, y Data lleva el
// código del gateway, el estado HTTP y el equipo, sin la contraseña.
func TestFailureSteps(t *testing.T) {
	type want struct {
		kind   Kind
		step   string
		reason string
		data   map[string]string // además de kind, step y reason
	}
	cases := []struct {
		name string
		cfg  func(t *testing.T) Config
		dial string // si no es "", el error es al abrir el canal a este equipo
		want want
	}{
		{"nombre del gateway inexistente", func(t *testing.T) Config {
			return Config{Host: "no-existe.invalid", Port: 443, User: "u", Password: secret}
		}, "", want{KindDNS, StepDNS, "", nil}},
		{"puerto cerrado", func(t *testing.T) Config {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			addr := ln.Addr().String()
			ln.Close() // nadie escucha: el sistema rechaza la conexión
			return configFor(t, addr)
		}, "", want{KindUnreachable, StepTCP, "refused", nil}},
		{"TLS cortado sin respuesta", func(t *testing.T) Config {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { ln.Close() })
			go func() {
				for {
					c, err := ln.Accept()
					if err != nil {
						return
					}
					c.Close()
				}
			}()
			return configFor(t, ln.Addr().String())
		}, "", want{KindTLS, StepTLS, "closed", nil}},
		{"TLS rechazado por la versión", func(t *testing.T) Config {
			srv := httptest.NewUnstartedServer(http.NotFoundHandler())
			srv.TLS = &tls.Config{MaxVersion: tls.VersionTLS11}
			srv.StartTLS()
			t.Cleanup(srv.Close)
			return configFor(t, srv.Listener.Addr().String())
		}, "", want{KindTLS, StepTLS, "rejected", nil}},
		{"HTTP inesperado", func(t *testing.T) Config {
			return tlsServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Server", "nginx")
				w.WriteHeader(http.StatusForbidden)
			})
		}, "", want{KindHTTP, StepHTTP, "", map[string]string{"status": "403"}}},
		{"cortado sin contestar el primer pedido", func(t *testing.T) Config {
			return tlsServer(t, func(w http.ResponseWriter, r *http.Request) {
				if c, _, err := w.(http.Hijacker).Hijack(); err == nil {
					c.Close()
				}
			})
		}, "", want{KindConnectionLost, StepHTTP, "noAnswer", nil}},
		{"sin NTLM", func(t *testing.T) Config {
			_, cfg := newFakeGateway(t, &fakeGateway{schemes: []string{"Negotiate"}})
			return cfg
		}, "", want{KindNoNTLM, StepHTTP, "", nil}},
		{"usuario o contraseña", func(t *testing.T) Config {
			_, cfg := newFakeGateway(t, &fakeGateway{user: "otro"})
			cfg.Password = secret
			return cfg
		}, "", want{KindAuth, StepAuth, "", map[string]string{"status": "401"}}},
		{"cortado en la autenticación", func(t *testing.T) Config {
			_, cfg := newFakeGateway(t, &fakeGateway{dropAtNTLM: true})
			return cfg
		}, "", want{KindConnectionLost, StepAuth, "", nil}},
		{"CAP rechaza al usuario", func(t *testing.T) Config {
			_, cfg := newFakeGateway(t, &fakeGateway{authCode: 0x800759DB})
			return cfg
		}, "", want{KindNotAuthorized, StepTunnelAuth, "cap", map[string]string{"code": "0x800759DB", "codeName": "E_PROXY_NAP_ACCESSDENIED"}}},
		{"otro código al autorizar", func(t *testing.T) Config {
			_, cfg := newFakeGateway(t, &fakeGateway{authCode: 0x800759E6})
			return cfg
		}, "", want{KindNotAuthorized, StepTunnelAuth, "", map[string]string{"code": "0x800759E6", "codeName": "E_PROXY_MAXCONNECTIONSREACHED"}}},
		{"doble factor rechazado (corta)", func(t *testing.T) Config {
			_, cfg := newFakeGateway(t, &fakeGateway{dropAtAuth: true})
			return cfg
		}, "", want{KindNotAuthorized, StepTunnelAuth, "dropped", nil}},
		{"RAP no permite el equipo", func(t *testing.T) Config {
			_, cfg := newFakeGateway(t, &fakeGateway{channelCode: 0x800759DA})
			return cfg
		}, "srv:3389", want{KindResourceDenied, StepChannel, "", map[string]string{"target": "srv:3389", "code": "0x800759DA", "codeName": "E_PROXY_RAP_ACCESSDENIED"}}},
		{"el gateway no llega al equipo", func(t *testing.T) Config {
			_, cfg := newFakeGateway(t, &fakeGateway{channelCode: 0x800759DD})
			return cfg
		}, "srv:3389", want{KindTargetUnreachable, StepChannel, "connect", map[string]string{"target": "srv:3389", "code": "0x800759DD", "codeName": "E_PROXY_TS_CONNECTFAILED"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := c.cfg(t)
			tun, err := connect(t, cfg, 10*time.Second)
			if c.dial != "" {
				if err != nil {
					t.Fatal(err)
				}
				_, err = tun.Dial("tcp", c.dial)
			}
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("err = %v", err)
			}
			if e.Kind != c.want.kind || e.Step != c.want.step || e.Reason != c.want.reason {
				t.Fatalf("kind=%v step=%q reason=%q, se esperaba %v %q %q (err: %v)", e.Kind, e.Step, e.Reason,
					c.want.kind, c.want.step, c.want.reason, err)
			}
			d := e.Data()
			if d["kind"] != c.want.kind.String() || d["step"] != c.want.step || d["gateway"] == "" {
				t.Fatalf("data = %v", d)
			}
			for k, v := range c.want.data {
				if d[k] != v {
					t.Fatalf("data[%s] = %q, se esperaba %q (%v)", k, d[k], v, d)
				}
			}
			for k, v := range d {
				if strings.Contains(v, secret) {
					t.Fatalf("data[%s] tiene la contraseña", k)
				}
			}
			if strings.Contains(e.Error(), secret) {
				t.Fatal("el mensaje tiene la contraseña")
			}
		})
	}
}

// Solo la falla del TCP (y el nombre del gateway) habla de la VPN: si el TCP
// conectó, la VPN anda.
func TestOnlyTCPMentionsVPN(t *testing.T) {
	for k := range kindNames {
		msg := (&Error{Kind: k, Gateway: "gw:443", Err: errors.New("x")}).Error()
		if mentions := strings.Contains(msg, "VPN"); mentions != (k == KindUnreachable) {
			t.Errorf("%v: %q", k, msg)
		}
	}
}

func TestHResultName(t *testing.T) {
	for code, want := range map[uint32]string{
		0x800759DB: "E_PROXY_NAP_ACCESSDENIED",
		0x000059DD: "E_PROXY_TS_CONNECTFAILED", // sin el 0x8007
		0x800704D4: "E_PROXY_CONNECTIONABORTED",
		0x80070005: "",
		0:          "",
	} {
		if got := HResultName(code); got != want {
			t.Errorf("0x%08X: %q", code, got)
		}
	}
}

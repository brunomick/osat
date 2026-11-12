package rdgateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func connect(t *testing.T, cfg Config, timeout time.Duration) (*Tunnel, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	tun, err := Connect(ctx, cfg)
	if tun != nil {
		t.Cleanup(func() { tun.Close() })
	}
	return tun, err
}

func kindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return 0
}

func echo(t *testing.T, g *fakeGateway, cfg Config) {
	t.Helper()
	tun, err := connect(t, cfg, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c, err := tun.Dial("tcp", "srv:3389")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	data := make([]byte, 200<<10)
	rand.Read(data)
	go c.Write(data)
	got := make([]byte, len(data))
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("el eco no coincide")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.chHost != "srv" || g.chPort != 3389 {
		t.Fatalf("canal a %s:%d", g.chHost, g.chPort)
	}
}

func TestConnectAndEcho(t *testing.T) {
	g, cfg := newFakeGateway(t, &fakeGateway{})
	echo(t, g, cfg)
}

func TestFragmentedPackets(t *testing.T) {
	g, cfg := newFakeGateway(t, &fakeGateway{fragment: true})
	echo(t, g, cfg)
}

func TestSecondDialFails(t *testing.T) {
	_, cfg := newFakeGateway(t, &fakeGateway{})
	tun, err := connect(t, cfg, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tun.Dial("tcp", "srv:3389"); err != nil {
		t.Fatal(err)
	}
	if _, err := tun.Dial("tcp", "srv:3389"); !errors.Is(err, ErrChannelUsed) {
		t.Fatalf("segundo Dial: %v", err)
	}
}

func TestBadPassword(t *testing.T) {
	_, cfg := newFakeGateway(t, &fakeGateway{user: "otro"})
	if _, err := connect(t, cfg, 5*time.Second); kindOf(err) != KindAuth {
		t.Fatalf("err = %v", err)
	}
}

func TestNoNTLMOffered(t *testing.T) {
	_, cfg := newFakeGateway(t, &fakeGateway{schemes: []string{"Negotiate"}})
	if _, err := connect(t, cfg, 5*time.Second); kindOf(err) != KindNoNTLM {
		t.Fatalf("err = %v", err)
	}
}

func TestApprovalWaits(t *testing.T) {
	_, cfg := newFakeGateway(t, &fakeGateway{authDelay: 2 * time.Second})
	if _, err := connect(t, cfg, 5*time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestApprovalTimeout(t *testing.T) {
	g, cfg := newFakeGateway(t, &fakeGateway{authDelay: 3 * time.Second})
	if _, err := connect(t, cfg, time.Second); kindOf(err) != KindTimeout {
		t.Fatalf("err = %v", err)
	}
	select {
	case <-g.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("el gateway no vio la conexión cerrada")
	}
}

func TestTunnelAuthDenied(t *testing.T) {
	_, cfg := newFakeGateway(t, &fakeGateway{authCode: 0x800759DB})
	_, err := connect(t, cfg, 5*time.Second)
	var e *Error
	if !errors.As(err, &e) || e.Kind != KindNotAuthorized || e.Code != 0x800759DB {
		t.Fatalf("err = %v", err)
	}
}

func TestResourceDenied(t *testing.T) {
	_, cfg := newFakeGateway(t, &fakeGateway{channelCode: 0x800759DA})
	tun, err := connect(t, cfg, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tun.Dial("tcp", "srv:3389"); kindOf(err) != KindResourceDenied {
		t.Fatalf("err = %v", err)
	}
}

func TestVerifyErrorPassesThrough(t *testing.T) {
	sentinel := errors.New("certificado no confiable")
	_, cfg := newFakeGateway(t, &fakeGateway{})
	cfg.VerifyConnection = func(tls.ConnectionState) error { return sentinel }
	if _, err := connect(t, cfg, 5*time.Second); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v", err)
	}
}

func TestServerCloseChannel(t *testing.T) {
	_, cfg := newFakeGateway(t, &fakeGateway{closeAfter: true})
	tun, err := connect(t, cfg, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c, err := tun.Dial("tcp", "srv:3389")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Read(make([]byte, 10)); err != io.EOF {
		t.Fatalf("Read = %v", err)
	}
}

func TestUnreachable(t *testing.T) {
	cfg := Config{Host: "127.0.0.1", Port: 1, User: "u", Password: "p"}
	if _, err := connect(t, cfg, 5*time.Second); kindOf(err) != KindUnreachable {
		t.Fatalf("err = %v", err)
	}
}

// Un RD Gateway de Windows corta la conexión sin responder si el pedido no es
// como el de los clientes de Windows (y FreeRDP): método RDG_OUT_DATA también
// para el upgrade, Content-Length: 0, RDG-Connection-Id y RDG-Correlation-Id
// escritos así, User-Agent MS-RDGateway/1.0.
func TestRequestLikeWindows(t *testing.T) {
	r := newUpgradeRequest("rds.corp", 443)
	got := string(r.bytes("a2V5", "NTLM abc"))
	if !strings.HasPrefix(got, "RDG_OUT_DATA /remoteDesktopGateway/ HTTP/1.1\r\n") || !strings.HasSuffix(got, "\r\n\r\n") {
		t.Fatalf("pedido:\n%s", got)
	}
	for _, want := range []string{
		"\r\nHost: rds.corp\r\n", "\r\nRDG-Connection-Id: {" + r.connID[1:], "\r\nRDG-Correlation-Id: {",
		"\r\nUser-Agent: MS-RDGateway/1.0\r\n", "\r\nAccept: */*\r\n", "\r\nContent-Length: 0\r\n",
		"\r\nConnection: Upgrade\r\n", "\r\nUpgrade: websocket\r\n", "\r\nSec-Websocket-Version: 13\r\n",
		"\r\nSec-Websocket-Key: a2V5\r\n", "\r\nAuthorization: NTLM abc\r\n", "\r\nPragma: no-cache\r\n",
		"\r\nCache-Control: no-cache\r\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("falta %q en:\n%s", want, got)
		}
	}
	if r.connID == r.corrID {
		t.Error("los dos identificadores son iguales")
	}
	if got := string(newUpgradeRequest("rds.corp", 8443).bytes("k", "x")); !strings.Contains(got, "\r\nHost: rds.corp:8443\r\n") {
		t.Errorf("Host con puerto:\n%s", got)
	}
}

func TestPingAnswered(t *testing.T) {
	g, cfg := newFakeGateway(t, &fakeGateway{ping: true})
	if _, err := connect(t, cfg, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if !g.ponged.Load() {
		t.Fatal("el cliente no contestó el ping")
	}
}

// Push rechazado: el gateway corta en vez de contestar la autorización.
func TestTunnelAuthDropped(t *testing.T) {
	_, cfg := newFakeGateway(t, &fakeGateway{dropAtAuth: true})
	if _, err := connect(t, cfg, 5*time.Second); kindOf(err) != KindNotAuthorized {
		t.Fatalf("err = %v", err)
	}
}

// Si el gateway espera el doble factor al crear el túnel, vencer el tiempo
// también es "no se aprobó a tiempo" (no "¿VPN conectada?").
func TestApprovalTimeoutAtCreate(t *testing.T) {
	_, cfg := newFakeGateway(t, &fakeGateway{createDelay: 3 * time.Second})
	if _, err := connect(t, cfg, time.Second); kindOf(err) != KindTimeout {
		t.Fatalf("err = %v", err)
	}
}

// Con la red trabada (el gateway no lee), cerrar el canal no se queda
// esperando a que termine una escritura bloqueada.
func TestCloseWithStuckWrite(t *testing.T) {
	stall := make(chan struct{})
	defer close(stall)
	_, cfg := newFakeGateway(t, &fakeGateway{stall: stall})
	tun, err := connect(t, cfg, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c, err := tun.Dial("tcp", "srv:3389")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		buf := make([]byte, 1<<20)
		for {
			if _, err := c.Write(buf); err != nil {
				return
			}
		}
	}()
	time.Sleep(500 * time.Millisecond) // que se llenen los búferes
	done := make(chan struct{})
	go func() { c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close quedó bloqueado")
	}
}

// Si la conexión con el gateway se corta después de establecerla (red
// inestable), el error lo dice, en vez de "¿VPN conectada?".
func TestConnectionLostDuringAuth(t *testing.T) {
	_, cfg := newFakeGateway(t, &fakeGateway{dropAtNTLM: true})
	_, err := connect(t, cfg, 5*time.Second)
	if kindOf(err) != KindConnectionLost {
		t.Fatalf("err = %v", err)
	}
}

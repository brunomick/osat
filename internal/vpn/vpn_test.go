package vpn

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/curve25519"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

func keyPair(t *testing.T) (priv, pub []byte) {
	t.Helper()
	priv = make([]byte, 32)
	if _, err := rand.Read(priv); err != nil {
		t.Fatal(err)
	}
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	return priv, pub
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func TestParse(t *testing.T) {
	priv, _ := keyPair(t)
	_, pub := keyPair(t)
	conf := fmt.Sprintf(`# Mullvad
[Interface]
PrivateKey = %s
Address = 10.64.1.2/32, fc00:bbbb::2/128
DNS = 10.64.0.1, search.example
MTU = 1380
PostUp = iptables -A ...

[Peer]
PublicKey = %s
AllowedIPs = 0.0.0.0/0, ::/0
Endpoint = ar1.example.net:51820 ; servidor
PersistentKeepalive = 15
`, b64(priv), b64(pub))
	c, err := Parse(conf)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Addresses) != 2 || c.Addresses[0].String() != "10.64.1.2/32" {
		t.Errorf("Addresses = %v", c.Addresses)
	}
	if len(c.DNS) != 1 || c.DNS[0].String() != "10.64.0.1" {
		t.Errorf("DNS = %v", c.DNS)
	}
	if c.MTU != 1380 {
		t.Errorf("MTU = %d", c.MTU)
	}
	p := c.Peers[0]
	if p.Endpoint != "ar1.example.net:51820" || p.Keepalive != 15 || len(p.AllowedIPs) != 2 {
		t.Errorf("Peer = %+v", p)
	}
}

func TestParseErrors(t *testing.T) {
	priv, pub := keyPair(t)
	ok := fmt.Sprintf("[Interface]\nPrivateKey = %s\nAddress = 10.0.0.2/32\n[Peer]\nPublicKey = %s\nEndpoint = 1.2.3.4:51820\n", b64(priv), b64(pub))
	if _, err := Parse(ok); err != nil {
		t.Fatalf("base: %v", err)
	}
	for name, conf := range map[string]string{
		"vacío":        "",
		"sin clave":    strings.Replace(ok, "PrivateKey = "+b64(priv)+"\n", "", 1),
		"clave mala":   strings.Replace(ok, b64(priv), "no-es-base64", 1),
		"sin address":  strings.Replace(ok, "Address = 10.0.0.2/32\n", "", 1),
		"sin peer":     ok[:strings.Index(ok, "[Peer]")],
		"sin puerto":   strings.Replace(ok, "1.2.3.4:51820", "1.2.3.4", 1),
		"línea suelta": ok + "basura\n",
	} {
		if _, err := Parse(conf); err == nil {
			t.Errorf("%s: Parse no dio error", name)
		}
	}
}

func TestPrivateKeyRoundTrip(t *testing.T) {
	conf := "[Interface]\r\nPrivateKey = ABC=\r\nAddress = 10.0.0.2/32\r\n[Peer]\r\nPublicKey = X\r\n"
	stripped := StripPrivateKey(conf)
	if strings.Contains(stripped, "ABC=") {
		t.Fatalf("la clave quedó en el texto: %q", stripped)
	}
	if PrivateKeyText(conf) != "ABC=" {
		t.Errorf("PrivateKeyText = %q", PrivateKeyText(conf))
	}
	back := WithPrivateKey(stripped, "ABC=")
	if PrivateKeyText(back) != "ABC=" || !strings.Contains(back, "Address = 10.0.0.2/32") {
		t.Errorf("WithPrivateKey = %q", back)
	}
}

// Con la VPN activada y el túnel caído, Dial falla: nunca sale directo.
func TestDialFailsClosed(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	priv, pub := keyPair(t)
	cfg := Config{
		PrivateKey: priv, MTU: defaultMTU,
		Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.0.2/32")},
		Peers:     []Peer{{PublicKey: pub, Endpoint: "no-existe.invalid:51820"}},
	}
	if err := Start(cfg); err == nil {
		t.Fatal("Start con un endpoint que no existe no dio error")
	}
	defer Stop()
	if s := Current(); s.State != StateError || s.Error == "" {
		t.Errorf("Current = %+v", s)
	}
	if _, err := DialTimeout("tcp", ln.Addr().String(), time.Second); !errors.Is(err, ErrDown) {
		t.Errorf("Dial con la VPN caída: err = %v, quería ErrDown", err)
	}

	Stop()
	c, err := DialTimeout("tcp", ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("Dial con la VPN apagada: %v", err)
	}
	c.Close()
	if s := Current(); s.State != StateOff {
		t.Errorf("Current = %+v", s)
	}
}

// De punta a punta: un "servidor" WireGuard en memoria con un servicio TCP
// dentro de su red; con la VPN activada, Dial llega a ese servicio, que ve la
// IP del túnel como origen.
func TestDialThroughTunnel(t *testing.T) {
	cliPriv, cliPub := keyPair(t)
	srvPriv, srvPub := keyPair(t)

	srvTun, srvNet, err := netstack.CreateNetTUN([]netip.Addr{netip.MustParseAddr("10.9.0.1")}, nil, 1420)
	if err != nil {
		t.Fatal(err)
	}
	srv := device.NewDevice(srvTun, conn.NewDefaultBind(), device.NewLogger(device.LogLevelSilent, ""))
	defer srv.Close()
	if err := srv.IpcSet(fmt.Sprintf("private_key=%s\nlisten_port=0\npublic_key=%s\nallowed_ip=10.9.0.2/32\n",
		hex.EncodeToString(srvPriv), hex.EncodeToString(cliPub))); err != nil {
		t.Fatal(err)
	}
	if err := srv.Up(); err != nil {
		t.Fatal(err)
	}
	port := listenPort(t, srv)

	ln, err := srvNet.ListenTCP(&net.TCPAddr{IP: net.ParseIP("10.9.0.1"), Port: 2222})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		host, _, _ := net.SplitHostPort(c.RemoteAddr().String())
		io.WriteString(c, "hola "+host)
	}()

	err = Start(Config{
		PrivateKey: cliPriv, MTU: 1420,
		Addresses: []netip.Prefix{netip.MustParsePrefix("10.9.0.2/32")},
		Peers: []Peer{{
			PublicKey:  srvPub,
			Endpoint:   fmt.Sprintf("127.0.0.1:%d", port),
			AllowedIPs: []netip.Prefix{netip.MustParsePrefix("10.9.0.0/24")},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Dial(ctx, "tcp", "10.9.0.1:2222")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	b, _ := io.ReadAll(c)
	if string(b) != "hola 10.9.0.2" {
		t.Errorf("respuesta = %q", b)
	}
	if s := Current(); s.State != StateOn || s.Address != "10.9.0.2" {
		t.Errorf("Current = %+v", s)
	}
}

func listenPort(t *testing.T, d *device.Device) int {
	t.Helper()
	s, err := d.IpcGet()
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(s, "\n") {
		if v, ok := strings.CutPrefix(l, "listen_port="); ok {
			var p int
			fmt.Sscan(v, &p)
			return p
		}
	}
	t.Fatal("sin listen_port")
	return 0
}

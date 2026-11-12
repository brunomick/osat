package portfwd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// socksRig es un proxy SOCKS con un dialAddr falso que anota los destinos y
// conecta siempre a un eco en mayúsculas (o falla con err).
type socksRig struct {
	l    *Listener
	mu   sync.Mutex
	got  []string
	fail error
}

func newSocksRig(t *testing.T) *socksRig {
	t.Helper()
	echo, stop := echoServer(t)
	t.Cleanup(stop)
	r := &socksRig{}
	l, err := ListenSOCKS(0, func(ctx context.Context, addr string) (net.Conn, error) {
		r.mu.Lock()
		r.got = append(r.got, addr)
		fail := r.fail
		r.mu.Unlock()
		if fail != nil {
			return nil, fail
		}
		return dialTo(echo)(ctx)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(l.Close)
	r.l = l
	return r
}

func (r *socksRig) dialed() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.got...)
}

func (r *socksRig) conn(t *testing.T) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(r.l.Port())), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(3 * time.Second))
	t.Cleanup(func() { c.Close() })
	return c
}

// greet manda el saludo con los métodos dados y devuelve la respuesta.
func greet(t *testing.T, c net.Conn, methods ...byte) []byte {
	t.Helper()
	c.Write(append([]byte{5, byte(len(methods))}, methods...))
	resp := make([]byte, 2)
	if _, err := io.ReadFull(c, resp); err != nil {
		t.Fatalf("respuesta al saludo: %v", err)
	}
	return resp
}

// request manda un pedido y devuelve los 10 bytes de la respuesta.
func request(t *testing.T, c net.Conn, cmd, atyp byte, addr []byte, port int) []byte {
	t.Helper()
	req := append([]byte{5, cmd, 0, atyp}, addr...)
	c.Write(append(req, byte(port>>8), byte(port)))
	resp := make([]byte, 10)
	if _, err := io.ReadFull(c, resp); err != nil {
		t.Fatalf("respuesta al pedido: %v", err)
	}
	return resp
}

var socksOKReply = []byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}

func TestSOCKSConnectDestinations(t *testing.T) {
	r := newSocksRig(t)
	cases := []struct {
		name string
		atyp byte
		addr []byte
		port int
		want string
	}{
		{"IPv4", 1, []byte{10, 1, 2, 3}, 5432, "10.1.2.3:5432"},
		{"dominio", 3, append([]byte{11}, "example.com"...), 443, "example.com:443"},
		{"IPv6", 4, net.ParseIP("2001:db8::1").To16(), 8080, "[2001:db8::1]:8080"},
	}
	for _, tc := range cases {
		c := r.conn(t)
		if got := greet(t, c, 0x00); !bytes.Equal(got, []byte{5, 0}) {
			t.Fatalf("%s: saludo = %v", tc.name, got)
		}
		if got := request(t, c, 1, tc.atyp, tc.addr, tc.port); !bytes.Equal(got, socksOKReply) {
			t.Fatalf("%s: respuesta = %v", tc.name, got)
		}
		// Después de la respuesta, el túnel pasa los datos.
		c.Write([]byte("hola " + tc.name))
		c.(*net.TCPConn).CloseWrite()
		if b, _ := io.ReadAll(c); string(b) != strings.ToUpper("hola "+tc.name) {
			t.Fatalf("%s: datos = %q", tc.name, b)
		}
		got := r.dialed()
		if last := got[len(got)-1]; last != tc.want {
			t.Fatalf("%s: dialAddr recibió %q, quería %q", tc.name, last, tc.want)
		}
	}
	waitIdle(t, r.l)
	s := r.l.Stats()
	if s.Total != 3 || s.BytesOut == 0 || s.BytesOut != s.BytesIn || s.LastErr != "" {
		t.Fatalf("stats: %+v", s)
	}
}

func waitIdle(t *testing.T, l *Listener) {
	t.Helper()
	for end := time.Now().Add(2 * time.Second); l.Stats().Active != 0 && time.Now().Before(end); {
		time.Sleep(10 * time.Millisecond)
	}
}

// Los datos que el programa manda pegados al pedido (sin esperar la respuesta) no se pierden.
func TestSOCKSKeepsEarlyData(t *testing.T) {
	r := newSocksRig(t)
	c := r.conn(t)
	c.Write([]byte{5, 1, 0})
	c.Write(append([]byte{5, 1, 0, 1, 1, 2, 3, 4, 0, 80}, "pegado"...))
	c.(*net.TCPConn).CloseWrite()
	b, _ := io.ReadAll(c)
	if want := append(append([]byte{5, 0}, socksOKReply...), "PEGADO"...); !bytes.Equal(b, want) {
		t.Fatalf("salió %q, quería %q", b, want)
	}
}

func TestSOCKSNoAcceptableMethod(t *testing.T) {
	r := newSocksRig(t)
	c := r.conn(t)
	if got := greet(t, c, 0x02); !bytes.Equal(got, []byte{5, 0xFF}) { // solo usuario/contraseña
		t.Fatalf("saludo = %v, quería 05 FF", got)
	}
	if _, err := c.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("la conexión tenía que cerrarse: %v", err)
	}
	if len(r.dialed()) != 0 {
		t.Fatal("no debería haber conectado a nada")
	}
}

func TestSOCKSUnsupportedCommandAndAddressType(t *testing.T) {
	r := newSocksRig(t)
	for _, cmd := range []byte{2 /* BIND */, 3 /* UDP */} {
		c := r.conn(t)
		greet(t, c, 0x00)
		if got := request(t, c, cmd, 1, []byte{1, 2, 3, 4}, 80); got[1] != 0x07 {
			t.Errorf("CMD=%d: respuesta = %v, quería 07", cmd, got)
		}
	}
	c := r.conn(t)
	greet(t, c, 0x00)
	if got := request(t, c, 1, 9, []byte{1, 2, 3, 4}, 80); got[1] != 0x08 {
		t.Errorf("ATYP=9: respuesta = %v, quería 08", got)
	}
	if len(r.dialed()) != 0 {
		t.Fatalf("no debería haber conectado a nada: %q", r.dialed())
	}
}

func TestSOCKSDialFailures(t *testing.T) {
	r := newSocksRig(t)
	// El servidor SSH no pudo abrir el canal: rechazada (05).
	r.fail = Refused(errors.New("no hay nada escuchando en 1.2.3.4:80"))
	c := r.conn(t)
	greet(t, c, 0x00)
	if got := request(t, c, 1, 1, []byte{1, 2, 3, 4}, 80); got[1] != 0x05 {
		t.Fatalf("respuesta = %v, quería 05", got)
	}
	if s := r.l.Stats(); !strings.Contains(s.LastErr, "no hay nada escuchando") {
		t.Fatalf("debería contar por qué falló: %+v", s)
	}
	// Cualquier otro error: fallo general (01).
	r.fail = errors.New("el servidor no contestó a tiempo")
	c = r.conn(t)
	greet(t, c, 0x00)
	if got := request(t, c, 1, 3, append([]byte{3}, "a.b"...), 80); got[1] != 0x01 {
		t.Fatalf("respuesta = %v, quería 01", got)
	}
}

// Un cliente que no termina el saludo no se queda ocupando la conexión.
func TestSOCKSHandshakeTimeout(t *testing.T) {
	orig := socksHandshakeTimeout
	socksHandshakeTimeout = 100 * time.Millisecond
	t.Cleanup(func() { socksHandshakeTimeout = orig })
	r := newSocksRig(t)
	c := r.conn(t)
	c.Write([]byte{5}) // y nada más
	start := time.Now()
	if _, err := c.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("la conexión tenía que cerrarse: %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("tardó demasiado en cortar")
	}
}

func TestSOCKSNotSOCKS5(t *testing.T) {
	r := newSocksRig(t)
	c := r.conn(t)
	c.Write([]byte("GET / HTTP/1.1\r\n\r\n"))
	if b, _ := io.ReadAll(c); len(b) != 0 {
		t.Fatalf("a un cliente que no habla SOCKS5 se le cierra sin contestar: %q", b)
	}
}

func TestSOCKSListensOnlyOnLoopback(t *testing.T) {
	r := newSocksRig(t)
	for _, ln := range r.l.lns {
		if !ln.Addr().(*net.TCPAddr).IP.IsLoopback() {
			t.Fatalf("escucha fuera del loopback: %s", ln.Addr())
		}
	}
}

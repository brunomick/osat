package remote

import (
	"bytes"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"osat/internal/portfwd"
	"osat/internal/store"
)

// socksConnect habla SOCKS5 a mano con el proxy y devuelve la conexión ya
// abierta (si el destino contestó) y el código de respuesta.
func socksConnect(t *testing.T, proxyPort int, host string, port int) (net.Conn, byte) {
	t.Helper()
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(proxyPort)), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	c.SetDeadline(time.Now().Add(3 * time.Second))
	c.Write([]byte{5, 1, 0})
	if hello := readFull(t, c, make([]byte, 2)); !bytes.Equal(hello, []byte{5, 0}) {
		t.Fatalf("saludo = %v", hello)
	}
	req := append([]byte{5, 1, 0, 3, byte(len(host))}, host...)
	c.Write(append(req, byte(port>>8), byte(port)))
	return c, readFull(t, c, make([]byte, 10))[1]
}

func readFull(t *testing.T, c net.Conn, b []byte) []byte {
	t.Helper()
	if _, err := io.ReadFull(c, b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDynamicForwardSOCKS(t *testing.T) {
	backend := echoBackend(t)
	m, sh := forwardRig(t, nil)
	local := portfwd.FreePort(22000)

	rule := store.Forward{ID: "socks", ConnID: "c", Kind: store.ForwardDynamic, LocalPort: local}
	if err := m.StartForward("tab", "srv", rule); err != nil {
		t.Fatal(err)
	}
	if sh.refs != 2 {
		t.Fatalf("el reenvío toma una referencia de la conexión: refs=%d", sh.refs)
	}

	// Un nombre: lo resuelve el "servidor" (acá, localhost) y no esta PC.
	c, code := socksConnect(t, local, "localhost", backend)
	if code != 0 {
		t.Fatalf("respuesta = %d", code)
	}
	c.Write([]byte("por el servidor"))
	c.(*net.TCPConn).CloseWrite()
	if b, _ := io.ReadAll(c); string(b) != "POR EL SERVIDOR" {
		t.Fatalf("datos = %q", b)
	}

	waitFor(t, "estadísticas", func() bool { s, _ := stateOf(m, "socks"); return s.Total == 1 && s.Active == 0 })
	s, _ := stateOf(m, "socks")
	n := int64(len("por el servidor"))
	if s.Status != FwRunning || s.LocalPort != local || s.BytesOut != n || s.BytesIn != n || s.Warning != "" || s.Error != "" {
		t.Fatalf("estado: %+v", s)
	}

	// Un destino al que el servidor no llega: respuesta "rechazada" y aviso en la fila.
	if _, code := socksConnect(t, local, "127.0.0.1", 1); code != 0x05 {
		t.Fatalf("respuesta = %d, quería 5 (rechazada)", code)
	}
	waitFor(t, "aviso", func() bool { s, _ := stateOf(m, "socks"); return s.Warning != "" })
	if s, _ := stateOf(m, "socks"); s.Status != FwRunning || !strings.Contains(s.Warning, "no hay nada escuchando en 127.0.0.1:1") {
		t.Fatalf("estado: %+v", s)
	}

	// Otro reenvío con el mismo puerto de esta PC: se dice cuál.
	other := store.Forward{ID: "x", ConnID: "c", LocalPort: local, Host: "127.0.0.1", Port: backend}
	if err := m.StartForward("tab", "srv", other); err == nil || !strings.Contains(err.Error(), "ya lo usa") {
		t.Fatalf("error: %v", err)
	}

	m.StopForward("socks")
	if sh.refs != 1 {
		t.Fatalf("al detenerlo se suelta la conexión: refs=%d", sh.refs)
	}
	if _, ok := stateOf(m, "socks"); ok {
		t.Fatal("un reenvío detenido no figura")
	}
	if _, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(local)), time.Second); err == nil {
		t.Fatal("el puerto debería estar libre")
	}
}

func TestDynamicForwardPortInUse(t *testing.T) {
	m, _ := forwardRig(t, nil)
	local := portfwd.FreePort(22100)
	taken, err := portfwd.Listen(local, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	err = m.StartForward("tab", "srv", store.Forward{ID: "socks", ConnID: "c", Kind: store.ForwardDynamic, LocalPort: local})
	if err == nil || !strings.Contains(err.Error(), "otro programa") {
		t.Fatalf("error: %v", err)
	}
	if s, ok := stateOf(m, "socks"); !ok || s.Status != FwError {
		t.Fatalf("el motivo tiene que verse en el reenvío: %+v", s)
	}
}

func TestDynamicForwardEndsWhenTheServerConnectionDrops(t *testing.T) {
	m, sh := forwardRig(t, nil)
	local := portfwd.FreePort(22200)
	if err := m.StartForward("tab", "srv", store.Forward{ID: "socks", ConnID: "c", Kind: store.ForwardDynamic, LocalPort: local}); err != nil {
		t.Fatal(err)
	}
	sh.client.Close()
	waitFor(t, "estado de error", func() bool { s, _ := stateOf(m, "socks"); return s.Status == FwError })
	if s, _ := stateOf(m, "socks"); !strings.Contains(s.Error, "se cortó la conexión") {
		t.Fatalf("estado: %+v", s)
	}
}

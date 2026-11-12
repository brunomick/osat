package remote

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh/agent"

	"osat/internal/store"
)

const (
	noAgentNotice     = "No se reenvió el agente: no hay un agente SSH en esta PC."
	agentDeniedNotice = "No se reenvió el agente: el servidor no lo permitió (revisá AllowAgentForwarding en sshd_config)."
)

// fakeLocalAgent reemplaza el agente de esta PC por uno en memoria con una
// clave "clave-de-la-pc". calls cuenta las veces que se conectó a él.
func fakeLocalAgent(t *testing.T, available bool) *atomic.Int32 {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	kr := agent.NewKeyring()
	if err := kr.Add(agent.AddedKey{PrivateKey: priv, Comment: "clave-de-la-pc"}); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	origNew, origAvail := newAgent, agentAvailable
	t.Cleanup(func() { newAgent, agentAvailable = origNew, origAvail })
	newAgent = func() (agent.Agent, net.Conn, error) { calls.Add(1); return kr, nil, nil }
	agentAvailable = func() bool { return available }
	return &calls
}

// waitAgent espera hasta que el servidor haya listado n veces el agente.
func waitAgent(log *shellLog, n int) []string {
	deadline := time.Now().Add(3 * time.Second)
	for len(log.agent()) < n && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	return log.agent()
}

const ubuntuVersion = "SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13"

func TestAgentForwarding(t *testing.T) {
	fakeLocalAgent(t, true)
	client, log := fakeShell(t, ubuntuVersion, nil)
	got := terminalOutputFor(t, client, store.Connection{ID: "c", ForwardAgent: true}, func(s string) bool { return strings.Contains(s, "new$ ") })
	if got != clearScreen+"new$ " {
		t.Fatalf("salida = %q", got)
	}
	if keys := waitAgent(log, 1); !reflect.DeepEqual(keys, []string{"clave-de-la-pc"}) {
		t.Fatalf("el servidor vio %q en el agente", keys)
	}
	if reqs, _ := log.get(); !reflect.DeepEqual(reqs, []string{"auth-agent-req@openssh.com", "pty-req", "shell"}) {
		t.Fatalf("pedidos = %q", reqs)
	}
}

func TestAgentForwardingOff(t *testing.T) {
	calls := fakeLocalAgent(t, true)
	client, log := fakeShell(t, ubuntuVersion, nil)
	terminalOutputFor(t, client, store.Connection{ID: "c"}, func(s string) bool { return strings.Contains(s, "new$ ") })
	time.Sleep(100 * time.Millisecond)
	if reqs, _ := log.get(); !reflect.DeepEqual(reqs, []string{"pty-req", "shell"}) {
		t.Fatalf("sin la opción no se pide el reenvío: pedidos = %q", reqs)
	}
	if calls.Load() != 0 || len(log.agent()) != 0 {
		t.Fatalf("se usó el agente sin la opción: %d conexiones, %q", calls.Load(), log.agent())
	}
}

func TestAgentForwardingWithoutLocalAgent(t *testing.T) {
	fakeLocalAgent(t, false)
	client, log := fakeShell(t, ubuntuVersion, nil)
	got := terminalOutputFor(t, client, store.Connection{ID: "c", ForwardAgent: true}, func(s string) bool { return strings.Contains(s, "new$ ") })
	if want := clearScreen + string(grayNotice(noAgentNotice)) + "new$ "; got != want {
		t.Fatalf("salida = %q, quería %q", got, want)
	}
	if reqs, _ := log.get(); !reflect.DeepEqual(reqs, []string{"pty-req", "shell"}) {
		t.Fatalf("sin agente no se pide el reenvío: pedidos = %q", reqs)
	}
}

// Sin arranque limpio (Windows) el aviso también se ve.
func TestAgentForwardingWithoutLocalAgentOnWindows(t *testing.T) {
	fakeLocalAgent(t, false)
	client, _ := fakeShell(t, "SSH-2.0-OpenSSH_for_Windows_9.5", nil)
	got := terminalOutputFor(t, client, store.Connection{ID: "c", ForwardAgent: true}, func(s string) bool { return strings.Contains(s, "$ ") })
	if want := string(grayNotice(noAgentNotice)) + ubuntuBanner; got != want {
		t.Fatalf("salida = %q, quería %q", got, want)
	}
}

func TestAgentForwardingDeniedByServer(t *testing.T) {
	fakeLocalAgent(t, true)
	client, log := fakeShell(t, ubuntuVersion, map[string]bool{"auth-agent-req@openssh.com": true})
	got := terminalOutputFor(t, client, store.Connection{ID: "c", ForwardAgent: true}, func(s string) bool { return strings.Contains(s, "new$ ") })
	if want := clearScreen + string(grayNotice(agentDeniedNotice)) + "new$ "; got != want {
		t.Fatalf("salida = %q, quería %q", got, want)
	}
	if len(log.agent()) != 0 {
		t.Fatalf("el servidor no debería haber usado el agente: %q", log.agent())
	}
}

// Dos terminales sobre la misma conexión (Duplicar pestaña): el manejador se
// registra una sola vez y cada canal del agente conecta al agente local en ese
// momento.
func TestAgentForwardingTwoTerminalsOneConnection(t *testing.T) {
	calls := fakeLocalAgent(t, true)
	client, log := fakeShell(t, ubuntuVersion, nil)
	m := NewManager(nil, nil, func(string, ...interface{}) {})
	sh := newSharedClient(client)
	conn := store.Connection{ID: "c", ForwardAgent: true}
	s1, err := m.startShell(sh, conn, 80, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close(s1.ID) })
	if err := sh.acquire(); err != nil {
		t.Fatal(err)
	}
	s2, err := m.startShell(sh, conn, 80, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close(s2.ID) })
	if keys := waitAgent(log, 2); !reflect.DeepEqual(keys, []string{"clave-de-la-pc", "clave-de-la-pc"}) {
		t.Fatalf("el servidor vio %q", keys)
	}
	if calls.Load() != 2 {
		t.Fatalf("conexiones al agente local = %d, quería una por canal (2)", calls.Load())
	}
}

// Si al abrir el canal el agente local ya no está, el servidor recibe un
// rechazo y la terminal sigue.
func TestAgentForwardingAgentGoneLater(t *testing.T) {
	fakeLocalAgent(t, true)
	newAgent = func() (agent.Agent, net.Conn, error) { return nil, nil, net.ErrClosed }
	client, log := fakeShell(t, ubuntuVersion, nil)
	got := terminalOutputFor(t, client, store.Connection{ID: "c", ForwardAgent: true}, func(s string) bool { return strings.Contains(s, "new$ ") })
	if got != clearScreen+"new$ " {
		t.Fatalf("salida = %q", got)
	}
	if keys := waitAgent(log, 1); len(keys) != 1 || !strings.HasPrefix(keys[0], "error: ") {
		t.Fatalf("el servidor debería ver el canal rechazado: %q", keys)
	}
}

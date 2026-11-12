package remote

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"osat/internal/store"
)

const ubuntuBanner = "Welcome to Ubuntu 26.04 LTS\r\n\r\n * Documentation:  https://docs.ubuntu.com\r\n\r\n" +
	"Last login: Tue Oct 27 21:36:01 2026 from 186.108.224.11\r\nubuntu@ip-172-31-24-103:~$ "

// fakeShellClient es un servidor que, al pedirle una shell, imprime banner y,
// cuando recibe una línea (terminada en \r), hace como si la ejecutara: eco de
// lo tipeado, la marca de arranque y un prompt nuevo "new$ ".
func fakeShellClient(t *testing.T, version string) *ssh.Client {
	t.Helper()
	c, _ := fakeShell(t, version, nil)
	return c
}

// shellLog es lo que recibió el servidor falso: los pedidos de la sesión en
// orden ("pty-req", "env NOMBRE=valor", "shell") y las líneas tipeadas. Con
// el reenvío del agente, agentKeys son los comentarios de las claves que el
// servidor vio en el agente reenviado (o el error, como "error: …").
type shellLog struct {
	mu        sync.Mutex
	reqs      []string
	lines     []string
	agentKeys []string
}

func (l *shellLog) agent() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.agentKeys...)
}

// listForwardedAgent hace lo que haría ssh-add -l en el servidor: abre un
// canal del agente hacia el cliente y lista las claves.
func (l *shellLog) listForwardedAgent(conn *ssh.ServerConn) {
	var got []string
	ch, reqs, err := conn.OpenChannel("auth-agent@openssh.com", nil)
	if err == nil {
		go ssh.DiscardRequests(reqs)
		var keys []*agent.Key
		keys, err = agent.NewClient(ch).List()
		ch.Close()
		for _, k := range keys {
			got = append(got, k.Comment)
		}
	}
	if err != nil {
		got = []string{"error: " + err.Error()}
	}
	l.mu.Lock()
	l.agentKeys = append(l.agentKeys, got...)
	l.mu.Unlock()
}

func (l *shellLog) get() (reqs, lines []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.reqs...), append([]string(nil), l.lines...)
}

// fakeShell es fakeShellClient, pero además registra lo recibido y acepta las
// variables de entorno salvo las de reject. Acepta el reenvío del agente (salvo
// reject["auth-agent-req@openssh.com"]) y en ese caso lista sus claves.
func fakeShell(t *testing.T, version string, reject map[string]bool) (*ssh.Client, *shellLog) {
	t.Helper()
	log := &shellLog{}
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{NoClientAuth: true, ServerVersion: version}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		nc, err := ln.Accept()
		if err != nil {
			return
		}
		conn, chans, reqs, err := ssh.NewServerConn(nc, cfg)
		if err != nil {
			return
		}
		go ssh.DiscardRequests(reqs)
		for nch := range chans {
			ch, creqs, err := nch.Accept()
			if err != nil {
				return
			}
			go func() {
				for req := range creqs {
					entry, ok := req.Type, req.Type == "pty-req" || req.Type == "shell"
					if req.Type == "env" {
						var e struct{ Name, Value string }
						ssh.Unmarshal(req.Payload, &e)
						entry, ok = "env "+e.Name+"="+e.Value, !reject[e.Name]
					}
					if req.Type == "auth-agent-req@openssh.com" {
						ok = !reject[req.Type]
					}
					log.mu.Lock()
					log.reqs = append(log.reqs, entry)
					log.mu.Unlock()
					req.Reply(ok, nil)
					if ok && req.Type == "auth-agent-req@openssh.com" {
						go log.listForwardedAgent(conn)
					}
					if req.Type != "shell" {
						continue
					}
					go func() {
						ch.Write([]byte(ubuntuBanner))
						var line []byte
						b := make([]byte, 1)
						for {
							if _, err := ch.Read(b); err != nil {
								return
							}
							if b[0] != '\r' {
								line = append(line, b[0])
								continue
							}
							log.mu.Lock()
							log.lines = append(log.lines, string(line))
							log.mu.Unlock()
							ch.Write(append(append(line, "\r\n"...), cleanStartMarker...))
							ch.Write([]byte("new$ "))
							line = nil
						}
					}()
				}
			}()
		}
	}()
	nc, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	sc, chans, reqs, err := ssh.NewClientConn(nc, ln.Addr().String(), &ssh.ClientConfig{User: "t", HostKeyCallback: ssh.InsecureIgnoreHostKey()})
	if err != nil {
		t.Fatal(err)
	}
	c := ssh.NewClient(sc, chans, reqs)
	t.Cleanup(func() { c.Close() })
	return c, log
}

// terminalOutput abre una terminal contra el servidor falso y espera hasta que
// la salida cumpla ok (o 3 s). Devuelve todo lo que recibió el frontend.
func terminalOutput(t *testing.T, version string, ok func(string) bool) string {
	t.Helper()
	return terminalOutputFor(t, fakeShellClient(t, version), store.Connection{ID: "c"}, ok)
}

// terminalOutputFor es terminalOutput con un cliente y una conexión dados.
func terminalOutputFor(t *testing.T, client *ssh.Client, conn store.Connection, ok func(string) bool) string {
	t.Helper()
	return terminalOutputWith(t, client, conn, false, ok)
}

// terminalOutputWith es terminalOutputFor, abriendo la shell como reconexión o no.
func terminalOutputWith(t *testing.T, client *ssh.Client, conn store.Connection, reconnect bool, ok func(string) bool) string {
	t.Helper()
	var mu sync.Mutex
	var out strings.Builder
	m := NewManager(nil, nil, func(event string, data ...interface{}) {
		if !strings.HasPrefix(event, "term:data:") {
			return
		}
		b, err := base64.StdEncoding.DecodeString(data[0].(string))
		if err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		out.Write(b)
		mu.Unlock()
	})
	sh := newSharedClient(client)
	s, err := m.startShell(sh, conn, 80, 24, reconnect)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close(s.ID) })
	if err := m.Attach(s.ID); err != nil {
		t.Fatal(err)
	}
	get := func() string { mu.Lock(); defer mu.Unlock(); return out.String() }
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline) && !ok(get()); {
		time.Sleep(10 * time.Millisecond)
	}
	return get()
}

func TestStartShellHidesTheBanner(t *testing.T) {
	got := terminalOutput(t, "SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13", func(s string) bool { return strings.Contains(s, "new$ ") })
	if want := clearScreen + "new$ "; got != want {
		t.Fatalf("la terminal debería arrancar limpia: salió %q, quería %q", got, want)
	}
}

// Al reconectar, el arranque limpio sigue escondiendo el banner pero no borra
// la pantalla ni el historial: queda lo que había antes del corte.
func TestStartShellOnReconnectKeepsTheScreen(t *testing.T) {
	client, log := fakeShell(t, "SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13", nil)
	got := terminalOutputWith(t, client, store.Connection{ID: "c"}, true, func(s string) bool { return strings.Contains(s, "new$ ") })
	if got != "new$ " {
		t.Fatalf("al reconectar no se borra nada ni se ve el banner: salió %q", got)
	}
	// El prompt común se fija igual que en una terminal nueva.
	if _, lines := log.get(); len(lines) != 1 || lines[0] != strings.TrimSuffix(cleanStartInput, "\r") {
		t.Fatalf("líneas tipeadas = %q", lines)
	}
}

func TestStartShellOnWindowsLeavesTheOutputAlone(t *testing.T) {
	got := terminalOutput(t, "SSH-2.0-OpenSSH_for_Windows_9.5", func(s string) bool { return strings.Contains(s, "$ ") })
	if got != ubuntuBanner {
		t.Fatalf("en Windows no se toca la salida: salió %q", got)
	}
}

// R3: las variables van antes de la shell, la rechazada se avisa en gris
// entre el borrado y el prompt, y el comando inicial se escribe una sola vez,
// después de la marca de arranque.
func TestStartShellInitialCommandAndEnv(t *testing.T) {
	client, log := fakeShell(t, "SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13", map[string]bool{"FOO": true})
	conn := store.Connection{ID: "c", InitialCommand: "uptime", Env: []string{"LANG=es_AR.UTF-8", "FOO=a=b"}}
	got := terminalOutputFor(t, client, conn, func(s string) bool { return strings.Count(s, "new$ ") == 2 })

	notice := string(grayNotice("El servidor no aceptó la variable FOO (revisá AcceptEnv en sshd_config)."))
	want := clearScreen + notice + "new$ " + "uptime\r\n" + string(cleanStartMarker) + "new$ "
	if got != want {
		t.Fatalf("salió %q,\nquería %q", got, want)
	}

	time.Sleep(100 * time.Millisecond) // por si el comando llegara dos veces
	reqs, lines := log.get()
	if want := []string{strings.TrimSuffix(cleanStartInput, "\r"), "uptime"}; !reflect.DeepEqual(lines, want) {
		t.Fatalf("líneas tipeadas = %q, quería %q", lines, want)
	}
	if want := []string{"pty-req", "env LANG=es_AR.UTF-8", "env FOO=a=b", "shell"}; !reflect.DeepEqual(reqs, want) {
		t.Fatalf("pedidos = %q, quería %q", reqs, want)
	}
}

// Sin arranque limpio (Windows) el comando se escribe enseguida y los avisos
// salen primero, recién cuando el frontend escucha.
func TestStartShellInitialCommandOnWindows(t *testing.T) {
	client, log := fakeShell(t, "SSH-2.0-OpenSSH_for_Windows_9.5", map[string]bool{"FOO": true})
	conn := store.Connection{ID: "c", InitialCommand: "dir", Env: []string{"FOO=1"}}
	got := terminalOutputFor(t, client, conn, func(s string) bool { return strings.Contains(s, "new$ ") })

	notice := string(grayNotice("El servidor no aceptó la variable FOO (revisá AcceptEnv en sshd_config)."))
	want := notice + ubuntuBanner + "dir\r\n" + string(cleanStartMarker) + "new$ "
	if got != want {
		t.Fatalf("salió %q,\nquería %q", got, want)
	}
	time.Sleep(100 * time.Millisecond)
	if _, lines := log.get(); !reflect.DeepEqual(lines, []string{"dir"}) {
		t.Fatalf("líneas tipeadas = %q", lines)
	}
}

// Sin comando ni variables no se manda nada de más.
func TestStartShellWithoutInitialCommand(t *testing.T) {
	client, log := fakeShell(t, "SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13", nil)
	terminalOutputFor(t, client, store.Connection{ID: "c"}, func(s string) bool { return strings.Contains(s, "new$ ") })
	time.Sleep(100 * time.Millisecond)
	reqs, lines := log.get()
	if !reflect.DeepEqual(reqs, []string{"pty-req", "shell"}) || len(lines) != 1 {
		t.Fatalf("pedidos = %q, líneas = %q", reqs, lines)
	}
}

package remote

import (
	"bufio"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// Prueba contra un servidor real con tmux. Se corre a mano:
//
//	OSAT_TMUX_SSH=usuario@127.0.0.1:2222 OSAT_TMUX_KEY=ruta/a/la/clave go test ./internal/remote -run TmuxIntegration -v
func tmuxTestClient(t *testing.T) *ssh.Client {
	target, keyPath := os.Getenv("OSAT_TMUX_SSH"), os.Getenv("OSAT_TMUX_KEY")
	if target == "" || keyPath == "" {
		t.Skip("OSAT_TMUX_SSH / OSAT_TMUX_KEY sin definir")
	}
	userName, addr, _ := strings.Cut(target, "@")
	key, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	c, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{User: userName, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// attachCtl conecta un tmuxCtl a la sesión, como startTmux pero sin Manager.
func attachCtl(t *testing.T, c *ssh.Client, id string) (*tmuxCtl, func() string, func()) {
	sess, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	stdin, _ := sess.StdinPipe()
	stdout, _ := sess.StdoutPipe()
	if err := sess.Start("tmux -C attach-session -t " + shellQuote(id)); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var out strings.Builder
	ctl := newTmuxCtl(stdin, id, false, func(b []byte) { mu.Lock(); out.Write(b); mu.Unlock() }, nil)
	go func() {
		r := bufio.NewReader(stdout)
		for {
			l, err := r.ReadBytes('\n')
			if len(l) > 0 {
				ctl.line([]byte(strings.TrimSuffix(string(l), "\n")))
			}
			if err != nil {
				return
			}
		}
	}()
	ctl.start(80, 24)
	get := func() string { mu.Lock(); defer mu.Unlock(); return out.String() }
	return ctl, get, func() { sess.Close() }
}

func waitOutput(t *testing.T, get func() string, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(get(), want) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no apareció %q en:\n%q", want, get())
}

func TestTmuxIntegration(t *testing.T) {
	c := tmuxTestClient(t)
	out, err := tmuxExec(c, tmuxCreateScript("osat prueba", nil, true))
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSpace(out)
	if !strings.HasPrefix(id, "$") {
		t.Fatalf("crear: %q", out)
	}
	t.Cleanup(func() { _, _ = tmuxExec(c, tmuxKillScript(id)) })

	ctl, get, closeConn := attachCtl(t, c, id)
	time.Sleep(500 * time.Millisecond)
	ctl.write([]byte("for i in $(seq 1 300); do echo fila-$i; done\r"))
	waitOutput(t, get, "fila-300")
	if n := strings.Count(get(), "fila-"); n < 300 {
		t.Fatalf("se perdieron líneas: %d", n)
	}
	closeConn() // como cerrar la pestaña

	l, err := tmuxList(c)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range l.Sessions {
		found = found || s.ID == id
	}
	if !found {
		t.Fatalf("la sesión no sobrevivió al cierre: %+v", l)
	}

	// Al volver, el historial aparece en la captura.
	_, get2, closeConn2 := attachCtl(t, c, id)
	defer closeConn2()
	waitOutput(t, get2, "fila-150")

	name, err := tmuxExec(c, tmuxRenameScript(id, "nuevo.nombre"))
	if err != nil || strings.TrimSpace(name) != "nuevo_nombre" {
		t.Fatalf("renombrar: %q %v", name, err)
	}
}

package remote

import (
	"encoding/base64"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// Una terminal de este equipo usa las mismas llamadas y eventos que las de SSH.
func TestOpenLocalTerminal(t *testing.T) {
	// La shell, un comando y lo que imprime (no su eco), según el sistema.
	shell, cmd, want := "cmd", "echo osat-%OS%\r\n", "osat-Windows_NT"
	if runtime.GOOS != "windows" {
		shell, cmd, want = "sh", "echo osat-$((1+1))\r", "osat-2"
	}
	var mu sync.Mutex
	var out strings.Builder
	exited := make(chan string, 1)
	m := NewManager(nil, nil, func(event string, data ...interface{}) {
		switch {
		case strings.HasPrefix(event, "term:data:"):
			b, _ := base64.StdEncoding.DecodeString(data[0].(string))
			mu.Lock()
			out.Write(b)
			mu.Unlock()
		case strings.HasPrefix(event, "term:exit:"):
			exited <- event
		}
	})
	have := false
	for _, s := range m.LocalShells() {
		have = have || s.ID == shell
	}
	if !have {
		t.Skipf("no hay %s en este equipo", shell)
	}

	info, err := m.OpenLocalTerminal(shell, "", 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	if info.Kind != KindTerminal || info.ConnID != LocalConnID || info.Title != shell {
		t.Fatalf("info inesperada: %+v", info)
	}
	if err := m.Attach(info.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Resize(info.ID, 90, 25); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if err := m.Write(info.ID, cmd); err != nil {
		t.Fatal(err)
	}
	get := func() string { mu.Lock(); defer mu.Unlock(); return out.String() }
	for deadline := time.Now().Add(15 * time.Second); !strings.Contains(get(), want); {
		if time.Now().After(deadline) {
			t.Fatalf("no llegó la salida del comando: %q", get())
		}
		time.Sleep(25 * time.Millisecond)
	}

	// Al salir de la shell se avisa y la sesión desaparece.
	m.Write(info.ID, "exit\r\n")
	select {
	case ev := <-exited:
		if ev != "term:exit:"+info.ID {
			t.Fatalf("evento %q", ev)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no llegó term:exit")
	}
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := m.get(info.ID); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("la sesión debería haberse cerrado")
		}
	}
}

func TestLocalSessionsCannotBeReusedAsRemote(t *testing.T) {
	m := NewManager(nil, nil, func(string, ...interface{}) {})
	s := &Session{ID: "x", Kind: KindTerminal, ConnID: LocalConnID}
	m.register(s)
	if _, err := m.OpenSFTPFrom("x"); err == nil {
		t.Fatal("abrir SFTP desde una terminal local debería fallar, no entrar en pánico")
	}
	if _, err := m.OpenTerminalFrom("x", 80, 24); err == nil {
		t.Fatal("abrir una terminal remota desde una local debería fallar")
	}
	m.Close("x") // sin conexión compartida: no debe fallar
}

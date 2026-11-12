//go:build windows

package localterm

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// collect, waitFor y los tests que no dependen del sistema están en localterm_test.go.

func TestShellsOfferPowerShellFirstAndCmd(t *testing.T) {
	shells := Shells()
	ids := map[string]bool{}
	for _, s := range shells {
		ids[s.ID] = true
		if s.Name == "" {
			t.Errorf("la shell %q no tiene nombre", s.ID)
		}
	}
	if len(shells) == 0 || shells[0].ID != DefaultID {
		t.Fatalf("la primera tendría que ser %q: %+v", DefaultID, shells)
	}
	if !ids["cmd"] {
		t.Fatalf("falta cmd: %+v", shells)
	}
}

func TestStartRunsCommandsAndExits(t *testing.T) {
	p, sh, err := Start("cmd", "", 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	if sh.ID != "cmd" {
		t.Fatalf("shell = %q", sh.ID)
	}
	out := newCollect(p)
	// %OS% se expande en la salida pero no en el eco de lo tipeado.
	if _, err := p.Write([]byte("echo osat-%OS%\r\n")); err != nil {
		t.Fatal(err)
	}
	out.waitFor(t, "osat-Windows_NT")
	if err := p.Resize(80, 24); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	p.Write([]byte("exit\r\n"))
	done := make(chan struct{})
	go func() { p.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("la shell no terminó después de exit")
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := p.Close(); err != nil { // cerrar de nuevo no debe hacer nada
		t.Fatalf("segundo Close: %v", err)
	}
}

func TestStartOpensInTheGivenFolder(t *testing.T) {
	dir, err := os.MkdirTemp("", "osat-localterm-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	p, _, err := Start("cmd", dir, 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	out := newCollect(p)
	p.Write([]byte("cd\r\n")) // imprime la carpeta actual
	out.waitFor(t, filepath.Base(dir))
}

//go:build !windows

package localterm

import (
	"testing"
	"time"
)

func TestShellsOfferTheUserShellFirst(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	shells := Shells()
	if len(shells) == 0 || shells[0].ID != "sh" || shells[0].Name == "" {
		t.Fatalf("la primera tendría que ser la de $SHELL (sh): %+v", shells)
	}
	seen := map[string]bool{}
	for _, s := range shells {
		if seen[s.ID] {
			t.Fatalf("%q aparece dos veces: %+v", s.ID, shells)
		}
		seen[s.ID] = true
	}
	// Sin elegir ninguna (o con la de Windows guardada) se abre la del usuario.
	if s, err := find(shells, DefaultID); err != nil || s.ID != "sh" {
		t.Fatalf("find(DefaultID) = %+v, %v", s, err)
	}
}

func TestStartRunsCommandsAndExits(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	p, sh, err := Start("sh", "", 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	if sh.ID != "sh" {
		t.Fatalf("shell = %q", sh.ID)
	}
	out := newCollect(p)
	// La cuenta se hace en la salida, no en el eco de lo tipeado.
	if _, err := p.Write([]byte("echo osat-$((1+1))\r")); err != nil {
		t.Fatal(err)
	}
	out.waitFor(t, "osat-2")
	if err := p.Resize(80, 24); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	p.Write([]byte("stty size\r"))
	out.waitFor(t, "24 80")
	p.Write([]byte("exit\r"))
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
	dir := t.TempDir()
	p, _, err := Start("sh", dir, 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	out := newCollect(p)
	p.Write([]byte("pwd\r"))
	out.waitFor(t, dir)
}

// Cerrar la pestaña con la shell en marcha (y un programa corriendo) la termina.
func TestCloseEndsARunningShell(t *testing.T) {
	p, _, err := Start("sh", "", 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	out := newCollect(p)
	p.Write([]byte("echo li''sto; sleep 60\r")) // el eco dice li''sto: "listo" es la salida
	out.waitFor(t, "listo")
	p.Close()
	done := make(chan struct{})
	go func() { p.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("la shell siguió corriendo después de Close")
	}
}

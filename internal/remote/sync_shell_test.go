//go:build shelltest

// Sincronizar contra el servidor de prueba de copy_shell_test.go, que sirve SFTP sobre el disco real: a
// diferencia del servidor en memoria, guarda las fechas de modificación que fija osat (Chtimes), así que acá
// se prueba que una segunda comparación inmediata no encuentra diferencias.
// Uso: go test -tags shelltest -run TestSyncShell ./internal/remote/ -v

package remote

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// shellSyncRig: un Manager con la sesión "srv" sobre el servidor de prueba, la carpeta "del servidor"
// (en el disco real) y la de esta PC, las dos vacías.
func shellSyncRig(t *testing.T) (*Manager, string, string) {
	t.Helper()
	_, c, cl := startTestServer(t, "real", 0)
	m := newBridgeManager(t)
	m.register(&Session{ID: "srv", Kind: KindSFTP, ConnID: "c", sftp: cl, shared: newSharedClient(c)})
	return m, t.TempDir(), t.TempDir()
}

func shellReq(local, remote, dir, mode string) SyncRequest {
	return SyncRequest{RemoteDir: filepath.ToSlash(remote), LocalDir: local, Direction: dir, Mode: mode, Compare: "time+size"}
}

func sameModTime(t *testing.T, a, b string) {
	t.Helper()
	fa, err := os.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	fb, err := os.Stat(b)
	if err != nil {
		t.Fatal(err)
	}
	if fa.ModTime().Unix() != fb.ModTime().Unix() {
		t.Errorf("fechas distintas: %s %v, %s %v", a, fa.ModTime(), b, fb.ModTime())
	}
}

// emptyAgain compara otra vez, en los dos modos de esa dirección y en ambas direcciones: no tiene que haber nada.
func emptyAgain(t *testing.T, m *Manager, local, remote, dir string) {
	t.Helper()
	modes := []string{"sync", "mirror"}
	for _, mode := range modes {
		p, err := m.SyncCompare("srv", shellReq(local, remote, dir, mode))
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Actions) != 0 {
			t.Errorf("segunda comparación (%s, %s): %q", dir, mode, ops(p.Actions))
		}
	}
	p, err := m.SyncCompare("srv", shellReq(local, remote, "both", "sync"))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 0 {
		t.Errorf("segunda comparación (ambas): %q", ops(p.Actions))
	}
}

func TestSyncShellUpload(t *testing.T) {
	m, remote, local := shellSyncRig(t)
	old := time.Date(2020, 3, 4, 5, 6, 7, 0, time.UTC)
	writeLocal(t, local, "a.txt", "hola", old)
	writeLocal(t, local, "sub/b.txt", "chau", old.Add(time.Hour))
	writeLocal(t, remote, "a.txt", "viejo y más largo", old.Add(-time.Hour))

	p, err := m.SyncCompare("srv", shellReq(local, remote, "up", "sync"))
	if err != nil {
		t.Fatal(err)
	}
	sameStrings(t, "plan", ops(p.Actions), []string{"upload a.txt", "mkdirRemote sub", "upload sub/b.txt"})
	runAll(t, m, p)
	if readLocal(t, remote, "a.txt") != "hola" || readLocal(t, remote, "sub/b.txt") != "chau" {
		t.Fatal("el contenido subido no coincide")
	}
	sameModTime(t, filepath.Join(local, "a.txt"), filepath.Join(remote, "a.txt"))
	sameModTime(t, filepath.Join(local, "sub", "b.txt"), filepath.Join(remote, "sub", "b.txt"))
	emptyAgain(t, m, local, remote, "up")
}

func TestSyncShellDownload(t *testing.T) {
	m, remote, local := shellSyncRig(t)
	old := time.Date(2021, 1, 2, 3, 4, 5, 0, time.UTC)
	writeLocal(t, remote, "a.txt", "del servidor", old)
	writeLocal(t, remote, "d/e/f.txt", "hondo", old)

	p, err := m.SyncCompare("srv", shellReq(local, remote, "down", "mirror"))
	if err != nil {
		t.Fatal(err)
	}
	runAll(t, m, p)
	if readLocal(t, local, "d/e/f.txt") != "hondo" {
		t.Fatal("el contenido bajado no coincide")
	}
	sameModTime(t, filepath.Join(remote, "a.txt"), filepath.Join(local, "a.txt"))
	emptyAgain(t, m, local, remote, "down")
}

func TestSyncShellBothWays(t *testing.T) {
	m, remote, local := shellSyncRig(t)
	t0 := time.Date(2022, 6, 7, 8, 9, 10, 0, time.UTC)
	writeLocal(t, local, "pc.txt", "nuevo en la pc", t0.Add(time.Hour))
	writeLocal(t, remote, "pc.txt", "viejo", t0)
	writeLocal(t, local, "srv.txt", "viejo", t0)
	writeLocal(t, remote, "srv.txt", "nuevo en el servidor", t0.Add(time.Hour))
	writeLocal(t, local, "solo-pc.txt", "p", t0)
	writeLocal(t, remote, "solo-srv/x.txt", "s", t0)

	p, err := m.SyncCompare("srv", shellReq(local, remote, "both", "sync"))
	if err != nil {
		t.Fatal(err)
	}
	sameStrings(t, "plan", ops(p.Actions), []string{
		"upload pc.txt", "upload solo-pc.txt", "mkdirLocal solo-srv", "download solo-srv/x.txt", "download srv.txt",
	})
	runAll(t, m, p)
	if readLocal(t, remote, "pc.txt") != "nuevo en la pc" || readLocal(t, local, "srv.txt") != "nuevo en el servidor" ||
		readLocal(t, local, "solo-srv/x.txt") != "s" || readLocal(t, remote, "solo-pc.txt") != "p" {
		t.Fatal("el contenido no coincide")
	}
	p, err = m.SyncCompare("srv", shellReq(local, remote, "both", "sync"))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 0 {
		t.Errorf("segunda comparación: %q", ops(p.Actions))
	}
}

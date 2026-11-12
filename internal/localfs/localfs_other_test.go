//go:build !windows

package localfs

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"osat/internal/remote"
)

// Remove manda a la papelera del usuario (Linux) o avisa NO_TRASH (macOS).
func TestRemoveTrash(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	src := t.TempDir()
	name := "mi archivo ñ.txt"
	p := filepath.Join(src, name)
	if err := os.WriteFile(p, []byte("uno"), 0o644); err != nil {
		t.Fatal(err)
	}

	if runtime.GOOS == "darwin" {
		err := Remove([]string{p})
		e, ok := err.(*remote.Error)
		if !ok || e.Code != remote.CodeNoTrash || e.Data["failed"] != "0" {
			t.Fatalf("macOS: esperaba NO_TRASH failed=0, obtuve %v", err)
		}
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("el archivo no quedó intacto: %v", err)
		}
		return
	}

	// Linux: va a Trash/files y queda su .trashinfo.
	if err := Remove([]string{p}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err == nil {
		t.Fatal("el archivo sigue en su lugar")
	}
	if _, err := os.Stat(filepath.Join(data, "Trash", "files", name)); err != nil {
		t.Fatalf("no apareció en Trash/files: %v", err)
	}
	meta, err := os.ReadFile(filepath.Join(data, "Trash", "info", name+".trashinfo"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(meta)
	if !strings.Contains(s, "%20") || !strings.Contains(s, "%C3%B1") {
		t.Fatalf(".trashinfo sin Path escapado: %q", s)
	}
	if !strings.Contains(s, "DeletionDate=") {
		t.Fatalf(".trashinfo sin DeletionDate: %q", s)
	}

	// Un segundo archivo con el mismo nombre va a "....txt.2".
	p2 := filepath.Join(src, name)
	if err := os.WriteFile(p2, []byte("dos"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Remove([]string{p2}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(data, "Trash", "files", name+".2")); err != nil {
		t.Fatalf("el segundo no quedó como .2: %v", err)
	}
}

// Un elemento en otro sistema de archivos (EXDEV) no puede ir a la papelera:
// queda intacto y se devuelve NO_TRASH con su posición.
func TestRemoveTrashOtherFilesystem(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("solo Linux")
	}
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)

	shm, err := os.MkdirTemp("/dev/shm", "osat-test")
	if err != nil {
		t.Skip("no hay /dev/shm:", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shm) })
	var ds, dd syscall.Stat_t
	if err := syscall.Stat(shm, &ds); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Stat(data, &dd); err != nil {
		t.Fatal(err)
	}
	if ds.Dev == dd.Dev {
		t.Skip("/dev/shm está en el mismo dispositivo que la papelera")
	}

	src := t.TempDir()
	normal := filepath.Join(src, "normal.txt")
	if err := os.WriteFile(normal, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	enShm := filepath.Join(shm, "lejos.txt")
	if err := os.WriteFile(enShm, []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}

	err = Remove([]string{normal, enShm})
	e, ok := err.(*remote.Error)
	if !ok || e.Code != remote.CodeNoTrash || e.Data["failed"] != "1" {
		t.Fatalf("esperaba NO_TRASH failed=1, obtuve %v", err)
	}
	if _, err := os.Stat(normal); err == nil {
		t.Fatal("el normal no se mandó a la papelera")
	}
	if _, err := os.Stat(enShm); err != nil {
		t.Fatalf("el de /dev/shm no quedó intacto: %v", err)
	}
}

// DeleteForever borra de verdad un archivo y una carpeta con contenido.
func TestDeleteForever(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(filepath.Join(sub, "hijo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "hijo", "b.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := DeleteForever([]string{f, sub}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f); err == nil {
		t.Fatal("el archivo sigue existiendo")
	}
	if _, err := os.Stat(sub); err == nil {
		t.Fatal("la carpeta sigue existiendo")
	}
}

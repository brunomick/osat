package remote

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionLogStrip(t *testing.T) {
	l := &sessionLog{}
	got := string(l.strip([]byte("\x1b[1;32muser@web\x1b[0m:~$ ls\r\n\x1b]0;título\x07a\tb\x1b(B\x08\n")))
	if want := "user@web:~$ ls\na\tb\n"; got != want {
		t.Fatalf("strip = %q, quería %q", got, want)
	}
}

func TestSessionLogStripAcrossWrites(t *testing.T) {
	l := &sessionLog{}
	got := string(l.strip([]byte("hola \x1b[3"))) + string(l.strip([]byte("1mrojo\x1b]0;x\x1b"))) + string(l.strip([]byte("\\fin")))
	if want := "hola rojofin"; got != want {
		t.Fatalf("strip = %q, quería %q", got, want)
	}
}

func TestSessionLogFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	a := openSessionLog(dir, "web 01/prod")
	b := openSessionLog(dir, "web 01/prod") // mismo nombre, mismo segundo: otro archivo
	if a == nil || b == nil {
		t.Fatal("no se creó el registro")
	}
	a.Write([]byte("\x1b[31muno\x1b[0m\r\n"))
	a.Close()
	a.Write([]byte("después de cerrar")) // no falla
	b.Close()

	files, _ := filepath.Glob(filepath.Join(dir, "web_01_prod_*.log"))
	if len(files) != 2 {
		t.Fatalf("archivos = %v", files)
	}
	got, _ := os.ReadFile(files[0])
	if string(got) != "uno\n" {
		t.Fatalf("contenido = %q", got)
	}

	var none *sessionLog = openSessionLog("", "x") // ajuste apagado
	none.Write([]byte("nada"))
	none.Close()
}

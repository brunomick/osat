package winclip

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func opener(contents map[int]string) func(i int) (io.ReadCloser, error) {
	return func(i int) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(contents[i])), nil }
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSaveTreeAndConflicts(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "informe.txt"), []byte("viejo"), 0o644)
	os.Mkdir(filepath.Join(dir, "fotos"), 0o755)

	mt := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	entries := []Entry{
		{Name: "informe.txt", ModTime: mt},
		{Name: "fotos", Dir: true},
		{Name: `fotos\a.jpg`},
		{Name: `fotos\sub\b.jpg`},
		{Name: "nuevo.txt"},
	}
	made, err := save(dir, entries, opener(map[int]string{0: "nuevo informe", 2: "A", 3: "B", 4: "N"}))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(dir, "informe - copia.txt"),
		filepath.Join(dir, "fotos - copia"),
		filepath.Join(dir, "nuevo.txt"),
	}
	if strings.Join(made, "|") != strings.Join(want, "|") {
		t.Fatalf("creados = %v; quería %v", made, want)
	}
	if read(t, filepath.Join(dir, "informe.txt")) != "viejo" {
		t.Fatal("pisó el archivo que ya estaba")
	}
	if read(t, want[0]) != "nuevo informe" || read(t, filepath.Join(want[1], "a.jpg")) != "A" ||
		read(t, filepath.Join(want[1], "sub", "b.jpg")) != "B" || read(t, want[2]) != "N" {
		t.Fatal("contenido distinto")
	}
	if fi, _ := os.Stat(want[0]); !fi.ModTime().Equal(mt) {
		t.Fatalf("fecha = %v; quería %v", fi.ModTime(), mt)
	}
}

func TestSaveRejectsBadNames(t *testing.T) {
	for _, name := range []string{`..\fuera.txt`, `a\..\..\b`, `C:\Windows\x`, `\raiz`, `a\\b`, `x:flujo`} {
		dir := t.TempDir()
		if _, err := save(dir, []Entry{{Name: name}}, opener(map[int]string{0: "x"})); err == nil {
			t.Errorf("%q: quería un error", name)
		}
		if es, _ := os.ReadDir(dir); len(es) != 0 {
			t.Errorf("%q: escribió algo", name)
		}
	}
}

func TestSaveNeedsAFolder(t *testing.T) {
	f := filepath.Join(t.TempDir(), "archivo")
	os.WriteFile(f, nil, 0o644)
	if _, err := save(f, []Entry{{Name: "a"}}, opener(nil)); err == nil {
		t.Fatal("quería un error al pegar en un archivo")
	}
}

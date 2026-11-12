package localfs

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"osat/internal/remote"
)

func TestListMoveConflict(t *testing.T) {
	dir := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(Mkdir(filepath.Join(dir, "sub")))
	must(os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hola"), 0o644))

	l, err := List(dir)
	must(err)
	if len(l.Entries) != 2 || l.Parent == "" || len(l.Crumbs) < 2 {
		t.Fatalf("listado inesperado: %+v", l)
	}
	kinds := map[string]string{}
	for _, e := range l.Entries {
		kinds[e.Name] = e.Mode
	}
	if kinds["sub"] != "Carpeta" || kinds["a.txt"] != "TXT" {
		t.Fatalf("tipos: %v", kinds)
	}

	must(Move([]string{filepath.Join(dir, "a.txt")}, filepath.Join(dir, "sub")))
	if _, err := os.Stat(filepath.Join(dir, "sub", "a.txt")); err != nil {
		t.Fatal("no se movió:", err)
	}

	// Mover algo con el mismo nombre que uno existente: CONFLICT, sin tocar nada.
	must(os.WriteFile(filepath.Join(dir, "a.txt"), []byte("otro"), 0o644))
	err = Move([]string{filepath.Join(dir, "a.txt")}, filepath.Join(dir, "sub"))
	if e, ok := err.(*remote.Error); !ok || e.Code != remote.CodeConflict {
		t.Fatalf("esperaba CONFLICT, obtuve %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "sub", "a.txt")); string(b) != "hola" {
		t.Fatal("se pisó el archivo existente")
	}

	if err := Move([]string{filepath.Join(dir, "sub")}, filepath.Join(dir, "sub")); err == nil {
		t.Fatal("dejó mover una carpeta dentro de sí misma")
	}
	if err := Rename(filepath.Join(dir, "a.txt"), filepath.Join(dir, "sub")); err == nil {
		t.Fatal("el renombrado pisó algo existente")
	}
}

func TestDrivesCrumb(t *testing.T) {
	if !hasDrives {
		t.Skip("solo Windows")
	}
	l, err := List(Drives)
	if err != nil || len(l.Entries) == 0 {
		t.Fatalf("unidades: %v %+v", err, l)
	}
	c, _ := List(`C:\`)
	if c.Parent != Drives || c.Crumbs[0].Path != Drives || c.Crumbs[1].Name != "C:" {
		t.Fatalf("raíz de unidad: %+v", c)
	}
}

func TestReadWriteText(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "notas.txt")
	if err := os.WriteFile(p, []byte("hola ñandú\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := ReadText(p)
	if err != nil || s != "hola ñandú\n" {
		t.Fatalf("ReadText = %q, %v", s, err)
	}
	if err := WriteText(p, "chau\r\n"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "chau\r\n" {
		t.Fatalf("contenido = %q", b)
	}

	bin := filepath.Join(dir, "x.bin")
	if err := os.WriteFile(bin, []byte{'a', 0, 'b'}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadText(bin); err != ErrNotText {
		t.Fatalf("binario: err = %v", err)
	}
	big := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(big, make([]byte, maxEditSize+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadText(big); err != ErrTooBig {
		t.Fatalf("grande: err = %v", err)
	}
	// No crea archivos: solo guarda los que existen.
	if err := WriteText(filepath.Join(dir, "nuevo.txt"), "x"); err == nil {
		t.Fatal("WriteText creó un archivo inexistente")
	}
}

func TestCopy(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) string {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	cpp := write("main.cpp", "// informaci\xf3n\r\nint main(){}\r\n") // Windows-1252 + CRLF: se copia byte a byte
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(cpp, old, old); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join("src", "a.txt"), "a")
	write(filepath.Join("src", "sub", "b.txt"), "bb")
	if err := os.Mkdir(filepath.Join(dir, "otra"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Misma carpeta: "main - copia.cpp", después "main - copia (2).cpp".
	got, err := Copy([]string{cpp}, dir)
	if want := filepath.Join(dir, "main - copia.cpp"); err != nil || len(got) != 1 || got[0] != want {
		t.Fatalf("copia 1: %v, %v", got, err)
	}
	got, err = Copy([]string{cpp}, dir)
	if want := filepath.Join(dir, "main - copia (2).cpp"); err != nil || len(got) != 1 || got[0] != want {
		t.Fatalf("copia 2: %v, %v", got, err)
	}
	for _, p := range []string{filepath.Join(dir, "main - copia.cpp"), filepath.Join(dir, "main - copia (2).cpp")} {
		if read(p) != "// informaci\xf3n\r\nint main(){}\r\n" {
			t.Errorf("%s: contenido distinto", p)
		}
		if fi, _ := os.Stat(p); !fi.ModTime().Equal(old) {
			t.Errorf("%s: la fecha de modificación no se conservó: %v", p, fi.ModTime())
		}
	}

	// Carpeta con subcarpetas; en otra carpeta conserva el nombre.
	got, err = Copy([]string{filepath.Join(dir, "src")}, dir)
	if want := filepath.Join(dir, "src - copia"); err != nil || len(got) != 1 || got[0] != want {
		t.Fatalf("carpeta: %v, %v", got, err)
	}
	if read(filepath.Join(dir, "src - copia", "sub", "b.txt")) != "bb" {
		t.Error("contenido de la carpeta copiada distinto")
	}
	got, err = Copy([]string{filepath.Join(dir, "src"), cpp}, filepath.Join(dir, "otra"))
	if err != nil || len(got) != 2 || filepath.Base(got[0]) != "src" || filepath.Base(got[1]) != "main.cpp" {
		t.Fatalf("otra carpeta: %v, %v", got, err)
	}

	// Una carpeta no se copia dentro de sí misma; no existe → error; el original no cambia.
	if _, err := Copy([]string{filepath.Join(dir, "src")}, filepath.Join(dir, "src", "sub")); err == nil {
		t.Error("copiar una carpeta dentro de sí misma debería fallar")
	}
	if _, err := Copy([]string{filepath.Join(dir, "no-existe")}, dir); err == nil {
		t.Error("copiar algo que no existe debería fallar")
	}
	if read(cpp) != "// informaci\xf3n\r\nint main(){}\r\n" {
		t.Error("el original cambió")
	}
}

// Un .cpp guardado en Windows-1252 (ANSI) se abre y se guarda de vuelta en
// Windows-1252, no se convierte a UTF-8 ni se corrompe.
func TestReadWriteTextWindows1252(t *testing.T) {
	p := filepath.Join(t.TempDir(), "main.cpp")
	orig := "// informaci\xf3n\r\nint main() { return 0; }\r\n"
	if err := os.WriteFile(p, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := ReadText(p)
	if err != nil || s != "// información\r\nint main() { return 0; }\r\n" {
		t.Fatalf("ReadText = %q, %v", s, err)
	}
	if err := WriteText(p, s+"// canción\r\n"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != orig+"// canci\xf3n\r\n" {
		t.Fatalf("contenido = %q", b)
	}
	// Un carácter que Windows-1252 no tiene no se guarda ni toca el archivo.
	if err := WriteText(p, "x → y"); err == nil {
		t.Fatal("WriteText aceptó un carácter sin equivalente")
	}
	if b, _ := os.ReadFile(p); string(b) != orig+"// canci\xf3n\r\n" {
		t.Fatalf("el archivo cambió: %q", b)
	}
	// Un binario sin ceros sigue rechazado.
	png := filepath.Join(filepath.Dir(p), "x.png")
	if err := os.WriteFile(png, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadText(png); err != ErrNotText {
		t.Fatalf("png: err = %v", err)
	}
}

// En Rename y Move la comparación es por archivo real (os.SameFile), no por
// texto: en Linux a.txt y A.txt son distintos (no se pisan); en Windows/macOS
// son el mismo (renombrar solo cambia mayúsculas).
func TestRenameAndMoveCase(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	A := filepath.Join(dir, "A.txt")
	if err := os.WriteFile(a, []byte("minus"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(A); err == nil {
		// El FS no distingue mayúsculas (Windows/macOS): renombrar a A.txt anda.
		if err := Rename(a, A); err != nil {
			t.Fatalf("Rename a->A: %v", err)
		}
		l, err := List(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(l.Entries) != 1 || l.Entries[0].Name != "A.txt" {
			t.Fatalf("esperaba una sola entrada A.txt: %+v", l.Entries)
		}
		return
	}
	// El FS distingue mayúsculas (Linux): a.txt y A.txt conviven.
	if err := os.WriteFile(A, []byte("mayus"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Rename(a, A); err == nil {
		t.Fatal("Rename a->A no debería pisar A.txt existente")
	}
	if b, _ := os.ReadFile(A); string(b) != "mayus" {
		t.Fatalf("A.txt se pisó: %q", b)
	}
	// Move de D/f.txt a d (carpetas que solo difieren en mayúsculas): se mueve.
	D, d := filepath.Join(dir, "D"), filepath.Join(dir, "d")
	if err := os.Mkdir(D, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(D, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Move([]string{filepath.Join(D, "f.txt")}, d); err != nil {
		t.Fatalf("Move D/f.txt -> d: %v", err)
	}
	if _, err := os.Stat(filepath.Join(d, "f.txt")); err != nil {
		t.Fatalf("d/f.txt no quedó: %v", err)
	}
}

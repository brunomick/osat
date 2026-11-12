//go:build shelltest

// Comprimir con tar de verdad contra el sshd mínimo de copy_shell_test.go.
// Uso: go test -tags shelltest -run TestCompress ./internal/remote/ -v

package remote

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func readTarGz(t *testing.T, p string) map[string]string {
	t.Helper()
	f, err := os.Open(filepath.FromSlash(p))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("no es un gzip: %v", err)
	}
	out := map[string]string{}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		out[strings.TrimSuffix(h.Name, "/")] = string(b)
	}
}

func compressRig(t *testing.T, mode string) (*Manager, string) {
	t.Helper()
	if _, err := exec.LookPath("tar"); err != nil && mode == "real" {
		t.Skip("no hay tar en el PATH")
	}
	_, c, cl := startTestServer(t, mode, 0)
	m := newBridgeManager(t)
	m.register(&Session{ID: "s", Kind: KindSFTP, ConnID: "c", shared: newSharedClient(c), sftp: cl})
	return m, filepath.ToSlash(t.TempDir())
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCompressWithRealTar(t *testing.T) {
	m, dir := compressRig(t, "real")
	writeFile(t, dir, "src/a.txt", "a")
	writeFile(t, dir, "src/sub/b.txt", "bb")
	writeFile(t, dir, "it's a file (1).txt", "comillas y espacios")
	writeFile(t, dir, "-rf", "empieza con guion")

	got, err := m.Compress("s", []string{dir + "/src", dir + "/it's a file (1).txt", dir + "/-rf"}, "mi:backup")
	if err != nil {
		t.Fatal(err)
	}
	if got != dir+"/mi:backup.tar.gz" {
		t.Fatalf("ruta: %s", got)
	}
	files := readTarGz(t, got)
	for name, content := range map[string]string{"src/a.txt": "a", "src/sub/b.txt": "bb", "it's a file (1).txt": "comillas y espacios", "-rf": "empieza con guion"} {
		if files[name] != content {
			t.Errorf("%q = %q, quería %q (hay %v)", name, files[name], content, keys(files))
		}
	}
	for name := range files {
		if strings.HasPrefix(name, "/") || strings.Contains(name, strings.TrimPrefix(dir, "/")) {
			t.Errorf("las rutas del .tar tienen que ser relativas: %q", name)
		}
	}

	// Sin comprimir, si se pide .tar.
	plain, err := m.Compress("s", []string{dir + "/src"}, "src.tar")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.FromSlash(plain))
	if len(b) < 512 || string(b[257:262]) != "ustar" {
		t.Fatal("src.tar tendría que ser un tar sin comprimir")
	}

	// No pisa: el mismo nombre falla y deja intacto el anterior.
	if _, err := m.Compress("s", []string{dir + "/src"}, "src.tar"); err == nil || !strings.Contains(err.Error(), "ya existe") {
		t.Fatalf("error: %v", err)
	}
	// Elementos de carpetas distintas, o que no existen.
	if _, err := m.Compress("s", []string{dir + "/src/a.txt", dir + "/-rf"}, "x"); err == nil {
		t.Fatal("tienen que estar en la misma carpeta")
	}
	if _, err := m.Compress("s", []string{dir + "/nada"}, "x"); err == nil {
		t.Fatal("un elemento inexistente tiene que fallar")
	}
	if _, err := os.Stat(filepath.FromSlash(dir + "/x.tar.gz")); err == nil {
		t.Fatal("no debería haber quedado un archivo de un intento fallido")
	}
}

func TestCompressWithoutTarOrShell(t *testing.T) {
	m, dir := compressRig(t, "notfound")
	writeFile(t, dir, "a.txt", "a")
	if _, err := m.Compress("s", []string{dir + "/a.txt"}, "x"); err == nil || !strings.Contains(err.Error(), "tar no está instalado") {
		t.Fatalf("error: %v", err)
	}

	m, dir = compressRig(t, "reject")
	writeFile(t, dir, "a.txt", "a")
	if _, err := m.Compress("s", []string{dir + "/a.txt"}, "x"); err == nil || !strings.Contains(err.Error(), "no permite ejecutar comandos") {
		t.Fatalf("error: %v", err)
	}
	// Y se recuerda: la próxima ni lo intenta.
	if _, err := m.Compress("s", []string{dir + "/a.txt"}, "x"); err == nil || !strings.Contains(err.Error(), "no permite ejecutar comandos") {
		t.Fatalf("error: %v", err)
	}
}

func keys(m map[string]string) []string {
	var k []string
	for n := range m {
		k = append(k, n)
	}
	sort.Strings(k)
	return k
}

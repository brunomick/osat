package remote

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestCopyName(t *testing.T) {
	taken := func(names ...string) func(string) bool {
		return func(n string) bool {
			for _, x := range names {
				if x == n {
					return true
				}
			}
			return false
		}
	}
	for _, c := range []struct {
		base  string
		isDir bool
		taken []string
		want  string
	}{
		{"a.txt", false, nil, "a.txt"},                                            // otra carpeta: sin cambios
		{"a.txt", false, []string{"a.txt"}, "a - copia.txt"},                      // misma carpeta
		{"a.txt", false, []string{"a.txt", "a - copia.txt"}, "a - copia (2).txt"}, // segunda copia
		{"a.txt", false, []string{"a.txt", "a - copia.txt", "a - copia (2).txt"}, "a - copia (3).txt"},
		{"a.tar.gz", false, []string{"a.tar.gz"}, "a.tar - copia.gz"},
		{"Makefile", false, []string{"Makefile"}, "Makefile - copia"},
		{".bashrc", false, []string{".bashrc"}, ".bashrc - copia"}, // dotfile: sin extensión
		{"src", true, []string{"src"}, "src - copia"},
		{"v1.2", true, []string{"v1.2"}, "v1.2 - copia"}, // una carpeta no tiene extensión
	} {
		if got := CopyName(c.base, c.isDir, taken(c.taken...)); got != c.want {
			t.Errorf("CopyName(%q, dir=%v, %v) = %q, quería %q", c.base, c.isDir, c.taken, got, c.want)
		}
	}
}

func TestCopyFilesRemote(t *testing.T) {
	cl := memSFTP(t)
	ctx := context.Background()
	if err := cl.MkdirAll("/w/src/sub"); err != nil {
		t.Fatal(err)
	}
	cl.MkdirAll("/w/otra")
	put(t, cl, "/w/a.cpp", "int main(){}\r\n// información")
	put(t, cl, "/w/src/x.txt", "x")
	put(t, cl, "/w/src/sub/y.txt", "yy")

	// Misma carpeta: "a - copia.cpp", y otra vez "a - copia (2).cpp".
	got, err := copyFiles(ctx, cl, nil, []string{"/w/a.cpp"}, "/w")
	if err != nil || !reflect.DeepEqual(got, []string{"/w/a - copia.cpp"}) {
		t.Fatalf("copia 1: %v, %v", got, err)
	}
	got, err = copyFiles(ctx, cl, nil, []string{"/w/a.cpp"}, "/w")
	if err != nil || !reflect.DeepEqual(got, []string{"/w/a - copia (2).cpp"}) {
		t.Fatalf("copia 2: %v, %v", got, err)
	}
	for _, p := range []string{"/w/a - copia.cpp", "/w/a - copia (2).cpp", "/w/a.cpp"} {
		if s := get(t, cl, p); s != "int main(){}\r\n// información" {
			t.Errorf("%s = %q", p, s) // bytes idénticos, con CRLF y acentos
		}
	}
	assertNoTemps(t, cl, "/w")

	// Carpeta entera, recursiva, en la misma carpeta y en otra.
	got, err = copyFiles(ctx, cl, nil, []string{"/w/src"}, "/w")
	if err != nil || !reflect.DeepEqual(got, []string{"/w/src - copia"}) {
		t.Fatalf("carpeta: %v, %v", got, err)
	}
	if get(t, cl, "/w/src - copia/x.txt") != "x" || get(t, cl, "/w/src - copia/sub/y.txt") != "yy" {
		t.Error("el contenido de la carpeta copiada no coincide")
	}
	got, err = copyFiles(ctx, cl, nil, []string{"/w/src", "/w/a.cpp"}, "/w/otra")
	if err != nil || !reflect.DeepEqual(got, []string{"/w/otra/src", "/w/otra/a.cpp"}) {
		t.Fatalf("otra carpeta (nombres sin cambios): %v, %v", got, err)
	}

	// Una carpeta no se copia dentro de sí misma, ni se pisa nada.
	if _, err := copyFiles(ctx, cl, nil, []string{"/w/src"}, "/w/src/sub"); err == nil || !strings.Contains(err.Error(), "dentro de sí misma") {
		t.Errorf("copiar dentro de sí misma: err = %v", err)
	}
	if _, err := copyFiles(ctx, cl, nil, []string{"/w/no-existe"}, "/w"); err == nil {
		t.Error("copiar algo que no existe debería fallar")
	}
	if s := get(t, cl, "/w/a.cpp"); s != "int main(){}\r\n// información" {
		t.Errorf("el original cambió: %q", s)
	}
}

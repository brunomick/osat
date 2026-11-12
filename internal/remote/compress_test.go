package remote

import (
	"reflect"
	"strings"
	"testing"
)

func TestArchiveName(t *testing.T) {
	for in, want := range map[string]string{
		"backup":         "backup.tar.gz",
		" backup ":       "backup.tar.gz",
		"backup.tar.gz":  "backup.tar.gz",
		"backup.TAR.GZ":  "backup.TAR.GZ",
		"backup.tgz":     "backup.tgz",
		"backup.tar":     "backup.tar",
		"backup.zip":     "backup.zip",
		"Backup.ZIP":     "Backup.ZIP",
		"datos.rar":      "datos.rar.tar.gz",
		"v1.2":           "v1.2.tar.gz",
		"it's a (1).tar": "it's a (1).tar",
	} {
		if got := ArchiveName(in); got != want {
			t.Errorf("ArchiveName(%q) = %q, quería %q", in, got, want)
		}
	}
}

func TestArchiveCommand(t *testing.T) {
	cases := []struct {
		dir, archive string
		names        []string
		want         string
	}{
		{"/srv/app", "app.tar.gz", []string{"src"}, `cd '/srv/app' && tar -czf './app.tar.gz' -- 'src'`},
		{"/srv/app", "app.tgz", []string{"a", "b c"}, `cd '/srv/app' && tar -czf './app.tgz' -- 'a' 'b c'`},
		{"/srv/app", "app.tar", []string{"a"}, `cd '/srv/app' && tar -cf './app.tar' -- 'a'`},
		// Comillas, "-" al principio y ":" no se interpretan: todo va entre comillas y tras "--" / "./".
		{"/home/o'brien", "a:b.tar.gz", []string{"-rf", "it's"}, `cd '/home/o'\''brien' && tar -czf './a:b.tar.gz' -- '-rf' 'it'\''s'`},
		{"/srv/app", "app.zip", []string{"src", "b c"}, `cd '/srv/app' && zip -r -q -y './app.zip' 'src' 'b c'`},
		// zip no tiene "--": los nombres con guion al principio llevan "./".
		{"/srv/app", "-x.zip", []string{"-rf", "it's"}, `cd '/srv/app' && zip -r -q -y './-x.zip' './-rf' 'it'\''s'`},
	}
	for _, c := range cases {
		if got := archiveCommand(c.dir, c.archive, c.names); got != c.want {
			t.Errorf("archiveCommand(%q, %q, %q)\n got %s\nwant %s", c.dir, c.archive, c.names, got, c.want)
		}
	}
}

func TestMissingToolMessages(t *testing.T) {
	if got := missingTool("zip").Error(); !strings.Contains(got, "zip no está instalado") || !strings.Contains(got, ".tar.gz") {
		t.Errorf("zip: %q", got)
	}
	if got := missingTool("tar").Error(); got != "tar no está instalado en el servidor" {
		t.Errorf("tar: %q", got)
	}
}

func TestCompressNeedsASFTPSessionWithAShell(t *testing.T) {
	m := newBridgeManager(t)
	if _, err := m.Compress("no-existe", []string{"/a"}, "x"); err == nil {
		t.Fatal("sesión inexistente")
	}
	cl := memSFTP(t)
	put(t, cl, "/a.txt", "a")
	m.register(&Session{ID: "s", Kind: KindSFTP, ConnID: "c", sftp: cl}) // sin conexión SSH: no hay dónde correr tar
	for _, bad := range [][]string{nil, {}} {
		if _, err := m.Compress("s", bad, "x"); err == nil {
			t.Fatalf("%v: no hay nada para comprimir", bad)
		}
	}
	for _, noName := range []string{".tar.gz", ".zip", ""} {
		if _, err := m.Compress("s", []string{"/a.txt"}, noName); err == nil {
			t.Fatalf("%q: sin nombre", noName)
		}
	}
	if _, err := m.Compress("s", []string{"/a.txt"}, "../x"); err == nil {
		t.Fatal("el nombre no puede llevar /")
	}
	if _, err := m.Compress("s", []string{"/a.txt"}, "x"); err == nil {
		t.Fatal("sin shell no se puede comprimir")
	}
}

func TestExtractBase(t *testing.T) {
	for in, want := range map[string]string{
		"proyecto.tar.gz":  "proyecto",
		"proyecto.TGZ":     "proyecto",
		"a.b.tar.xz":       "a.b",
		"x.tar.bz2":        "x",
		"x.tar.zst":        "x",
		"x.tar":            "x",
		"Datos (1).zip":    "Datos (1)",
		"it's.tar.gz":      "it's",
		"backup.tar.gz.sh": "",
		"notas.txt":        "",
		".tar.gz":          "", // sin nombre
		"zip":              "",
	} {
		base, ok := ExtractBase(in)
		if base != want || ok != (want != "") {
			t.Errorf("ExtractBase(%q) = %q, %v; quería %q", in, base, ok, want)
		}
	}
}

func TestListTopCommand(t *testing.T) {
	const tail = ` | sed -e 's|^\./||' -e 's|^\(/*[^/]*\).*|\1|' | sort -u | head -n 200`
	if got, want := listTopCommand("/srv/it's.tar.gz"), `tar -tf '/srv/it'\''s.tar.gz'`+tail; got != want {
		t.Errorf("tar:\n got %s\nwant %s", got, want)
	}
	if got, want := listTopCommand("/srv/x.ZIP"), `unzip -Z1 '/srv/x.ZIP'`+tail; got != want {
		t.Errorf("zip:\n got %s\nwant %s", got, want)
	}
}

func TestParseTop(t *testing.T) {
	top, err := parseTop("proyecto\n")
	if err != nil || !reflect.DeepEqual(top, []string{"proyecto"}) {
		t.Fatalf("%v %v", top, err)
	}
	// Un tar hecho con "tar cf x ." trae la entrada "./", que no cuenta.
	top, err = parseTop("\na\nb c\n")
	if err != nil || !reflect.DeepEqual(top, []string{"a", "b c"}) {
		t.Fatalf("%v %v", top, err)
	}
	for _, bad := range []string{"", "\n", ".\n", "/etc\n", "ok\n/root\n", "..\n", "ok\n..\n"} {
		if _, err := parseTop(bad); err == nil {
			t.Errorf("%q no debería valer", bad)
		}
	}
}

func TestExtractPlan(t *testing.T) {
	has := func(names ...string) func(string) bool {
		return func(n string) bool {
			for _, x := range names {
				if x == n {
					return true
				}
			}
			return false
		}
	}
	cases := []struct {
		name       string
		top        []string
		exists     func(string) bool
		want       string
		wantFolder bool
	}{
		{"una sola carpeta en la raíz y libre: directo", []string{"proyecto"}, has("proyecto.tar.gz"), "/srv/proyecto", false},
		{"una sola, pero ya existe: carpeta nueva, sin pisarla", []string{"proyecto"}, has("proyecto.tar.gz", "proyecto"), "/srv/proyecto - copia", true},
		{"varias entradas en la raíz: carpeta con el nombre del archivo", []string{"a", "b"}, has("proyecto.tar.gz"), "/srv/proyecto", true},
		{"la carpeta del archivo ya existe: otra", []string{"a", "b"}, has("proyecto", "proyecto - copia"), "/srv/proyecto - copia (2)", true},
	}
	for _, c := range cases {
		got, folder := extractPlan("/srv", "proyecto", c.top, c.exists)
		if got != c.want || folder != c.wantFolder {
			t.Errorf("%s: %q, %v; quería %q, %v", c.name, got, folder, c.want, c.wantFolder)
		}
	}
}

func TestExtractCommand(t *testing.T) {
	if got, want := extractCommand("/srv/x.tar.gz", "/srv/x"), `tar -xf '/srv/x.tar.gz' -C '/srv/x' --no-same-owner`; got != want {
		t.Errorf("tar: %s", got)
	}
	if got, want := extractCommand("/srv/it's.zip", "/srv/x y"), `unzip -q -n '/srv/it'\''s.zip' -d '/srv/x y'`; got != want {
		t.Errorf("zip: %s", got)
	}
}

func TestExtractNeedsASFTPSessionWithAShell(t *testing.T) {
	m := newBridgeManager(t)
	if _, err := m.Extract("no-existe", "/a.tar.gz"); err == nil {
		t.Fatal("sesión inexistente")
	}
	cl := memSFTP(t)
	put(t, cl, "/a.tar.gz", "x")
	put(t, cl, "/notas.txt", "x")
	m.register(&Session{ID: "s", Kind: KindSFTP, ConnID: "c", sftp: cl})
	if _, err := m.Extract("s", "/notas.txt"); err == nil || !strings.Contains(err.Error(), "no sé extraer") {
		t.Fatalf("error: %v", err)
	}
	if _, err := m.Extract("s", "/a.tar.gz"); err == nil || !strings.Contains(err.Error(), "no permite ejecutar comandos") {
		t.Fatalf("sin shell no se puede extraer: %v", err)
	}
}

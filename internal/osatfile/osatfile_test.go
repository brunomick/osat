package osatfile

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"osat/internal/store"
)

func TestSealOpenRoundTrip(t *testing.T) {
	in := []Entry{
		{Name: "web", Group: "Prod/Web", Protocol: store.ProtoSSH, Host: "10.0.0.1", Port: 22, User: "root",
			AuthType: store.AuthKey, KeyPath: `C:\keys\web.ppk`, Color: "#f00",
			Secret: "frase", KeyName: "web.ppk", Key: []byte("PuTTY-User-Key-File-3: ssh-ed25519\n...")},
		{Name: "dc", Protocol: store.ProtoRDP, Host: "dc01", Port: 3389, User: "admin", Domain: "CORP", Gateway: "rds.corp",
			AuthType: store.AuthPassword, Secret: "pá$$wörd"},
		{Name: "app", Protocol: store.ProtoSSH, Host: "app", Port: 22, User: "deploy", AuthType: store.AuthAgent,
			ForwardAgent: true, InitialCommand: "cd /srv && ls", Env: []string{"LANG=es_AR.UTF-8", "A=b=c"}},
	}
	snips := []SnippetEntry{
		{Name: "reiniciar nginx", Folder: "Web", Command: "sudo systemctl restart nginx", Mode: "run", Group: "Prod"},
		{Name: "tail", Command: "tail -f /var/log/{{archivo}}\n", Mode: "paste"},
	}
	sites := []SiteEntry{{Name: "Hosting", URL: "https://panel.example.com/login"}, {Name: "Nube", URL: "http://nube.local:8080"}}
	data, err := Seal(in, snips, sites, nil, "clave del equipo")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"frase", "10.0.0.1", "PuTTY-User-Key"} {
		if bytes.Contains(data, []byte(s)) {
			t.Fatalf("el archivo tiene %q sin cifrar", s)
		}
	}
	if bytes.Contains(data, []byte("systemctl")) {
		t.Fatal("el archivo tiene un snippet sin cifrar")
	}
	out, outSnips, outSites, _, err := Open(data, "clave del equipo")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("difieren:\n%+v\n%+v", in, out)
	}
	if !reflect.DeepEqual(snips, outSnips) {
		t.Fatalf("snippets difieren:\n%+v\n%+v", snips, outSnips)
	}
	if !reflect.DeepEqual(sites, outSites) {
		t.Fatalf("sitios difieren:\n%+v\n%+v", sites, outSites)
	}
	if bytes.Contains(data, []byte("panel.example.com")) {
		t.Fatal("el archivo tiene un sitio sin cifrar")
	}
}

// Un archivo sin snippets (como los de antes) se abre con la lista vacía.
func TestOpenWithoutSnippets(t *testing.T) {
	data, err := Seal([]Entry{{Name: "x", Host: "h", User: "u"}}, nil, nil, nil, "pw")
	if err != nil {
		t.Fatal(err)
	}
	entries, snips, _, _, err := Open(data, "pw")
	if err != nil || len(entries) != 1 || len(snips) != 0 {
		t.Fatalf("Open = %+v, %+v, %v", entries, snips, err)
	}
}

func TestOpenWrongPassword(t *testing.T) {
	data, err := Seal([]Entry{{Name: "x", Host: "h", User: "u"}}, nil, nil, nil, "buena")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := Open(data, "mala"); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("esperaba ErrBadPassword, fue %v", err)
	}
}

func TestOpenNotOsat(t *testing.T) {
	if _, _, _, _, err := Open([]byte(`{"connections":[]}`), "x"); err == nil {
		t.Fatal("aceptó un archivo que no es .osat")
	}
}

func TestSealNeedsPassword(t *testing.T) {
	if _, err := Seal(nil, nil, nil, nil, ""); err == nil {
		t.Fatal("exportó sin contraseña")
	}
}

func TestSaveKey(t *testing.T) {
	dir := t.TempDir()
	p1, err := SaveKey(dir, `C:\Users\otro\.ssh\id_rsa`, []byte("A"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(p1) != dir {
		t.Fatalf("quedó fuera de la carpeta: %s", p1)
	}
	if b, _ := os.ReadFile(p1); string(b) != "A" {
		t.Fatalf("contenido %q", b)
	}
	// La misma clave otra vez: mismo archivo.
	if p, _ := SaveKey(dir, "id_rsa", []byte("A")); p != p1 {
		t.Fatalf("no reusó el archivo: %s vs %s", p, p1)
	}
	// Otra clave con el mismo nombre: no pisa la primera.
	p2, _ := SaveKey(dir, "id_rsa", []byte("B"))
	if p2 == p1 {
		t.Fatal("pisó una clave distinta con el mismo nombre")
	}
	if p, _ := SaveKey(dir, "server.ppk", []byte("C")); filepath.Ext(p) != ".ppk" {
		t.Fatalf("perdió la extensión: %s", p)
	}
	// Un nombre con ../ no se escapa de la carpeta.
	if p, _ := SaveKey(dir, "../../evil", []byte("D")); filepath.Dir(p) != dir {
		t.Fatalf("se escapó de la carpeta: %s", p)
	}
}

func TestPinsRoundTrip(t *testing.T) {
	in := []Entry{{Name: "a", Protocol: store.ProtoSSH, Host: "h", Port: 22, User: "u", AuthType: store.AuthAgent,
		Pins: []store.PinnedDir{{Path: "/srv", Name: "web"}, {Path: "/var/log"}}}}
	data, err := Seal(in, nil, nil, nil, "clave")
	if err != nil {
		t.Fatal(err)
	}
	out, _, _, _, err := Open(data, "clave")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("difieren:\n%+v\n%+v", in, out)
	}
}

// Las credenciales por defecto van cifradas y vuelven igual; sin ellas, Open devuelve nil.
func TestDefaultsRoundTrip(t *testing.T) {
	in := &DefaultsEntry{User: "admin", Domain: "CORP", Secret: "s3cr3t-por-defecto"}
	data, err := Seal([]Entry{{Name: "x", Host: "h", UseDefaultCreds: true}}, nil, nil, in, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(in.Secret)) {
		t.Fatal("la contraseña por defecto quedó sin cifrar")
	}
	entries, _, _, out, err := Open(data, "pw")
	if err != nil || out == nil || *out != *in || !entries[0].UseDefaultCreds {
		t.Fatalf("Open = %+v, %+v, %v", entries, out, err)
	}
	data, _ = Seal([]Entry{{Name: "x", Host: "h"}}, nil, nil, nil, "pw")
	if _, _, _, out, _ := Open(data, "pw"); out != nil {
		t.Fatalf("sin credenciales por defecto, Open = %+v", out)
	}
}

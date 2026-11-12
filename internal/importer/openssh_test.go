package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"osat/internal/store"
)

func TestOpenSSHConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(filepath.Join(sshDir, "config.d"), 0o700); err != nil {
		t.Fatal(err)
	}
	inc := "Host incluido\n  HostName 10.0.0.9\n"
	if err := os.WriteFile(filepath.Join(sshDir, "config.d", "extra.conf"), []byte(inc), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := `# global, antes de cualquier Host
Include config.d/*.conf

Host web web2
    HostName %h.empresa.com
    User deploy
    IdentityFile ~/.ssh/id_web
    ForwardAgent yes

Host db
    HostName=10.0.5.20
    Port 2222
    ProxyJump bastion

Host legacy
    HostName "legacy.empresa.com"
    ProxyCommand ssh -W %h:%p bastion

Host cadena
    ProxyJump admin@gw:2222,otro

Host *.prod !nope.prod
    User produser
    ForwardAgent yes

Host api.prod
    Port 2200

Match host *
    User nadie

Host *
    User root
    Port 22
    IdentityFile none
`
	p := filepath.Join(sshDir, "config")
	if err := os.WriteFile(p, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := Scan(OpenSSH, p, "", []store.Connection{{Protocol: store.ProtoSSH, Host: "10.0.5.20", Port: 2222, User: "root"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Items) != 7 {
		t.Fatalf("items = %+v", r.Items)
	}
	web := byName(t, r.Items, "web")
	if web.Host != "web.empresa.com" || web.User != "deploy" || web.Port != 22 || web.Group != "OpenSSH" ||
		web.AuthType != store.AuthKey || web.KeyPath != filepath.Join(home, ".ssh", "id_web") {
		t.Errorf("web = %+v", web)
	}
	// ForwardAgent yes activa la opción (también heredado de un comodín); sin él, no.
	if !web.ForwardAgent || !byName(t, r.Items, "api.prod").ForwardAgent || byName(t, r.Items, "db").ForwardAgent {
		t.Errorf("ForwardAgent: web=%v api.prod=%v db=%v", web.ForwardAgent, byName(t, r.Items, "api.prod").ForwardAgent, byName(t, r.Items, "db").ForwardAgent)
	}
	if w2 := byName(t, r.Items, "web2"); w2.Host != "web2.empresa.com" {
		t.Errorf("web2 = %+v", w2)
	}
	// Lo de Host * se hereda; el primer valor gana; Match se ignora; sin clave = agente.
	db := byName(t, r.Items, "db")
	if db.Host != "10.0.5.20" || db.Port != 2222 || db.User != "root" || db.AuthType != store.AuthAgent || !db.Exists || db.Jump != "bastion" {
		t.Errorf("db = %+v", db)
	}
	if l := byName(t, r.Items, "legacy"); l.Host != "legacy.empresa.com" {
		t.Errorf("legacy = %+v", l)
	}
	// Comodín con negación: api.prod hereda de *.prod.
	if api := byName(t, r.Items, "api.prod"); api.User != "produser" || api.Port != 2200 || api.Host != "api.prod" {
		t.Errorf("api.prod = %+v", api)
	}
	if i := byName(t, r.Items, "incluido"); i.Host != "10.0.0.9" || i.User != "root" {
		t.Errorf("incluido = %+v", i)
	}
	// Los patrones no son conexiones.
	for _, it := range r.Items {
		if strings.ContainsAny(it.Name, "*?!") {
			t.Errorf("se importó un patrón: %+v", it)
		}
	}
	// Un ProxyJump a un nombre se enlaza al importar; una cadena o usuario@host:puerto, no.
	if c := byName(t, r.Items, "cadena"); c.Jump != "" {
		t.Errorf("cadena = %+v", c)
	}
	want := []string{"cadena: usa ProxyJump admin@gw:2222,otro", "legacy: usa ProxyCommand", "Match host *"}
	all := strings.Join(r.Skipped, "\n")
	for _, w := range want {
		if !strings.Contains(all, w) {
			t.Errorf("falta el aviso %q en %q", w, r.Skipped)
		}
	}
	if strings.Contains(all, "db:") {
		t.Errorf("aviso de más para db: %q", r.Skipped)
	}
}

func TestOpenSSHDefaults(t *testing.T) {
	// Sin User en ningún lado: el usuario de este equipo, como ssh.
	p := write(t, "config", []byte("Host solo\n"))
	r, err := Scan(OpenSSH, p, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if it := byName(t, r.Items, "solo"); it.Host != "solo" || it.User == "" || strings.Contains(it.User, `\`) || it.Port != 22 {
		t.Errorf("solo = %+v", it)
	}
	// Sin ~/.ssh/config: NotFound.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if r, err := Scan(OpenSSH, "", "", nil); err != nil || !r.NotFound {
		t.Errorf("sin config: %+v, %v", r, err)
	}
	// Un archivo sin ningún Host da un error claro.
	if _, err := Scan(OpenSSH, write(t, "vacio", []byte("# nada\n")), "", nil); err == nil {
		t.Error("un config sin Host no dio error")
	}
}

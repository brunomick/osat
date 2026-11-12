package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// winscpPw arma una contraseña guardada por WinSCP (sin contraseña maestra):
// flag, versión, largo, relleno 0 y usuario+host+contraseña, cada byte negado con XOR 0xA3.
func winscpPw(pw, user, host string) string {
	enc := func(b byte) string { return fmt.Sprintf("%02X", ^b^0xA3) }
	s := user + host + pw
	out := enc(0xFF) + enc(0) + enc(byte(len(s))) + enc(0)
	for i := 0; i < len(s); i++ {
		out += enc(s[i])
	}
	return out
}

func TestImportWinSCPSavesPassword(t *testing.T) {
	a := newExportApp(t)
	ini := "[Sessions\\web]\nHostName=10.0.0.5\nUserName=root\nPassword=" + winscpPw("pw-web", "root", "10.0.0.5") +
		"\n\n[Sessions\\sin%20clave]\nHostName=10.0.0.6\nUserName=root\n"
	path := filepath.Join(t.TempDir(), "WinSCP.ini")
	if err := os.WriteFile(path, []byte(ini), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := a.ImportScan("winscp", path, "")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := a.ImportConnections(r.Items); err != nil || n != 2 {
		t.Fatalf("ImportConnections = %d, %v", n, err)
	}
	got := byName(a)
	if web := got["web"]; !web.HasSecret || a.store.Secret(web.ID) != "pw-web" {
		t.Fatalf("web = %+v, secreto %q", web, a.store.Secret(web.ID))
	}
	if got["sin clave"].HasSecret {
		t.Fatal("una sesión sin contraseña quedó con una")
	}
}

func TestImportOpenSSHConfig(t *testing.T) {
	a := newExportApp(t)
	cfg := "Host web\n  HostName 10.0.0.5\n  IdentityFile ~/.ssh/id_web\n  ForwardAgent yes\n\nHost db\n  HostName 10.0.0.6\n  ProxyJump web\n\nHost *\n  User root\n"
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := a.ImportScan("openssh", path, "")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := a.ImportConnections(r.Items); err != nil || n != 2 {
		t.Fatalf("ImportConnections = %d, %v", n, err)
	}
	got := byName(a)
	if web := got["web"]; web.Host != "10.0.0.5" || web.User != "root" || web.AuthType != "key" || web.Group != "OpenSSH" {
		t.Fatalf("web = %+v", web)
	}
	// ForwardAgent yes llega a la conexión guardada; sin él queda desactivado.
	if !got["web"].ForwardAgent || got["db"].ForwardAgent {
		t.Fatalf("ForwardAgent: web=%v db=%v", got["web"].ForwardAgent, got["db"].ForwardAgent)
	}
	// ProxyJump web: queda enlazada a la conexión web importada, sin aviso.
	if db := got["db"]; db.AuthType != "agent" || db.JumpID != got["web"].ID {
		t.Fatalf("db = %+v", db)
	}
	if len(r.Skipped) != 0 {
		t.Fatalf("avisos = %q", r.Skipped)
	}
}

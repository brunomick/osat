package store

import (
	"testing"

	"github.com/zalando/go-keyring"
)

func newDefaultsStore(t *testing.T) *Store {
	t.Helper()
	keyring.MockInit()
	dir := t.TempDir()
	return &Store{dir: dir, path: dir + "/connections.json", conns: map[string]Connection{}}
}

func mustSaveConn(t *testing.T, s *Store, c Connection, secret string) Connection {
	t.Helper()
	c, err := s.Save(ConnectionInput{Connection: c, Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Sin usuario propio se puede guardar solo si hay uno por defecto.
func TestSaveWithoutUserNeedsDefault(t *testing.T) {
	s := newDefaultsStore(t)
	if _, err := s.Save(ConnectionInput{Connection: Connection{Host: "h"}}); err == nil {
		t.Fatal("sin usuario ni usuario por defecto debería fallar")
	}
	if _, err := s.SaveDefaults(DefaultCredsInput{DefaultCreds: DefaultCreds{User: "admin"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(ConnectionInput{Connection: Connection{Host: "h"}}); err != nil {
		t.Fatalf("con usuario por defecto debería guardarse: %v", err)
	}
	if _, err := s.Save(ConnectionInput{Connection: Connection{Host: "h", Protocol: ProtoVNC}}); err != nil {
		t.Fatalf("VNC no lleva usuario: %v", err)
	}
}

func TestDefaultsResolve(t *testing.T) {
	s := newDefaultsStore(t)
	if _, err := s.SaveDefaults(DefaultCredsInput{DefaultCreds: DefaultCreds{User: " admin ", Domain: "CORP"}, Secret: "pw-default"}); err != nil {
		t.Fatal(err)
	}
	flag := mustSaveConn(t, s, Connection{Host: "a", User: "propio", AuthType: AuthKey, KeyPath: "k", UseDefaultCreds: true}, "")
	own := mustSaveConn(t, s, Connection{Host: "b", User: "propio", AuthType: AuthPassword}, "pw-propia")
	empty := mustSaveConn(t, s, Connection{Host: "c", AuthType: AuthPassword}, "")
	key := mustSaveConn(t, s, Connection{Host: "d", User: "propio", AuthType: AuthKey, KeyPath: "k"}, "")
	rdp := mustSaveConn(t, s, Connection{Host: "e", User: "propio", Domain: "OTRO", Protocol: ProtoRDP}, "")
	rdpNoDom := mustSaveConn(t, s, Connection{Host: "f", User: "propio", Protocol: ProtoRDP}, "")
	vnc := mustSaveConn(t, s, Connection{Host: "g", Protocol: ProtoVNC}, "")

	if flag.AuthType != AuthPassword || flag.KeyPath != "" {
		t.Fatalf("SSH con credenciales por defecto debe pasar a contraseña: %+v", flag)
	}
	for _, tc := range []struct {
		name         string
		c            Connection
		user, domain string
		secret       string
	}{
		{"flag", flag, "admin", "", "pw-default"},
		{"propias", own, "propio", "", "pw-propia"},
		{"vacía", empty, "admin", "", "pw-default"},
		{"clave: sin contraseña por defecto", key, "propio", "", ""},
		{"rdp con dominio propio", rdp, "propio", "OTRO", "pw-default"},
		{"rdp sin dominio", rdpNoDom, "propio", "CORP", "pw-default"},
		{"vnc", vnc, "", "", "pw-default"},
	} {
		got := s.WithDefaults(tc.c)
		if got.User != tc.user || got.Domain != tc.domain {
			t.Errorf("%s: WithDefaults = %q / %q, quería %q / %q", tc.name, got.User, got.Domain, tc.user, tc.domain)
		}
		if sec := s.SecretFor(got); sec != tc.secret {
			t.Errorf("%s: SecretFor = %q, quería %q", tc.name, sec, tc.secret)
		}
	}
}

// KeepSecret conserva la contraseña, sin él se borra; y todo sobrevive a recargar el store.
func TestSaveDefaultsSecretAndReload(t *testing.T) {
	s := newDefaultsStore(t)
	if _, err := s.SaveDefaults(DefaultCredsInput{DefaultCreds: DefaultCreds{User: "admin"}, Secret: "x"}); err != nil {
		t.Fatal(err)
	}
	d, err := s.SaveDefaults(DefaultCredsInput{DefaultCreds: DefaultCreds{User: "root", Domain: "D"}, KeepSecret: true})
	if err != nil || !d.HasSecret || s.DefaultSecret() != "x" {
		t.Fatalf("KeepSecret: %+v %v %q", d, err, s.DefaultSecret())
	}
	re := &Store{dir: s.dir}
	if err := re.loadDefaults(); err != nil || re.Defaults() != (DefaultCreds{User: "root", Domain: "D", HasSecret: true}) {
		t.Fatalf("recargado = %+v %v", re.Defaults(), err)
	}
	if d, _ := s.SaveDefaults(DefaultCredsInput{DefaultCreds: DefaultCreds{User: "root"}}); d.HasSecret || s.DefaultSecret() != "" {
		t.Fatal("sin KeepSecret la contraseña debería borrarse")
	}
}

func TestSetUseDefaultCredsByGroup(t *testing.T) {
	s := newDefaultsStore(t)
	a := mustSaveConn(t, s, Connection{Host: "a", User: "u", Group: "Prod", AuthType: AuthKey, KeyPath: "k"}, "")
	b := mustSaveConn(t, s, Connection{Host: "b", User: "u", Group: "Prod/Web", Protocol: ProtoRDP}, "")
	c := mustSaveConn(t, s, Connection{Host: "c", User: "u", Group: "Producción"}, "")

	if n, err := s.SetUseDefaultCreds("Prod", true); err != nil || n != 2 {
		t.Fatalf("SetUseDefaultCreds = %d, %v", n, err)
	}
	got := func(id string) Connection { x, _ := s.Get(id); return x }
	if x := got(a.ID); !x.UseDefaultCreds || x.AuthType != AuthPassword || x.KeyPath != "" {
		t.Fatalf("a = %+v", x)
	}
	if !got(b.ID).UseDefaultCreds || got(c.ID).UseDefaultCreds {
		t.Fatal("solo el grupo y sus subgrupos")
	}
	if n, _ := s.SetUseDefaultCreds("", false); n != 2 {
		t.Fatalf("desmarcar todas cambió %d", n)
	}
}

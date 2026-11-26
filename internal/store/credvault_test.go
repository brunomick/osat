package store

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func rawCred(t *testing.T, id string) string {
	t.Helper()
	v, err := secretGet(keyringService, credKey(id))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func credsCode(err error) string {
	var e *CredsError
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// En el almacén del sistema solo quedan los datos cifrados; la clave está en otro lado (credentials.key).
func TestCredsSealedInStore(t *testing.T) {
	keyring.MockInit()
	s := newTestStore(t)
	a := mustSaveCred(t, s, CredInput{Label: "AWS", Account: "123456789012", User: "root-user", Password: "hunter2"})
	raw := rawCred(t, a.ID)
	for _, secret := range []string{"hunter2", "root-user", "123456789012"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("el almacén tiene %q a la vista: %s", secret, raw)
		}
	}
	key, err := os.ReadFile(s.credKeyPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, string(key)) {
		t.Fatal("la clave está en el almacén")
	}

	// Al reiniciar se lee sin pedir nada.
	n := reloadCreds(t, s)
	if d, err := n.CredData(a.ID); err != nil || d != (CredData{Account: "123456789012", User: "root-user", Password: "hunter2"}) {
		t.Fatalf("después de reiniciar: %+v, %v", d, err)
	}
}

// Las credenciales de antes del cifrado se cifran al arrancar (SealCreds) y, mientras tanto, se siguen leyendo.
func TestSealCredsMigratesPlain(t *testing.T) {
	keyring.MockInit()
	s := newTestStore(t)
	a := mustSaveCred(t, s, CredInput{Label: "AWS", Password: "hunter2"})
	if err := secretSet(keyringService, credKey(a.ID), `{"account":"","user":"u","password":"hunter2"}`); err != nil {
		t.Fatal(err)
	}
	n := reloadCreds(t, s)
	if d, err := n.CredData(a.ID); err != nil || d.Password != "hunter2" {
		t.Fatalf("sin cifrar todavía: %+v, %v", d, err)
	}
	if err := n.SealCreds(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rawCred(t, a.ID), "hunter2") {
		t.Fatal("SealCreds no la cifró")
	}
	if d, err := n.CredData(a.ID); err != nil || d.User != "u" {
		t.Fatalf("cifrada: %+v, %v", d, err)
	}
}

// Sin la clave (se borró, o la carpeta de datos vino de otra PC) las credenciales siguen en la lista y se vuelven a
// cargar editándolas.
func TestCredsLostKey(t *testing.T) {
	keyring.MockInit()
	s := newTestStore(t)
	a := mustSaveCred(t, s, CredInput{Label: "AWS", Domain: "aws.amazon.com", User: "root-user", Password: "hunter2"})
	if err := os.Remove(s.credKeyPath()); err != nil {
		t.Fatal(err)
	}
	n := reloadCreds(t, s)
	if got := n.Creds(); len(got) != 1 || got[0].Domain != "aws.amazon.com" {
		t.Fatalf("lista = %+v", got)
	}
	if _, err := n.CredData(a.ID); credsCode(err) != CodeCredsLost {
		t.Fatalf("perdida: %v", err)
	}
	if _, err := n.SaveCred(CredInput{ID: a.ID, Label: "AWS", Domain: "aws.amazon.com", User: "u", KeepPassword: true}); err == nil {
		t.Fatal("se conservó una contraseña que no se puede leer")
	}
	if _, err := n.SaveCred(CredInput{ID: a.ID, Label: "AWS", Domain: "aws.amazon.com", User: "u", Password: "nueva"}); err != nil {
		t.Fatal(err)
	}
	if d, err := n.CredData(a.ID); err != nil || d.Password != "nueva" {
		t.Fatalf("recargada: %+v, %v", d, err)
	}
}

// Una clave dañada no traba nada: se crea otra (lo cifrado con la vieja se recarga editando).
func TestCredsBrokenKey(t *testing.T) {
	keyring.MockInit()
	s := newTestStore(t)
	a := mustSaveCred(t, s, CredInput{Label: "AWS", Password: "hunter2"})
	if err := os.WriteFile(s.credKeyPath(), []byte("basura"), 0o600); err != nil {
		t.Fatal(err)
	}
	n := reloadCreds(t, s)
	if _, err := n.CredData(a.ID); credsCode(err) != CodeCredsLost {
		t.Fatalf("con la clave dañada: %v", err)
	}
	b := mustSaveCred(t, n, CredInput{Label: "Hosting", Password: "pw-hosting"})
	if d, err := reloadCreds(t, n).CredData(b.ID); err != nil || d.Password != "pw-hosting" {
		t.Fatalf("con la clave nueva: %+v, %v", d, err)
	}
}

func TestProtectKeyRoundTrip(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	p, err := protectKey(key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := unprotectKey(p)
	if err != nil || string(got) != string(key) {
		t.Fatalf("%q, %v", got, err)
	}
}

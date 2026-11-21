package store

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func mustSaveCred(t *testing.T, s *Store, in CredInput) Cred {
	t.Helper()
	c, err := s.SaveCred(in)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func credLabels(cs []Cred) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Label)
	}
	return out
}

// reloadCreds lee de nuevo credentials.json, como al reiniciar la app.
func reloadCreds(t *testing.T, s *Store) *Store {
	t.Helper()
	n := &Store{dir: s.dir, path: s.path, conns: map[string]Connection{}}
	if err := n.loadCreds(); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCredsRoundTrip(t *testing.T) {
	keyring.MockInit()
	s := newTestStore(t)
	a := mustSaveCred(t, s, CredInput{Label: " AWS prod ", Account: "123456789012", User: "root-user", Password: "hunter2"})
	b := mustSaveCred(t, s, CredInput{Label: "Hosting", User: "viewer", Password: "pw-viewer"})
	if a.Label != "AWS prod" || a.ID == "" || b.ID == a.ID {
		t.Fatalf("credenciales = %+v, %+v", a, b)
	}
	got, err := s.CredData(a.ID)
	if err != nil || got != (CredData{Account: "123456789012", User: "root-user", Password: "hunter2"}) {
		t.Fatalf("CredData = %+v, %v", got, err)
	}
	if l := credLabels(reloadCreds(t, s).Creds()); !reflect.DeepEqual(l, []string{"AWS prod", "Hosting"}) {
		t.Fatalf("etiquetas recargadas = %q", l)
	}
	raw, err := os.ReadFile(s.credsPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"hunter2", "root-user", "123456789012", "viewer", "pw-viewer"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("credentials.json contiene %q:\n%s", secret, raw)
		}
	}
}

func TestCredEditKeepPassword(t *testing.T) {
	keyring.MockInit()
	s := newTestStore(t)
	c := mustSaveCred(t, s, CredInput{Label: "admin", User: "a", Password: "vieja"})
	mustSaveCred(t, s, CredInput{ID: c.ID, Label: "admin2", Account: "acc", User: "b", KeepPassword: true})
	if got, _ := s.CredData(c.ID); got != (CredData{Account: "acc", User: "b", Password: "vieja"}) {
		t.Fatalf("con KeepPassword = %+v", got)
	}
	if l := credLabels(s.Creds()); !reflect.DeepEqual(l, []string{"admin2"}) {
		t.Fatalf("etiquetas = %q", l)
	}
	mustSaveCred(t, s, CredInput{ID: c.ID, Label: "admin2", User: "b", Password: "nueva"})
	if got, _ := s.CredData(c.ID); got.Password != "nueva" || got.Account != "" {
		t.Fatalf("sin KeepPassword = %+v", got)
	}
	// Sin KeepPassword y sin contraseña: se vacía a propósito.
	mustSaveCred(t, s, CredInput{ID: c.ID, Label: "admin2", User: "b"})
	if got, _ := s.CredData(c.ID); got.Password != "" {
		t.Fatalf("no se vació la contraseña: %+v", got)
	}
}

func TestCredLabelRules(t *testing.T) {
	keyring.MockInit()
	s := newTestStore(t)
	if _, err := s.SaveCred(CredInput{Label: "  ", User: "x"}); err == nil {
		t.Fatal("aceptó una etiqueta vacía")
	}
	a := mustSaveCred(t, s, CredInput{Label: "admin"})
	if _, err := s.SaveCred(CredInput{Label: "ADMIN"}); err == nil {
		t.Fatal("aceptó una etiqueta repetida")
	}
	b := mustSaveCred(t, s, CredInput{Label: "otro"})
	if _, err := s.SaveCred(CredInput{ID: b.ID, Label: "admin"}); err == nil {
		t.Fatal("renombró a una etiqueta repetida")
	}
	// Guardar la misma credencial con su propia etiqueta no es repetirla.
	mustSaveCred(t, s, CredInput{ID: a.ID, Label: "admin", User: "u"})
	if _, err := s.SaveCred(CredInput{ID: "nope", Label: "x"}); err == nil {
		t.Fatal("editó una credencial inexistente")
	}
	if _, err := s.CredData("nope"); err == nil {
		t.Fatal("leyó una credencial inexistente")
	}
}

func TestDeleteCredRemovesSecret(t *testing.T) {
	keyring.MockInit()
	s := newTestStore(t)
	a := mustSaveCred(t, s, CredInput{Label: "a", Password: "x"})
	mustSaveCred(t, s, CredInput{Label: "b", Password: "y"})
	if err := s.DeleteCred(a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := secretGet(keyringService, credKey(a.ID)); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("la credencial borrada sigue en el keyring: %v", err)
	}
	if l := credLabels(reloadCreds(t, s).Creds()); !reflect.DeepEqual(l, []string{"b"}) {
		t.Fatalf("etiquetas = %q", l)
	}
}

func TestImportCredsSkipsSameLabel(t *testing.T) {
	keyring.MockInit()
	s := newTestStore(t)
	mustSaveCred(t, s, CredInput{Label: "admin", Password: "local"})
	in := []CredInput{{Label: "admin", Password: "importada"}, {Label: "root", Account: "acc", User: "u", Password: "p"}}
	n, err := s.ImportCreds(in)
	if n != 1 || err == nil || !strings.Contains(err.Error(), "admin") {
		t.Fatalf("ImportCreds = %d, %v (quería 1 y el aviso de la salteada)", n, err)
	}
	cs := s.Creds()
	if got, _ := s.CredData(cs[0].ID); got.Password != "local" {
		t.Fatalf("la importación pisó la credencial local: %+v", got)
	}
	if got, _ := s.CredData(cs[1].ID); got != (CredData{Account: "acc", User: "u", Password: "p"}) {
		t.Fatalf("importada = %+v", got)
	}
	// Importar de nuevo no duplica nada.
	if n, _ := s.ImportCreds(in); n != 0 || len(s.Creds()) != 2 {
		t.Fatalf("la segunda importación agregó %d: %+v", n, s.Creds())
	}
}

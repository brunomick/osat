package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestMigrateLegacyDir(t *testing.T) {
	base := t.TempDir()
	old := filepath.Join(base, legacyDirName)
	dir := filepath.Join(base, "osat")
	os.MkdirAll(old, 0o700)
	os.WriteFile(filepath.Join(old, "connections.json"), []byte(`{"version":1}`), 0o600)

	migrateLegacyDir(old, dir)
	if _, err := os.Stat(filepath.Join(dir, "connections.json")); err != nil {
		t.Fatalf("no se migró connections.json: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("la carpeta vieja sigue existiendo: %v", err)
	}
}

// Si la carpeta nueva ya existe no se toca nada (no pisar datos nuevos).
func TestMigrateLegacyDirKeepsExisting(t *testing.T) {
	base := t.TempDir()
	old := filepath.Join(base, legacyDirName)
	dir := filepath.Join(base, "osat")
	os.MkdirAll(old, 0o700)
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(old, "connections.json"), []byte("viejo"), 0o600)
	os.WriteFile(filepath.Join(dir, "connections.json"), []byte("nuevo"), 0o600)

	migrateLegacyDir(old, dir)
	if b, _ := os.ReadFile(filepath.Join(dir, "connections.json")); string(b) != "nuevo" {
		t.Fatalf("se pisaron los datos nuevos: %q", b)
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("la carpeta vieja debería quedar intacta: %v", err)
	}
}

func TestSecretMigratesLegacyKeyring(t *testing.T) {
	keyring.MockInit()
	keyring.Set(legacyKeyringService, "c1", "clave-vieja")

	s := &Store{}
	if got := s.Secret("c1"); got != "clave-vieja" {
		t.Fatalf("Secret = %q", got)
	}
	if v, err := keyring.Get(keyringService, "c1"); err != nil || v != "clave-vieja" {
		t.Fatalf("no se copió al servicio nuevo: %q %v", v, err)
	}
	if _, err := keyring.Get(legacyKeyringService, "c1"); err == nil {
		t.Fatal("el secreto viejo debería haberse borrado")
	}
	if got := s.Secret("inexistente"); got != "" {
		t.Fatalf("Secret de una conexión sin secreto = %q", got)
	}
}

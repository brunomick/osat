package importer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"osat/internal/store"
)

// testdata/compass-encrypted.json se generó cifrando como Compass
// (packages/connection-storage/src/encrypt.ts, con node:crypto) con la
// passphrase "frase de prueba". Tiene dos conexiones: un replica set y una que
// pasa por un túnel SSH con contraseña.
const compassPassphrase = "frase de prueba"

func TestCompassEncrypted(t *testing.T) {
	path := "testdata/compass-encrypted.json"

	// Sin passphrase: se pide (como la contraseña maestra de WinSCP).
	if _, err := Scan(Compass, path, "", nil); !errors.Is(err, ErrMasterPassword) {
		t.Fatalf("sin passphrase: %v", err)
	}
	if _, err := Scan(Compass, path, "otra", nil); !errors.Is(err, ErrBadMasterPassword) {
		t.Fatalf("passphrase equivocada: %v", err)
	}

	r, err := Scan(Compass, path, compassPassphrase, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Items) != 3 {
		t.Fatalf("items = %+v", r.Items)
	}
	ventas := byName(t, r.Items, "Ventas (prod)")
	if ventas.Protocol != store.ProtoMongo || ventas.Group != "Compass" || ventas.User != "lector" || ventas.Host != "db1.example.com" || ventas.Port != 27017 ||
		ventas.URI != "mongodb://lector@db1.example.com:27017,db2.example.com:27018/ventas?replicaSet=rs0&authSource=admin" {
		t.Fatalf("ventas = %+v", ventas)
	}
	if ventas.Secret != "p@ss:w/rd" || !ventas.HasSecret || ventas.Jump != "" {
		t.Fatalf("ventas: secreto o salto = %+v", ventas)
	}

	interna := byName(t, r.Items, "Interna")
	bastion := byName(t, r.Items, "ops@bastion.example.com")
	if interna.Jump != bastion.Name || interna.Secret != "pw-interna" || interna.URI != "mongodb://analista@10.0.0.5/?directConnection=true" {
		t.Fatalf("interna = %+v", interna)
	}
	if bastion.Protocol != store.ProtoSSH || bastion.Host != "bastion.example.com" || bastion.Port != 2222 || bastion.User != "ops" ||
		bastion.AuthType != store.AuthPassword || bastion.Secret != "pw-bastion" {
		t.Fatalf("bastion = %+v", bastion)
	}
}

// Sin passphrase, Compass deja las contraseñas en la cadena y en el túnel.
func TestCompassPlain(t *testing.T) {
	path := write(t, "compass.json", []byte(`{
  "type": "Compass Connections",
  "version": 1,
  "connections": [
    {"id": "a", "favorite": {"name": "Local"}, "connectionOptions": {"connectionString": "mongodb://localhost:27017"}},
    {"id": "b", "connectionOptions": {"connectionString": "mongodb+srv://u:clave%21@cluster0.abcd.mongodb.net/?retryWrites=true&authMechanism=MONGODB-OIDC"}},
    {"id": "c", "favorite": {"name": "Con clave"}, "connectionOptions": {"connectionString": "mongodb://x@10.0.0.9",
      "sshTunnel": {"host": "jump.example.com", "port": {"$numberInt": "22"}, "username": "deploy", "identityKeyFile": "C:\\keys\\id_ed25519", "identityKeyPassphrase": "frase"}}},
    {"id": "d", "favorite": {"name": "Otra por el mismo"}, "connectionOptions": {"connectionString": "mongodb://y@10.0.0.10",
      "sshTunnel": {"host": "JUMP.example.com", "port": 22, "username": "deploy"}}},
    {"id": "e", "favorite": {"name": "Rota"}, "connectionOptions": {"connectionString": "http://nada"}},
    {"id": "f", "favorite": {"name": "Ya existe"}, "connectionOptions": {"connectionString": "mongodb://z@10.0.0.11",
      "sshTunnel": {"host": "bastion.local", "port": 22, "username": "root"}}}
  ]
}`))
	existing := []store.Connection{{Name: "mi bastión", Protocol: store.ProtoSSH, Host: "bastion.local", Port: 22, User: "root"}}
	r, err := Scan(Compass, path, "", existing)
	if err != nil {
		t.Fatal(err)
	}
	local := byName(t, r.Items, "Local")
	if local.User != "" || local.Secret != "" || local.URI != "mongodb://localhost:27017" {
		t.Fatalf("local = %+v", local)
	}
	srv := byName(t, r.Items, "cluster0.abcd.mongodb.net") // sin nombre: el host
	if srv.Secret != "clave!" || srv.URI != "mongodb+srv://u@cluster0.abcd.mongodb.net/?retryWrites=true&authMechanism=MONGODB-OIDC" {
		t.Fatalf("srv = %+v", srv)
	}
	// Un solo Item SSH para los dos que usan el mismo túnel, con la clave.
	jump := byName(t, r.Items, "deploy@jump.example.com")
	if jump.AuthType != store.AuthKey || jump.KeyPath != `C:\keys\id_ed25519` || jump.Secret != "frase" || !jump.HasKey {
		t.Fatalf("jump = %+v", jump)
	}
	if a, b := byName(t, r.Items, "Con clave"), byName(t, r.Items, "Otra por el mismo"); a.Jump != jump.Name || b.Jump != jump.Name {
		t.Fatalf("saltos: %q %q", a.Jump, b.Jump)
	}
	// El túnel que ya está en osat se reusa por su nombre (no se crea otro).
	if it := byName(t, r.Items, "Ya existe"); it.Jump != "mi bastión" {
		t.Fatalf("ya existe: %+v", it)
	}
	if len(r.Items) != 6 {
		t.Fatalf("items = %+v", r.Items)
	}
	joined := strings.Join(r.Skipped, "\n")
	if !strings.Contains(joined, "Rota") || !strings.Contains(joined, "OIDC") {
		t.Fatalf("avisos = %q", r.Skipped)
	}
}

func TestCompassRejectsOtherFiles(t *testing.T) {
	for name, body := range map[string]string{
		"no-json":      "hola",
		"otro-tipo":    `{"type": "Algo", "version": 1, "connections": []}`,
		"otra-version": `{"type": "Compass Connections", "version": {"$numberInt": "2"}, "connections": []}`,
	} {
		if _, err := Scan(Compass, write(t, name+".json", []byte(body)), "", nil); err == nil {
			t.Errorf("%s: debería fallar", name)
		}
	}
	// Datos cifrados dañados.
	b, err := os.ReadFile("testdata/compass-encrypted.json")
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(string(b), `"connectionSecrets": "AA`, `"connectionSecrets": "AB`, 1) // otra versión de cifrado
	if broken == string(b) {
		t.Fatal("el archivo de prueba no tiene connectionSecrets")
	}
	if _, err := Scan(Compass, write(t, "roto.json", []byte(broken)), compassPassphrase, nil); err == nil || errors.Is(err, ErrBadMasterPassword) {
		t.Fatalf("datos dañados: %v", err)
	}
}

// fakeCompassDir hace que las conexiones "de esta PC" se lean de una carpeta de prueba.
func fakeCompassDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev := compassDataDir
	compassDataDir = func() string { return dir }
	t.Cleanup(func() { compassDataDir = prev })
	return dir
}

func writeCompassConn(t *testing.T, dir, id, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "Connections"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Connections", id+".json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Sin Compass en esta PC: no se encontró nada (se puede elegir un archivo exportado).
func TestCompassLocalNotFound(t *testing.T) {
	fakeCompassDir(t)
	if r, err := Scan(Compass, "", "", nil); err != nil || !r.NotFound {
		t.Fatalf("sin Compass: %+v %v", r, err)
	}
}

// Las guardadas en esta PC: favoritas en "Compass", recientes aparte, y las
// de versiones viejas (sin connectionInfo) se ignoran.
func TestCompassLocalWithoutSecrets(t *testing.T) {
	dir := fakeCompassDir(t)
	writeCompassConn(t, dir, "a", `{"_id": "a", "connectionInfo": {"id": "a", "savedConnectionType": "favorite", "favorite": {"name": "Ventas"},
		"connectionOptions": {"connectionString": "mongodb://lector@db.example.com/ventas"}}, "version": 1}`)
	writeCompassConn(t, dir, "b", `{"_id": "b", "connectionInfo": {"id": "b", "savedConnectionType": "recent",
		"connectionOptions": {"connectionString": "mongodb://localhost:27017"}}, "version": 1}`)
	writeCompassConn(t, dir, "c", `{"_id": "c", "name": "vieja", "isFavorite": true}`)
	writeCompassConn(t, dir, "d", `no es json`)
	r, err := Scan(Compass, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Items) != 2 || r.Location != filepath.Join(dir, "Connections") {
		t.Fatalf("resultado = %+v", r)
	}
	if v := byName(t, r.Items, "Ventas"); v.Group != "Compass" || v.URI != "mongodb://lector@db.example.com/ventas" {
		t.Fatalf("ventas = %+v", v)
	}
	if l := byName(t, r.Items, "localhost"); l.Group == "Compass" {
		t.Fatalf("la reciente debería ir en otro grupo: %+v", l)
	}
	if len(r.Skipped) != 1 || !strings.Contains(r.Skipped[0], "d.json") {
		t.Fatalf("avisos = %q", r.Skipped)
	}
}

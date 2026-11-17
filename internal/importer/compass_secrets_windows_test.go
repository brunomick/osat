//go:build windows

package importer

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"osat/internal/store"
)

func dpapiEncrypt(t *testing.T, b []byte) []byte {
	t.Helper()
	in := windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		t.Fatal(err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return bytes.Clone(unsafe.Slice(out.Data, out.Size))
}

// fakeSafeStorage arma una carpeta de Compass como la deja Electron: la clave
// en "Local State" protegida con DPAPI, y devuelve cómo cifrar un dato ("v10").
func fakeSafeStorage(t *testing.T, dir string) func(plain string) string {
	t.Helper()
	key := make([]byte, 32)
	rand.Read(key)
	enc := base64.StdEncoding.EncodeToString(append([]byte("DPAPI"), dpapiEncrypt(t, key)...))
	if err := os.WriteFile(filepath.Join(dir, "Local State"), []byte(`{"os_crypt":{"encrypted_key":"`+enc+`"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return func(plain string) string {
		block, _ := aes.NewCipher(key)
		gcm, _ := cipher.NewGCM(block)
		nonce := make([]byte, 12)
		rand.Read(nonce)
		out := append(append([]byte("v10"), nonce...), gcm.Seal(nil, nonce, []byte(plain), nil)...)
		return base64.StdEncoding.EncodeToString(out)
	}
}

// Las contraseñas que guarda Compass se descifran con la clave de safeStorage:
// la de la base y la del túnel SSH, que se vuelve una conexión de salto.
func TestCompassLocalSecrets(t *testing.T) {
	dir := fakeCompassDir(t)
	seal := fakeSafeStorage(t, dir)
	writeCompassConn(t, dir, "a", `{"_id": "a", "version": 1, "connectionInfo": {"id": "a", "savedConnectionType": "favorite", "favorite": {"name": "Interna"},
		"connectionOptions": {"connectionString": "mongodb://analista@10.0.0.5/?directConnection=true",
		"sshTunnel": {"host": "bastion.example.com", "port": 2222, "username": "ops"}}},
		"connectionSecrets": "`+seal(`{"password":"pw-interna","sshTunnelPassword":"pw-bastion"}`)+`"}`)
	// Un dato viejo, protegido directamente con DPAPI (sin "v10").
	old := base64.StdEncoding.EncodeToString(dpapiEncrypt(t, []byte(`{"password":"pw-vieja"}`)))
	writeCompassConn(t, dir, "b", `{"_id": "b", "version": 1, "connectionInfo": {"id": "b", "favorite": {"name": "Vieja"},
		"connectionOptions": {"connectionString": "mongodb://u@h1"}}, "connectionSecrets": "`+old+`"}`)
	// Uno dañado: se importa sin contraseña y con aviso.
	writeCompassConn(t, dir, "c", `{"_id": "c", "version": 1, "connectionInfo": {"id": "c", "favorite": {"name": "Rota"},
		"connectionOptions": {"connectionString": "mongodb://u@h2"}}, "connectionSecrets": "`+base64.StdEncoding.EncodeToString([]byte("v10basura-basura-basura-basura"))+`"}`)

	r, err := Scan(Compass, "", "", []store.Connection{})
	if err != nil {
		t.Fatal(err)
	}
	interna := byName(t, r.Items, "Interna")
	bastion := byName(t, r.Items, "ops@bastion.example.com")
	if interna.Secret != "pw-interna" || !interna.HasSecret || interna.Jump != bastion.Name {
		t.Fatalf("interna = %+v", interna)
	}
	if bastion.Secret != "pw-bastion" || bastion.Port != 2222 {
		t.Fatalf("bastion = %+v", bastion)
	}
	if v := byName(t, r.Items, "Vieja"); v.Secret != "pw-vieja" {
		t.Fatalf("vieja = %+v", v)
	}
	if rota := byName(t, r.Items, "Rota"); rota.Secret != "" {
		t.Fatalf("rota = %+v", rota)
	}
	if joined := strings.Join(r.Skipped, "\n"); !strings.Contains(joined, "Rota") {
		t.Fatalf("avisos = %q", r.Skipped)
	}
}

// Una clave que no es de este usuario (o está dañada): todo se importa sin contraseñas, con un aviso.
func TestCompassLocalBadKey(t *testing.T) {
	dir := fakeCompassDir(t)
	os.WriteFile(filepath.Join(dir, "Local State"), []byte(`{"os_crypt":{"encrypted_key":"`+base64.StdEncoding.EncodeToString([]byte("DPAPIbasura"))+`"}}`), 0o600)
	writeCompassConn(t, dir, "a", `{"_id": "a", "connectionInfo": {"id": "a", "favorite": {"name": "X"},
		"connectionOptions": {"connectionString": "mongodb://u@h"}}, "connectionSecrets": "djEwYWJj"}`)
	r, err := Scan(Compass, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if x := byName(t, r.Items, "X"); x.Secret != "" || len(r.Skipped) != 1 {
		t.Fatalf("%+v %q", x, r.Skipped)
	}
}

package remote

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"

	"osat/internal/store"
)

// Las claves de testdata/*.ppk vienen de los tests de github.com/kayrus/putty
// (Apache-2.0). Las cifradas usan la passphrase "testkey".
func TestPPKKeys(t *testing.T) {
	cases := []struct {
		file      string
		encrypted bool
	}{
		{"ed25519-v2-enc.ppk", true},
		{"rsa-v3.ppk", false},
		{"rsa-v3-enc.ppk", true},
	}
	kbd := func(string) ssh.AuthMethod { return ssh.Password("") }
	code := func(err error) string {
		var ke *Error
		if errors.As(err, &ke) {
			return ke.Code
		}
		return ""
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			path := filepath.Join("testdata", tc.file)
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !isPPK(b) {
				t.Fatal("isPPK = false")
			}
			c := store.Connection{AuthType: store.AuthKey, KeyPath: path}

			if tc.encrypted {
				if _, _, err := authMethods(c, "", kbd); code(err) != CodeNeedSecret {
					t.Fatalf("sin passphrase: err = %v", err)
				}
				if _, _, err := authMethods(c, "otra", kbd); code(err) != CodeAuthFailed {
					t.Fatalf("passphrase incorrecta: err = %v", err)
				}
			}
			pass := ""
			if tc.encrypted {
				pass = "testkey"
			}
			signer, err := parsePPK(b, pass)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := authMethods(c, pass, kbd); err != nil {
				t.Fatal(err)
			}
			// El firmante tiene que firmar y verificar con su clave pública.
			sig, err := signer.Sign(nil, []byte("hola"))
			if err != nil {
				t.Fatal(err)
			}
			if err := signer.PublicKey().Verify([]byte("hola"), sig); err != nil {
				t.Fatalf("firma inválida: %v", err)
			}
		})
	}

	// Una clave OpenSSH no se toma por .ppk.
	if isPPK([]byte("-----BEGIN OPENSSH PRIVATE KEY-----\n")) {
		t.Fatal("isPPK con una clave OpenSSH")
	}
	if !isPPK(append([]byte{0xEF, 0xBB, 0xBF}, bytes.TrimSpace([]byte("PuTTY-User-Key-File-3: ssh-rsa"))...)) {
		t.Fatal("isPPK no ignora el BOM")
	}
}

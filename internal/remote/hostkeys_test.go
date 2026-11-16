package remote

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func pubKey(t *testing.T, k any) ssh.PublicKey {
	t.Helper()
	p, err := ssh.NewPublicKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHostKeyAlgorithms(t *testing.T) {
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	rk, _ := rsa.GenerateKey(rand.Reader, 2048)
	ed, _, _ := ed25519.GenerateKey(rand.Reader)

	path := filepath.Join(t.TempDir(), "known_hosts")
	lines := []string{
		knownhosts.Line([]string{knownhosts.Normalize("ecdsa.test:2223")}, pubKey(t, &ec.PublicKey)),
		knownhosts.Line([]string{knownhosts.Normalize("rsa.test:22")}, pubKey(t, &rk.PublicKey)),
		// Host hasheado (HashKnownHosts de OpenSSH) con clave ed25519.
		knownhosts.Line([]string{knownhosts.HashHostname(knownhosts.Normalize("hashed.test:22"))}, pubKey(t, ed)),
	}
	os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	hk, err := NewHostKeys(path)
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string][]string{
		"desconocido.test:22": preferredHostKeyAlgos,
		"ecdsa.test:2223":     {ssh.KeyAlgoECDSA256},
		"rsa.test:22":         {ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256},
		"hashed.test:22":      {ssh.KeyAlgoED25519},
	}
	for host, want := range cases {
		if got := hk.Algorithms(host); !reflect.DeepEqual(got, want) {
			t.Errorf("Algorithms(%s) = %v; quería %v", host, got, want)
		}
	}
	if preferredHostKeyAlgos[0] != ssh.KeyAlgoED25519 {
		t.Error("ed25519 debería ser el preferido para hosts nuevos")
	}
}

func TestLsMode(t *testing.T) {
	cases := map[os.FileMode]string{
		0o644:                    "-rw-r--r--",
		os.ModeDir | 0o755:       "drwxr-xr-x",
		os.ModeSymlink | 0o777:   "lrwxrwxrwx",
		os.ModeNamedPipe | 0o600: "prw-------",
		os.ModeDevice | os.ModeCharDevice | 0o666: "crw-rw-rw-",
	}
	for m, want := range cases {
		if got := lsMode(m); got != want {
			t.Errorf("lsMode(%v) = %q; quería %q", m, got, want)
		}
	}
}

// Trust solo acepta la clave cuya huella vio el usuario: si otra conexión pisó la pendiente
// mientras el diálogo estaba abierto, no se guarda ninguna.
func TestTrustRequiresShownFingerprint(t *testing.T) {
	seen, _, _ := ed25519.GenerateKey(rand.Reader)
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	path := filepath.Join(t.TempDir(), "known_hosts")
	hk, err := NewHostKeys(path)
	if err != nil {
		t.Fatal(err)
	}
	const host = "srv.test:22"
	shown := ssh.FingerprintSHA256(pubKey(t, seen))

	hk.pending[host] = pubKey(t, other) // la clave que llegó después, de otro intento
	if err := hk.Trust(host, shown); err == nil {
		t.Fatal("se aceptó una clave distinta de la que se mostró")
	}
	if err := hk.Trust(host, ""); err == nil {
		t.Fatal("se aceptó sin huella")
	}
	if b, _ := os.ReadFile(path); len(b) != 0 {
		t.Fatalf("known_hosts no debería haber cambiado: %q", b)
	}

	hk.pending[host] = pubKey(t, seen)
	if err := hk.Trust(host, shown); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "ssh-ed25519") {
		t.Fatalf("falta la clave en known_hosts: %q", b)
	}
}

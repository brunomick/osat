package remote

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"osat/internal/store"
)

func newSigner(t *testing.T) (ssh.Signer, ed25519.PrivateKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s, priv
}

// signUserCert firma con la CA un certificado de usuario "u" para pub.
func signUserCert(t *testing.T, ca ssh.Signer, pub ssh.PublicKey) *ssh.Certificate {
	t.Helper()
	cert := &ssh.Certificate{
		Key: pub, Serial: 1, CertType: ssh.UserCert, KeyId: "test",
		ValidPrincipals: []string{"u"}, ValidBefore: ssh.CertTimeInfinity,
	}
	if err := cert.SignCert(rand.Reader, ca); err != nil {
		t.Fatal(err)
	}
	return cert
}

// certServer es un servidor SSH que solo acepta certificados firmados por ca:
// las claves sueltas se rechazan.
func certServer(t *testing.T, ca ssh.PublicKey) string {
	t.Helper()
	hostKey, _ := newSigner(t)
	checker := &ssh.CertChecker{
		IsUserAuthority: func(auth ssh.PublicKey) bool { return bytes.Equal(auth.Marshal(), ca.Marshal()) },
	}
	cfg := &ssh.ServerConfig{PublicKeyCallback: checker.Authenticate}
	cfg.AddHostKey(hostKey)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer nc.Close()
				_, chans, reqs, err := ssh.NewServerConn(nc, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for nch := range chans {
					nch.Reject(ssh.UnknownChannelType, "solo autenticación")
				}
			}()
		}
	}()
	return ln.Addr().String()
}

// loginWith intenta autenticarse con los métodos dados; devuelve el error del login.
func loginWith(addr string, methods []ssh.AuthMethod) error {
	c, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User: "u", Auth: methods, HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err == nil {
		c.Close()
	}
	return err
}

// writeKey guarda priv como clave OpenSSH sin passphrase y devuelve la ruta.
func writeKey(t *testing.T, dir string, priv ed25519.PrivateKey) string {
	t.Helper()
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUserCertificate(t *testing.T) {
	ca, _ := newSigner(t)
	addr := certServer(t, ca.PublicKey())
	kbd := func(string) ssh.AuthMethod { return ssh.Password("") }

	t.Run("con -cert.pub entra", func(t *testing.T) {
		dir := t.TempDir()
		signer, priv := newSigner(t)
		path := writeKey(t, dir, priv)
		cert := signUserCert(t, ca, signer.PublicKey())
		if err := os.WriteFile(path+"-cert.pub", ssh.MarshalAuthorizedKey(cert), 0o644); err != nil {
			t.Fatal(err)
		}
		methods, _, err := authMethods(store.Connection{AuthType: store.AuthKey, KeyPath: path}, "", kbd)
		if err != nil {
			t.Fatal(err)
		}
		if err := loginWith(addr, methods); err != nil {
			t.Fatalf("no entró con el certificado: %v", err)
		}
	})

	t.Run("sin -cert.pub no entra", func(t *testing.T) {
		_, priv := newSigner(t)
		path := writeKey(t, t.TempDir(), priv)
		methods, _, err := authMethods(store.Connection{AuthType: store.AuthKey, KeyPath: path}, "", kbd)
		if err != nil {
			t.Fatal(err)
		}
		if err := loginWith(addr, methods); err == nil {
			t.Fatal("entró con una clave suelta")
		}
	})

	t.Run("certificado de otra clave", func(t *testing.T) {
		dir := t.TempDir()
		other, _ := newSigner(t)
		_, priv := newSigner(t)
		path := writeKey(t, dir, priv)
		cert := signUserCert(t, ca, other.PublicKey())
		certPath := path + "-cert.pub"
		if err := os.WriteFile(certPath, ssh.MarshalAuthorizedKey(cert), 0o644); err != nil {
			t.Fatal(err)
		}
		_, _, err := authMethods(store.Connection{AuthType: store.AuthKey, KeyPath: path}, "", kbd)
		if err == nil || !strings.Contains(err.Error(), certPath) {
			t.Fatalf("err = %v, quería un error que nombre %s", err, certPath)
		}
	})

	t.Run("certificado dañado", func(t *testing.T) {
		dir := t.TempDir()
		_, priv := newSigner(t)
		path := writeKey(t, dir, priv)
		certPath := path + "-cert.pub"
		if err := os.WriteFile(certPath, []byte("esto no es un certificado\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, _, err := authMethods(store.Connection{AuthType: store.AuthKey, KeyPath: path}, "", kbd)
		if err == nil || !strings.Contains(err.Error(), certPath) {
			t.Fatalf("err = %v, quería un error que nombre %s", err, certPath)
		}
	})

	t.Run("-cert.pub con una clave común", func(t *testing.T) {
		dir := t.TempDir()
		signer, priv := newSigner(t)
		path := writeKey(t, dir, priv)
		certPath := path + "-cert.pub"
		if err := os.WriteFile(certPath, ssh.MarshalAuthorizedKey(signer.PublicKey()), 0o644); err != nil {
			t.Fatal(err)
		}
		_, _, err := authMethods(store.Connection{AuthType: store.AuthKey, KeyPath: path}, "", kbd)
		if err == nil || !strings.Contains(err.Error(), certPath) {
			t.Fatalf("err = %v, quería un error que nombre %s", err, certPath)
		}
	})
}

func TestUserCertificatePPK(t *testing.T) {
	ca, _ := newSigner(t)
	addr := certServer(t, ca.PublicKey())
	kbd := func(string) ssh.AuthMethod { return ssh.Password("") }

	// copia la .ppk cifrada de testdata a una carpeta temporal y arma su certificado.
	setup := func(t *testing.T) (path string, cert []byte) {
		t.Helper()
		b, err := os.ReadFile(filepath.Join("testdata", "ed25519-v2-enc.ppk"))
		if err != nil {
			t.Fatal(err)
		}
		path = filepath.Join(t.TempDir(), "server.ppk")
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
		signer, err := parsePPK(b, "testkey")
		if err != nil {
			t.Fatal(err)
		}
		return path, ssh.MarshalAuthorizedKey(signUserCert(t, ca, signer.PublicKey()))
	}
	conn := func(path string) store.Connection {
		return store.Connection{AuthType: store.AuthKey, KeyPath: path}
	}

	for _, name := range []string{"server.ppk-cert.pub", "server-cert.pub"} {
		t.Run(name+" entra", func(t *testing.T) {
			path, cert := setup(t)
			if err := os.WriteFile(filepath.Join(filepath.Dir(path), name), cert, 0o644); err != nil {
				t.Fatal(err)
			}
			methods, _, err := authMethods(conn(path), "testkey", kbd)
			if err != nil {
				t.Fatal(err)
			}
			if err := loginWith(addr, methods); err != nil {
				t.Fatalf("no entró con el certificado: %v", err)
			}
		})
	}

	t.Run("sin certificado no entra", func(t *testing.T) {
		path, _ := setup(t)
		methods, _, err := authMethods(conn(path), "testkey", kbd)
		if err != nil {
			t.Fatal(err)
		}
		if err := loginWith(addr, methods); err == nil {
			t.Fatal("entró con una clave suelta")
		}
	})

	t.Run("server.ppk-cert.pub tiene prioridad", func(t *testing.T) {
		path, cert := setup(t)
		dir := filepath.Dir(path)
		bad := filepath.Join(dir, "server.ppk-cert.pub")
		os.WriteFile(bad, []byte("dañado\n"), 0o644)
		os.WriteFile(filepath.Join(dir, "server-cert.pub"), cert, 0o644)
		_, _, err := authMethods(conn(path), "testkey", kbd)
		if err == nil || !strings.Contains(err.Error(), bad) {
			t.Fatalf("err = %v, quería un error que nombre %s", err, bad)
		}
	})
}

// R1.4: los certificados que el agente tiene cargados se ofrecen al servidor.
func TestAgentCertificate(t *testing.T) {
	ca, _ := newSigner(t)
	addr := certServer(t, ca.PublicKey())
	kbd := func(string) ssh.AuthMethod { return ssh.Password("") }

	orig := newAgent
	t.Cleanup(func() { newAgent = orig })
	use := func(k agent.Agent) {
		newAgent = func() (agent.Agent, net.Conn, error) { return k, nil, nil }
	}
	login := func() error {
		methods, cleanup, err := authMethods(store.Connection{AuthType: store.AuthAgent}, "", kbd)
		if err != nil {
			t.Fatal(err)
		}
		defer cleanup()
		return loginWith(addr, methods)
	}

	signer, priv := newSigner(t)

	// agente con la clave y su certificado
	withCert := agent.NewKeyring()
	err := withCert.Add(agent.AddedKey{PrivateKey: priv, Certificate: signUserCert(t, ca, signer.PublicKey())})
	if err != nil {
		t.Fatal(err)
	}
	use(withCert)
	if err := login(); err != nil {
		t.Fatalf("no entró con el certificado del agente: %v", err)
	}

	// agente con la clave sola
	plain := agent.NewKeyring()
	if err := plain.Add(agent.AddedKey{PrivateKey: priv}); err != nil {
		t.Fatal(err)
	}
	use(plain)
	if err := login(); err == nil {
		t.Fatal("entró con una clave suelta del agente")
	}
}

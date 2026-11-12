package remote

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"osat/internal/applog"
	"osat/internal/store"
)

// keyServer es un servidor SSH que solo acepta la clave pública authorized.
func keyServer(t *testing.T, authorized ssh.PublicKey) (string, int) {
	t.Helper()
	_, hostPriv, _ := ed25519.GenerateKey(rand.Reader)
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if bytes.Equal(key.Marshal(), authorized.Marshal()) {
			return nil, nil
		}
		return nil, errors.New("clave no autorizada")
	}}
	cfg.AddHostKey(hostSigner)
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
				_, chans, reqs, err := ssh.NewServerConn(nc, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for nch := range chans {
					nch.Reject(ssh.UnknownChannelType, "no hay canales")
				}
			}()
		}
	}()
	a := ln.Addr().(*net.TCPAddr)
	return a.IP.String(), a.Port
}

// Conectar con contraseña, con un salto con contraseña y con una clave con frase, en nivel
// detallado: ninguna contraseña ni frase puede aparecer en el archivo de log.
func TestLogNeverHasSecrets(t *testing.T) {
	m, st := jumpRig(t)

	logDir := t.TempDir()
	prev := slog.Default()
	if _, err := applog.Setup(logDir, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		slog.SetDefault(prev)
		applog.Close()
	})

	const (
		pwWeb      = "Web-Pw-8f31-SECRETA"
		pwWrong    = "Mala-Pw-9d05-SECRETA"
		pwBastion  = "Bastion-Pw-47c2-SECRETA"
		pwDb       = "Db-Pw-12ee-SECRETA"
		passphrase = "Frase-61ab-SECRETA"
		wrongPhras = "FraseMala-3c7d-SECRETA"
	)
	save := func(c store.Connection) store.Connection {
		t.Helper()
		c, err := st.Save(store.ConnectionInput{Connection: c})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	// 1. Contraseña: una equivocada y una buena (que se guarda).
	wh, wp := pwServer(t, pwWeb, nil)
	web := save(store.Connection{Name: "web", Host: wh, Port: wp, User: "u", AuthType: store.AuthPassword})
	if _, err := dialTrusting(m, ConnectRequest{ConnectionID: web.ID, Secret: pwWrong}); err == nil {
		t.Fatal("conectó con una contraseña equivocada")
	}
	c, err := dialTrusting(m, ConnectRequest{ConnectionID: web.ID, Secret: pwWeb, SaveSecret: true})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()

	// 2. Salto con contraseña, hacia un destino con otra contraseña.
	bh, bp := pwServer(t, pwBastion, nil)
	dh, dp := pwServer(t, pwDb, nil)
	bastion := save(store.Connection{Name: "bastion", Host: bh, Port: bp, User: "u", AuthType: store.AuthPassword})
	db := save(store.Connection{Name: "db", Host: dh, Port: dp, User: "u", AuthType: store.AuthPassword, JumpID: bastion.ID})
	c, err = dialTrusting(m, ConnectRequest{
		ConnectionID: db.ID, Secret: pwDb,
		JumpSecrets: map[string]JumpSecret{bastion.ID: {Secret: pwBastion, Save: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()

	// 3. Clave ed25519 con frase: sin frase, con frase equivocada y con la buena.
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	authorized, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(block)
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	kh, kp := keyServer(t, authorized)
	withKey := save(store.Connection{Name: "con-clave", Host: kh, Port: kp, User: "u", AuthType: store.AuthKey, KeyPath: keyPath})
	var ke *Error
	if _, err := dialTrusting(m, ConnectRequest{ConnectionID: withKey.ID}); !errors.As(err, &ke) || ke.Code != CodeNeedSecret {
		t.Fatalf("sin frase: err = %v", err)
	}
	if _, err := dialTrusting(m, ConnectRequest{ConnectionID: withKey.ID, Secret: wrongPhras}); err == nil {
		t.Fatal("conectó con una frase equivocada")
	}
	c, err = dialTrusting(m, ConnectRequest{ConnectionID: withKey.ID, Secret: passphrase, SaveSecret: true})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()

	data, err := os.ReadFile(filepath.Join(logDir, "logs", "osat.log"))
	if err != nil {
		t.Fatal(err)
	}
	logText := string(data)
	t.Logf("log generado:\n%s", logText)

	for _, secret := range []string{pwWeb, pwWrong, pwBastion, pwDb, passphrase, wrongPhras, string(block.Bytes), strings.Split(string(keyPEM), "\n")[1]} {
		if strings.Contains(logText, secret) {
			t.Errorf("el log contiene un secreto: %q", secret)
		}
	}
	// Y sí tiene lo que sirve para diagnosticar.
	for _, want := range []string{"conexión abierta", "conn=web", "conn=bastion", "conn=db", "via=bastion", "conn=con-clave",
		"huella de host presentada", "huella de host aceptada", "no se pudo conectar", "code=AUTH_FAILED", "level=DEBUG msg=conectando", "auth=key"} {
		if !strings.Contains(logText, want) {
			t.Errorf("falta %q en el log", want)
		}
	}
}

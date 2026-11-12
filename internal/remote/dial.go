package remote

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kayrus/putty"
	sshagent "github.com/xanzy/ssh-agent"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"osat/internal/i18n"
	"osat/internal/store"
)

const dialTimeout = 15 * time.Second

// Variable (no const) para poder acortarlo en los tests.
var handshakeTimeout = 30 * time.Second

// newAgent conecta al agente SSH de esta PC y agentAvailable dice si hay uno;
// son variables para poder reemplazarlas en los tests por un agente en memoria.
var (
	newAgent       = sshagent.New
	agentAvailable = sshagent.Available
)

// ExpandHome reemplaza un "~" inicial por la carpeta personal.
func ExpandHome(p string) string {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// kbd arma el método keyboard-interactive: también se ofrece con clave o agente
// porque hay servidores que piden un segundo factor después de la clave.
func authMethods(c store.Connection, secret string, kbd func(password string) ssh.AuthMethod) ([]ssh.AuthMethod, func(), error) {
	cleanup := func() {}
	switch c.AuthType {
	case store.AuthKey:
		pem, err := os.ReadFile(ExpandHome(c.KeyPath))
		if err != nil {
			return nil, cleanup, i18n.Err("remote.couldnTReadThePrivate", err)
		}
		if isPPK(pem) {
			signer, err := parsePPK(pem, secret)
			if err != nil {
				return nil, cleanup, err
			}
			return keyMethods(c.KeyPath, signer, kbd, cleanup)
		}
		signer, err := ssh.ParsePrivateKey(pem)
		var missing *ssh.PassphraseMissingError
		if errors.As(err, &missing) {
			if secret == "" {
				return nil, cleanup, newErr(CodeNeedSecret, "La clave privada está protegida. Ingresá la passphrase.",
					map[string]string{"kind": "passphrase"})
			}
			signer, err = ssh.ParsePrivateKeyWithPassphrase(pem, []byte(secret))
			if err != nil {
				return nil, cleanup, newErr(CodeAuthFailed, "Passphrase incorrecta.", map[string]string{"kind": "passphrase"})
			}
		} else if err != nil {
			return nil, cleanup, i18n.Err("remote.invalidPrivateKeyV", err)
		}
		return keyMethods(c.KeyPath, signer, kbd, cleanup)

	case store.AuthAgent:
		ag, conn, err := newAgent()
		if err != nil {
			return nil, cleanup, i18n.Err("remote.noSshAgentFoundPageant", err)
		}
		if conn != nil {
			cleanup = func() { conn.Close() }
		}
		return []ssh.AuthMethod{ssh.PublicKeysCallback(agent.Agent(ag).Signers), kbd("")}, cleanup, nil

	default: // password
		if secret == "" {
			return nil, cleanup, newErr(CodeNeedSecret, "Ingresá la contraseña.", map[string]string{"kind": "password"})
		}
		return []ssh.AuthMethod{ssh.Password(secret), kbd(secret)}, cleanup, nil
	}
}

// keyMethods arma los métodos de autenticación de una clave privada. Si junto
// a la clave hay un certificado de usuario, se ofrece primero y después la
// clave sola (un servidor que no confía en la CA sigue aceptándola).
func keyMethods(keyPath string, signer ssh.Signer, kbd func(password string) ssh.AuthMethod, cleanup func()) ([]ssh.AuthMethod, func(), error) {
	signers := []ssh.Signer{signer}
	certSigner, err := userCertSigner(keyPath, signer)
	if err != nil {
		return nil, cleanup, err
	}
	if certSigner != nil {
		signers = []ssh.Signer{certSigner, signer}
	}
	return []ssh.AuthMethod{ssh.PublicKeys(signers...), kbd("")}, cleanup, nil
}

// userCertSigner busca el certificado de usuario de la clave, como ssh:
// "<clave>-cert.pub" y, para una .ppk, también "<clave sin .ppk>-cert.pub".
// Devuelve nil si no hay certificado; si el archivo existe pero no sirve,
// devuelve un error que lo nombra.
func userCertSigner(keyPath string, signer ssh.Signer) (ssh.Signer, error) {
	keyPath = ExpandHome(keyPath)
	candidates := []string{keyPath + "-cert.pub"}
	if strings.EqualFold(filepath.Ext(keyPath), ".ppk") {
		candidates = append(candidates, strings.TrimSuffix(keyPath, filepath.Ext(keyPath))+"-cert.pub")
	}
	for _, path := range candidates {
		b, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, i18n.Err("remote.couldnTReadTheCertificate", path, err)
		}
		pub, _, _, _, err := ssh.ParseAuthorizedKey(b)
		if err != nil {
			return nil, i18n.Err("remote.invalidCertificateSV", path, err)
		}
		cert, ok := pub.(*ssh.Certificate)
		if !ok {
			return nil, i18n.Err("remote.invalidCertificateSItIs", path)
		}
		certSigner, err := ssh.NewCertSigner(cert, signer)
		if err != nil {
			return nil, i18n.Err("remote.theCertificateSDoesnT", path, keyPath, err)
		}
		return certSigner, nil
	}
	return nil, nil
}

// isPPK: clave en formato de PuTTY (.ppk v2 o v3), que x/crypto/ssh no lee.
func isPPK(b []byte) bool {
	b = bytes.TrimSpace(bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})) // BOM de UTF-8
	return bytes.HasPrefix(b, []byte("PuTTY-User-Key-File-"))
}

// parsePPK arma el firmante de una clave .ppk, con los mismos errores que
// las claves OpenSSH (pedir passphrase, passphrase incorrecta).
func parsePPK(b []byte, secret string) (ssh.Signer, error) {
	k, err := putty.New(b)
	if err != nil {
		return nil, i18n.Err("remote.invalidPpkKeyV", err)
	}
	var pass []byte
	if k.Encryption != "none" {
		if secret == "" {
			return nil, newErr(CodeNeedSecret, "La clave privada está protegida. Ingresá la passphrase.",
				map[string]string{"kind": "passphrase"})
		}
		pass = []byte(secret)
	}
	raw, err := k.ParseRawPrivateKey(pass)
	if err != nil {
		if pass != nil {
			return nil, newErr(CodeAuthFailed, "Passphrase incorrecta.", map[string]string{"kind": "passphrase"})
		}
		return nil, i18n.Err("remote.invalidPpkKeyV", err)
	}
	if p, ok := raw.(*ed25519.PrivateKey); ok {
		raw = *p // NewSignerFromKey espera el valor, no el puntero
	}
	signer, err := ssh.NewSignerFromKey(raw)
	if err != nil {
		return nil, i18n.Err("remote.unsupportedPpkKeyV", err)
	}
	return signer, nil
}

// Dial abre un cliente SSH autenticado contra la conexión indicada.
// prompt (opcional) se usa para preguntas keyboard-interactive. via
// (opcional) es el salto: la conexión TCP se abre desde ese servidor.
func Dial(c store.Connection, secret string, hk *HostKeys, prompt Prompter, via *ssh.Client) (*ssh.Client, error) {
	addr := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	clock := &handshakeClock{}
	kbd := func(password string) ssh.AuthMethod {
		return keyboardInteractive(addr, password, prompt, clock.pause)
	}
	methods, cleanup, err := authMethods(c, secret, kbd)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	cb, err := hk.Callback()
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User:              c.User,
		Auth:              methods,
		HostKeyCallback:   cb,
		HostKeyAlgorithms: hk.Algorithms(addr),
		Timeout:           dialTimeout,
	}
	client, err := dialWithDeadline(addr, cfg, clock, via)
	if err != nil {
		var ke *Error
		if errors.As(err, &ke) {
			return nil, ke
		}
		if isAuthError(err) {
			kind := "password"
			if c.AuthType == store.AuthKey {
				kind = "key"
			} else if c.AuthType == store.AuthAgent {
				kind = "agent"
			}
			return nil, newErr(CodeAuthFailed, "Autenticación rechazada por el servidor.", map[string]string{"kind": kind})
		}
		return nil, i18n.Err("remote.couldnTConnectToS", addr, err)
	}
	go keepAlive(client)
	return client, nil
}

// dialWithDeadline es como ssh.Dial, pero cfg.Timeout de ssh.Dial solo cubre
// el connect TCP: un servidor que acepta y nunca responde colgaría el
// handshake para siempre. Acá el deadline cubre handshake + autenticación.
// Con via, la conexión TCP sale de ese servidor (ProxyJump).
func dialWithDeadline(addr string, cfg *ssh.ClientConfig, clock *handshakeClock, via *ssh.Client) (*ssh.Client, error) {
	var conn net.Conn
	var err error
	if via != nil {
		ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
		conn, err = via.DialContext(ctx, "tcp", addr)
		cancel()
	} else {
		conn, err = net.DialTimeout("tcp", addr, dialTimeout)
	}
	if err != nil {
		return nil, err
	}
	if clock == nil {
		clock = &handshakeClock{}
	}
	clock.conn = conn
	clock.start()
	sc, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		clock.stop()
		conn.Close()
		return nil, err
	}
	clock.stop()
	return ssh.NewClient(sc, chans, reqs), nil
}

// handshakeClock maneja el deadline del handshake y permite pausarlo mientras
// el usuario contesta una pregunta keyboard-interactive (p. ej. un OTP).
type handshakeClock struct {
	conn net.Conn
	// Los túneles por un salto no aceptan deadlines: ahí se cierra la
	// conexión cuando vence el tiempo.
	timer *time.Timer
}

func (h *handshakeClock) start() {
	if h.conn == nil {
		return
	}
	if h.conn.SetDeadline(time.Now().Add(handshakeTimeout)) != nil {
		conn := h.conn
		h.timer = time.AfterFunc(handshakeTimeout, func() { conn.Close() })
	}
}

func (h *handshakeClock) stop() {
	if h.conn != nil {
		_ = h.conn.SetDeadline(time.Time{})
	}
	if h.timer != nil {
		h.timer.Stop()
		h.timer = nil
	}
}

func (h *handshakeClock) pause() (resume func()) {
	h.stop()
	return h.start
}

const (
	keepAliveInterval = 30 * time.Second // por defecto; el ajuste Terminal.KeepAlive lo cambia (ver SetOptions)
	keepAliveTimeout  = 15 * time.Second
	keepAliveMaxMiss  = 3 // como ServerAliveCountMax de OpenSSH
)

// keepAlive cierra el cliente si el servidor deja de responder, así las
// terminales reciben term:exit y SFTP devuelve error en vez de colgarse.
func keepAlive(c *ssh.Client) {
	t := time.NewTicker(currentOptions().KeepAlive)
	defer t.Stop()
	missed := 0
	for range t.C {
		res := make(chan error, 1)
		go func() {
			_, _, err := c.SendRequest("keepalive@openssh.com", true, nil)
			res <- err
		}()
		select {
		case err := <-res:
			if err != nil { // conexión cerrada (por nosotros o por el servidor)
				c.Close()
				return
			}
			missed = 0
		case <-time.After(keepAliveTimeout):
			missed++
			if missed >= keepAliveMaxMiss {
				c.Close()
				return
			}
		}
	}
}

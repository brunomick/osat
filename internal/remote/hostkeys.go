package remote

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"osat/internal/i18n"
)

// HostKeys gestiona el archivo known_hosts propio de osat.
type HostKeys struct {
	mu      sync.Mutex
	path    string
	pending map[string]ssh.PublicKey // clave vista pero aún no aceptada, por host:port
}

func NewHostKeys(path string) (*HostKeys, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0o600)
	if err != nil {
		return nil, err
	}
	f.Close()
	return &HostKeys{path: path, pending: map[string]ssh.PublicKey{}}, nil
}

// Callback devuelve un HostKeyCallback que valida contra known_hosts y, si la
// clave es desconocida o distinta, devuelve un *Error estructurado.
func (h *HostKeys) Callback() (ssh.HostKeyCallback, error) {
	h.mu.Lock()
	cb, err := knownhosts.New(h.path)
	h.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := cb(hostname, remote, key)
		if err == nil {
			return nil
		}
		var ke *knownhosts.KeyError
		if !errors.As(err, &ke) {
			return err
		}
		h.mu.Lock()
		h.pending[hostname] = key
		h.mu.Unlock()
		data := map[string]string{
			"host":        hostname,
			"keyType":     key.Type(),
			"fingerprint": ssh.FingerprintSHA256(key),
		}
		if len(ke.Want) == 0 {
			slog.Info("huella de host presentada", "host", hostname, "type", data["keyType"], "fingerprint", data["fingerprint"])
			return newErr(CodeHostKeyUnknown, "Servidor desconocido: verificá la huella antes de continuar.", data)
		}
		data["expected"] = ssh.FingerprintSHA256(ke.Want[0].Key)
		slog.Warn("la huella del host cambió", "host", hostname, "type", data["keyType"], "fingerprint", data["fingerprint"], "expected", data["expected"])
		return newErr(CodeHostKeyMismatch,
			"¡ATENCIÓN! La clave del servidor cambió. Podría tratarse de un ataque man-in-the-middle.", data)
	}, nil
}

// Orden de preferencia de claves de host: ed25519 primero, como OpenSSH.
var preferredHostKeyAlgos = []string{
	ssh.KeyAlgoED25519,
	ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521,
	ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256,
}

var (
	probeOnce sync.Once
	probeKey  ssh.PublicKey
)

// Algorithms devuelve qué tipos de clave de host pedirle al servidor. Si ya
// confiamos en una clave para hostport, solo se piden algoritmos de ese tipo
// (si no, un servidor con varias claves podría mandar otra y saltaría una
// falsa alerta de MITM). nil = usar los valores por defecto de x/crypto.
func (h *HostKeys) Algorithms(hostport string) []string {
	h.mu.Lock()
	cb, err := knownhosts.New(h.path)
	h.mu.Unlock()
	if err != nil {
		return preferredHostKeyAlgos
	}
	// Truco: verificar una clave que no puede estar en known_hosts devuelve un
	// KeyError con las claves conocidas para el host (sirve con hosts hasheados).
	probeOnce.Do(func() {
		pub, _, _ := ed25519.GenerateKey(rand.Reader)
		probeKey, _ = ssh.NewPublicKey(pub)
	})
	var ke *knownhosts.KeyError
	if !errors.As(cb(hostport, &net.TCPAddr{}, probeKey), &ke) || len(ke.Want) == 0 {
		return preferredHostKeyAlgos
	}
	known := map[string]bool{}
	for _, k := range ke.Want {
		t := k.Key.Type()
		known[t] = true
		if t == ssh.KeyAlgoRSA { // una clave RSA se verifica con rsa-sha2-*
			known[ssh.KeyAlgoRSASHA512], known[ssh.KeyAlgoRSASHA256] = true, true
		}
	}
	var out []string
	for _, a := range preferredHostKeyAlgos {
		if known[a] {
			out = append(out, a)
		}
	}
	return out // vacío (tipo poco común, p. ej. certificados) = nil = defaults
}

// Trust agrega a known_hosts la clave pendiente para host (formato host:port).
func (h *HostKeys) Trust(host string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	key, ok := h.pending[host]
	if !ok {
		return i18n.Err("remote.thereIsNoPendingKey", host)
	}
	f, err := os.OpenFile(h.path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	line := knownhosts.Line([]string{knownhosts.Normalize(host)}, key)
	if _, err := f.WriteString(line + "\n"); err != nil {
		return err
	}
	delete(h.pending, host)
	slog.Info("huella de host aceptada", "host", host, "type", key.Type(), "fingerprint", ssh.FingerprintSHA256(key))
	return nil
}

// Forget elimina todas las entradas de known_hosts para host (host:port).
// Se usa cuando el usuario confirma que el cambio de clave es legítimo.
func (h *HostKeys) Forget(host string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	norm := knownhosts.Normalize(host)
	in, err := os.ReadFile(h.path)
	if err != nil {
		return err
	}
	var out strings.Builder
	sc := bufio.NewScanner(strings.NewReader(string(in)))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		fields := strings.Fields(line)
		if len(fields) >= 1 {
			match := false
			for _, hst := range strings.Split(fields[0], ",") {
				if hst == norm {
					match = true
					break
				}
			}
			if match {
				continue
			}
		}
		out.WriteString(line + "\n")
	}
	if err := os.WriteFile(h.path, []byte(out.String()), 0o600); err != nil {
		return err
	}
	slog.Info("huella de host olvidada", "host", host)
	return nil
}

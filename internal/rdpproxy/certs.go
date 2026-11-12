package rdpproxy

import (
	"bufio"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"osat/internal/i18n"
	"osat/internal/remote"
)

// KnownCerts guarda las huellas de certificados RDP que el usuario aceptó
// (rdp_known_certs: una línea "host:puerto huella"), como known_hosts en SSH.
// Los certificados válidos para el sistema no pasan por acá.
type KnownCerts struct {
	mu      sync.Mutex
	path    string
	pending map[string]string // huella vista pero aún no aceptada, por host:puerto
}

func NewKnownCerts(path string) (*KnownCerts, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0o600)
	if err != nil {
		return nil, err
	}
	f.Close()
	return &KnownCerts{path: path, pending: map[string]string{}}, nil
}

// Fingerprint: SHA-256 del certificado en hexadecimal con ":" (como lo muestra
// el visor de certificados).
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

// check acepta la cadena si es válida para el sistema y para serverName, o si
// su huella es la aceptada para hostPort. Si no, devuelve un *remote.Error
// para que el frontend pregunte.
func (k *KnownCerts) check(hostPort, serverName string, chain []*x509.Certificate) error {
	leaf := chain[0]
	inter := x509.NewCertPool()
	for _, c := range chain[1:] {
		inter.AddCert(c)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: serverName, Intermediates: inter}); err == nil {
		return nil
	}
	fp := Fingerprint(leaf.Raw)
	k.mu.Lock()
	defer k.mu.Unlock()
	known := k.lookup(hostPort)
	if known == fp {
		return nil
	}
	k.pending[hostPort] = fp
	data := map[string]string{
		"host":        hostPort,
		"subject":     leaf.Subject.CommonName,
		"issuer":      leaf.Issuer.CommonName,
		"notAfter":    leaf.NotAfter.Format("2006-01-02"),
		"fingerprint": fp,
	}
	if known == "" {
		return remote.NewError(remote.CodeRdpCertUnknown,
			"El certificado del servidor no es de confianza para el sistema: verificá la huella antes de continuar.", data)
	}
	data["expected"] = known
	return remote.NewError(remote.CodeRdpCertMismatch,
		"¡ATENCIÓN! El certificado del servidor cambió. Podría tratarse de un ataque man-in-the-middle.", data)
}

// lookup: huella aceptada para hostPort, o "". Con k.mu tomado.
func (k *KnownCerts) lookup(hostPort string) string {
	f, err := os.Open(k.path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if h, fp, ok := strings.Cut(strings.TrimSpace(sc.Text()), " "); ok && h == hostPort {
			return fp
		}
	}
	return ""
}

// Trust acepta para hostPort (reemplazando la anterior) la huella que el
// usuario vio. Tiene que ser la última que se vio: si otro sondeo vio una
// distinta mientras el diálogo estaba abierto, no se acepta ninguna.
func (k *KnownCerts) Trust(hostPort, fp string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if pending, ok := k.pending[hostPort]; !ok || pending != fp {
		return i18n.Err("app.theCertificateOfSChanged", hostPort)
	}
	b, err := os.ReadFile(k.path)
	if err != nil {
		return err
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if h, _, _ := strings.Cut(strings.TrimSpace(l), " "); l != "" && h != hostPort {
			out = append(out, l)
		}
	}
	out = append(out, hostPort+" "+fp)
	if err := os.WriteFile(k.path, []byte(strings.Join(out, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	delete(k.pending, hostPort)
	return nil
}

// CheckGateway es check para el certificado de un RD Gateway: el error lleva
// data["gateway"] = "1" para que el aviso diga que es el del gateway.
func (k *KnownCerts) CheckGateway(hostPort, serverName string, chain []*x509.Certificate) error {
	err := k.check(hostPort, serverName, chain)
	var re *remote.Error
	if errors.As(err, &re) {
		re.Data["gateway"] = "1"
	}
	return err
}

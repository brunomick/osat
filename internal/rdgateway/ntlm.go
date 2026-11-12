package rdgateway

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"strings"
	"time"
	"unicode/utf16"

	"golang.org/x/crypto/md4"
)

// NTLMv2 (MS-NLMP) armado como los clientes de Windows: además de la
// respuesta, la información del servidor lleva el channel binding del TLS
// (protección extendida), el nombre del servicio y, si el servidor mandó la
// marca de tiempo, el MIC. Un RD Gateway que exige protección extendida
// corta la conexión si falta.

const (
	avEOL             = 0
	avNbComputerName  = 1
	avNbDomainName    = 2
	avFlags           = 6
	avTimestamp       = 7
	avTargetName      = 9
	avChannelBindings = 10
)

const (
	ntlmUnicode       = 0x00000001
	ntlmRequestTarget = 0x00000004
	ntlmNTLM          = 0x00000200
	ntlmAlwaysSign    = 0x00008000
	ntlmESS           = 0x00080000
	ntlmTargetInfo    = 0x00800000
	ntlmVersion       = 0x02000000
	ntlm128           = 0x20000000
	ntlmKeyExch       = 0x40000000
	ntlm56            = 0x80000000

	ntlmFlags = ntlmUnicode | ntlmRequestTarget | ntlmNTLM | ntlmAlwaysSign | ntlmESS |
		ntlmTargetInfo | ntlmVersion | ntlm128 | ntlm56
)

// ntlmOSVersion: Windows 10.0.19041, revisión NTLM 15.
var ntlmOSVersion = []byte{10, 0, 0x61, 0x4a, 0, 0, 0, 0x0f}

func utf16le(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[2*i:], c)
	}
	return b
}

func avPair(id uint16, v []byte) []byte {
	b := binary.LittleEndian.AppendUint16(nil, id)
	b = binary.LittleEndian.AppendUint16(b, uint16(len(v)))
	return append(b, v...)
}

func hmacMD5(key []byte, data ...[]byte) []byte {
	h := hmac.New(md5.New, key)
	for _, d := range data {
		h.Write(d)
	}
	return h.Sum(nil)
}

func ntowfv2(user, domain, password string) []byte {
	h := md4.New()
	h.Write(utf16le(password))
	return hmacMD5(h.Sum(nil), utf16le(strings.ToUpper(user)+domain))
}

// ntlmv2Temp: la estructura NTLMv2_CLIENT_CHALLENGE (MS-NLMP §2.2.2.7).
func ntlmv2Temp(timestamp, clientChallenge, targetInfo []byte) []byte {
	b := []byte{1, 1, 0, 0, 0, 0, 0, 0}
	b = append(b, timestamp...)
	b = append(b, clientChallenge...)
	b = append(b, 0, 0, 0, 0)
	b = append(b, targetInfo...)
	return append(b, 0, 0, 0, 0)
}

// channelBindings: MD5 de la gss_channel_bindings_struct con
// "tls-server-end-point:" y el hash del certificado del servidor (RFC 5929:
// SHA-256, o el hash de la firma si es más fuerte).
func channelBindings(certDER []byte) []byte {
	var sum []byte
	c, err := x509.ParseCertificate(certDER)
	switch {
	case err == nil && (c.SignatureAlgorithm == x509.SHA384WithRSA || c.SignatureAlgorithm == x509.ECDSAWithSHA384 || c.SignatureAlgorithm == x509.SHA384WithRSAPSS):
		h := sha512.Sum384(certDER)
		sum = h[:]
	case err == nil && (c.SignatureAlgorithm == x509.SHA512WithRSA || c.SignatureAlgorithm == x509.ECDSAWithSHA512 || c.SignatureAlgorithm == x509.SHA512WithRSAPSS):
		h := sha512.Sum512(certDER)
		sum = h[:]
	default:
		h := sha256.Sum256(certDER)
		sum = h[:]
	}
	app := append([]byte("tls-server-end-point:"), sum...)
	s := make([]byte, 16, 20+len(app)) // direcciones del iniciador y del aceptor: vacías
	s = binary.LittleEndian.AppendUint32(s, uint32(len(app)))
	h := md5.Sum(append(s, app...))
	return h[:]
}

func ntlmNegotiate() []byte {
	b := make([]byte, 32, 40)
	copy(b, "NTLMSSP\x00")
	binary.LittleEndian.PutUint32(b[8:], 1)
	binary.LittleEndian.PutUint32(b[12:], ntlmFlags)
	binary.LittleEndian.PutUint32(b[20:], 40) // dominio y estación: vacíos
	binary.LittleEndian.PutUint32(b[28:], 40)
	return append(b, ntlmOSVersion...)
}

type ntlmChallengeMsg struct {
	flags           uint32
	serverChallenge []byte
	targetInfo      []byte
}

func parseChallenge(b []byte) (*ntlmChallengeMsg, error) {
	if len(b) < 48 || string(b[:8]) != "NTLMSSP\x00" || binary.LittleEndian.Uint32(b[8:]) != 2 {
		return nil, errors.New("el desafío NTLM no es válido")
	}
	n, off := int(binary.LittleEndian.Uint16(b[40:])), int(binary.LittleEndian.Uint32(b[44:]))
	if off+n > len(b) {
		return nil, errors.New("el desafío NTLM está cortado")
	}
	return &ntlmChallengeMsg{
		flags:           binary.LittleEndian.Uint32(b[20:]),
		serverChallenge: b[24:32],
		targetInfo:      b[off : off+n],
	}, nil
}

// ntlmAuthenticate arma el AUTHENTICATE para el desafío chal. neg es el
// NEGOTIATE que se mandó (entra en el MIC); spn, el nombre del servicio
// ("HTTP/host"); cbt, el resultado de channelBindings.
func ntlmAuthenticate(neg, chal []byte, domain, user, password, workstation, spn string, cbt []byte) ([]byte, error) {
	c, err := parseChallenge(chal)
	if err != nil {
		return nil, err
	}
	if c.flags&ntlmKeyExch != 0 {
		return nil, errors.New("el servidor pidió intercambio de claves NTLM, que no se pidió")
	}

	// La información del servidor, sin el EOL, más lo que agrega el cliente.
	var ti, timestamp []byte
	var flags uint32
	for rest := c.targetInfo; len(rest) >= 4; {
		id, l := binary.LittleEndian.Uint16(rest), int(binary.LittleEndian.Uint16(rest[2:]))
		if id == avEOL || 4+l > len(rest) {
			break
		}
		switch id {
		case avTimestamp:
			timestamp = rest[4 : 4+l]
		case avFlags:
			if l >= 4 {
				flags = binary.LittleEndian.Uint32(rest[4:])
			}
			rest = rest[4+l:]
			continue // se vuelve a agregar abajo
		}
		ti = append(ti, rest[:4+l]...)
		rest = rest[4+l:]
	}
	withMIC := timestamp != nil
	if withMIC {
		flags |= 0x2 // MIC presente
	}
	if flags != 0 {
		ti = append(ti, avPair(avFlags, binary.LittleEndian.AppendUint32(nil, flags))...)
	}
	ti = append(ti, avPair(avChannelBindings, cbt)...)
	ti = append(ti, avPair(avTargetName, utf16le(spn))...)
	ti = append(ti, avPair(avEOL, nil)...)

	if timestamp == nil {
		ft := uint64(time.Now().UnixNano()/100) + 116444736000000000
		timestamp = binary.LittleEndian.AppendUint64(nil, ft)
	}
	cc := make([]byte, 8)
	if _, err := rand.Read(cc); err != nil {
		return nil, err
	}
	key := ntowfv2(user, domain, password)
	temp := ntlmv2Temp(timestamp, cc, ti)
	proof := hmacMD5(key, c.serverChallenge, temp)
	nt := append(proof, temp...)
	lm := make([]byte, 24) // con marca de tiempo del servidor va en cero
	if !withMIC {
		lm = append(hmacMD5(key, c.serverChallenge, cc), cc...)
	}

	// Encabezado de 88 bytes (con versión y MIC) y después los campos.
	payload := [][]byte{lm, nt, utf16le(domain), utf16le(user), utf16le(workstation), nil}
	b := make([]byte, 88)
	copy(b, "NTLMSSP\x00")
	binary.LittleEndian.PutUint32(b[8:], 3)
	off := len(b)
	for i, p := range payload { // Lm, Nt, dominio, usuario, estación, clave de sesión
		at := 12 + 8*i
		binary.LittleEndian.PutUint16(b[at:], uint16(len(p)))
		binary.LittleEndian.PutUint16(b[at+2:], uint16(len(p)))
		binary.LittleEndian.PutUint32(b[at+4:], uint32(off))
		off += len(p)
	}
	binary.LittleEndian.PutUint32(b[60:], c.flags&ntlmFlags)
	copy(b[64:], ntlmOSVersion)
	for _, p := range payload {
		b = append(b, p...)
	}
	if withMIC {
		sessionKey := hmacMD5(key, proof) // sin intercambio de claves, es la exportada
		copy(b[72:88], hmacMD5(sessionKey, neg, chal, b))
	}
	return b, nil
}

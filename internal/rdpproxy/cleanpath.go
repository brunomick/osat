package rdpproxy

import (
	"encoding/asn1"
	"errors"
	"fmt"
)

// RDCleanPath (IronRDP, crates/ironrdp-rdcleanpath): el cliente web manda su
// X.224 Connection Request envuelto en esta PDU (DER, etiquetas EXPLICIT), el
// proxy abre TCP+TLS hacia el servidor y responde con el Connection Confirm y
// la cadena de certificados. Desde ahí el WebSocket lleva el TLS ya descifrado.

const (
	cleanPathVersion = 3390 // VERSION_1 = 3389 + 1

	errGeneral     = 1
	errNegotiation = 2 // x224 lleva la respuesta del servidor (p. ej. "hace falta CredSSP")

	maxRequestLen = 16 << 10
)

// request es lo que manda el cliente. Error, cadena y dirección solo los manda
// el proxy: se declaran para rechazar un pedido que los traiga (encoding/asn1
// ignora en silencio los elementos que sobran al final de una SEQUENCE).
type request struct {
	Version           int           `asn1:"explicit,tag:0"`
	Error             asn1.RawValue `asn1:"optional,explicit,tag:1"`
	Destination       string        `asn1:"optional,explicit,tag:2,utf8"`
	ProxyAuth         string        `asn1:"optional,explicit,tag:3,utf8"`
	ServerAuth        string        `asn1:"optional,explicit,tag:4,utf8"`
	PreconnectionBlob string        `asn1:"optional,explicit,tag:5,utf8"`
	X224ConnectionPDU []byte        `asn1:"optional,explicit,tag:6"`
	ServerCertChain   asn1.RawValue `asn1:"optional,explicit,tag:7"`
	ServerAddr        asn1.RawValue `asn1:"optional,explicit,tag:9"`
}

func parseRequest(b []byte) (*request, error) {
	var r request
	rest, err := asn1.Unmarshal(b, &r)
	if err != nil {
		return nil, fmt.Errorf("RDCleanPath inválido: %w", err)
	}
	if len(rest) != 0 {
		return nil, errors.New("RDCleanPath inválido: sobran bytes")
	}
	if r.Version != cleanPathVersion {
		return nil, fmt.Errorf("versión de RDCleanPath no soportada: %d", r.Version)
	}
	if len(r.X224ConnectionPDU) == 0 {
		return nil, errors.New("RDCleanPath sin X.224")
	}
	if len(r.Error.FullBytes)+len(r.ServerCertChain.FullBytes)+len(r.ServerAddr.FullBytes) != 0 {
		return nil, errors.New("RDCleanPath inválido: no es un pedido")
	}
	return &r, nil
}

// pduLen dice cuánto mide la PDU completa a partir de su encabezado DER, o 0
// si todavía no llegaron bytes suficientes para saberlo.
func pduLen(b []byte) (int, error) {
	if len(b) < 2 {
		return 0, nil
	}
	if b[0] != 0x30 {
		return 0, errors.New("RDCleanPath inválido: no es una SEQUENCE")
	}
	if b[1] < 0x80 {
		return 2 + int(b[1]), nil
	}
	n := int(b[1] & 0x7f)
	if n == 0 || n > 3 {
		return 0, errors.New("RDCleanPath inválido: largo")
	}
	if len(b) < 2+n {
		return 0, nil
	}
	l := 0
	for _, c := range b[2 : 2+n] {
		l = l<<8 | int(c)
	}
	return 2 + n + l, nil
}

// cleanPathErr: los campos opcionales en 0 no se mandan.
type cleanPathErr struct {
	code, http, wsa int
	tls             int // -1: sin alerta TLS
}

func responsePDU(x224 []byte, certs [][]byte, serverAddr string) []byte {
	var chain []byte
	for _, c := range certs {
		chain = append(chain, tlv(0x04, c)...)
	}
	return tlv(0x30, cat(
		explicit(0, integer(cleanPathVersion)),
		explicit(6, tlv(0x04, x224)),
		explicit(7, tlv(0x30, chain)),
		explicit(9, tlv(0x0c, []byte(serverAddr))),
	))
}

func errorPDU(e cleanPathErr, x224 []byte) []byte {
	body := explicit(0, integer(e.code))
	if e.http != 0 {
		body = cat(body, explicit(1, integer(e.http)))
	}
	if e.wsa != 0 {
		body = cat(body, explicit(2, integer(e.wsa)))
	}
	if e.tls >= 0 {
		body = cat(body, explicit(3, integer(e.tls)))
	}
	parts := [][]byte{explicit(0, integer(cleanPathVersion)), explicit(1, tlv(0x30, body))}
	if x224 != nil {
		parts = append(parts, explicit(6, tlv(0x04, x224)))
	}
	return tlv(0x30, cat(parts...))
}

func explicit(tag int, inner []byte) []byte { return tlv(byte(0xa0|tag), inner) }

func integer(v int) []byte {
	b, _ := asn1.Marshal(v) // un int nunca falla
	return b
}

func tlv(tag byte, content []byte) []byte {
	n := len(content)
	var hdr []byte
	switch {
	case n < 0x80:
		hdr = []byte{tag, byte(n)}
	case n < 0x100:
		hdr = []byte{tag, 0x81, byte(n)}
	case n < 0x10000:
		hdr = []byte{tag, 0x82, byte(n >> 8), byte(n)}
	default:
		hdr = []byte{tag, 0x83, byte(n >> 16), byte(n >> 8), byte(n)}
	}
	return append(hdr, content...)
}

func cat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

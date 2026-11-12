package rdpproxy

import (
	"bytes"
	"encoding/asn1"
	"testing"
)

// fullPDU es la PDU completa de IronRDP, para leer lo que arma el proxy.
type fullPDU struct {
	Version    int           `asn1:"explicit,tag:0"`
	Error      asn1.RawValue `asn1:"optional,explicit,tag:1"`
	X224       []byte        `asn1:"optional,explicit,tag:6"`
	CertChain  [][]byte      `asn1:"optional,explicit,tag:7"`
	ServerAddr string        `asn1:"optional,explicit,tag:9,utf8"`
}

type fullErr struct {
	Code int `asn1:"explicit,tag:0"`
	HTTP int `asn1:"optional,explicit,tag:1"`
	WSA  int `asn1:"optional,explicit,tag:2"`
	TLS  int `asn1:"optional,explicit,tag:3,default:-1"`
}

func TestVersionOnlyEncoding(t *testing.T) {
	// SEQUENCE { [0] INTEGER 3390 }: el vector que usa IronRDP en sus tests.
	want := []byte{0x30, 0x06, 0xa0, 0x04, 0x02, 0x02, 0x0d, 0x3e}
	if got := tlv(0x30, explicit(0, integer(cleanPathVersion))); !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestParseRequest(t *testing.T) {
	x224 := []byte{0x03, 0x00, 0x00, 0x13, 0x0e, 0xe0}
	b, err := asn1.Marshal(request{Version: cleanPathVersion, Destination: "srv:3389", ProxyAuth: "tok", X224ConnectionPDU: x224})
	if err != nil {
		t.Fatal(err)
	}
	n, err := pduLen(b)
	if err != nil || n != len(b) {
		t.Fatalf("pduLen = %d, %v; want %d", n, err, len(b))
	}
	r, err := parseRequest(b)
	if err != nil {
		t.Fatal(err)
	}
	if r.Destination != "srv:3389" || r.ProxyAuth != "tok" || !bytes.Equal(r.X224ConnectionPDU, x224) {
		t.Fatalf("got %+v", r)
	}
}

func TestParseRequestRejects(t *testing.T) {
	good, _ := asn1.Marshal(request{Version: cleanPathVersion, ProxyAuth: "t", X224ConnectionPDU: []byte{1}})
	wrongVersion, _ := asn1.Marshal(request{Version: 3389, ProxyAuth: "t", X224ConnectionPDU: []byte{1}})
	noX224, _ := asn1.Marshal(request{Version: cleanPathVersion, ProxyAuth: "t"})
	for name, b := range map[string][]byte{
		"versión":     wrongVersion,
		"sin x224":    noX224,
		"sobrante":    append(append([]byte{}, good...), 0),
		"no SEQUENCE": {0x04, 0x01, 0x00},
		// Una respuesta (trae [7]) no es un pedido válido.
		"respuesta": responsePDU([]byte{1}, [][]byte{{2}}, "1.2.3.4:3389"),
	} {
		if _, err := parseRequest(b); err == nil {
			t.Errorf("%s: se aceptó", name)
		}
	}
}

func TestPduLenPartial(t *testing.T) {
	big := tlv(0x30, make([]byte, 300)) // largo en forma larga (0x82)
	for i := 0; i < 4; i++ {
		if n, err := pduLen(big[:i]); n != 0 || err != nil {
			t.Fatalf("con %d bytes: %d, %v", i, n, err)
		}
	}
	if n, _ := pduLen(big); n != len(big) {
		t.Fatalf("pduLen = %d, want %d", n, len(big))
	}
}

func TestResponsePDU(t *testing.T) {
	x224 := []byte{0x03, 0x00, 0x00, 0x13}
	certs := [][]byte{bytes.Repeat([]byte{0xaa}, 900), {0xbb}}
	var p fullPDU
	rest, err := asn1.Unmarshal(responsePDU(x224, certs, "10.0.0.5:3389"), &p)
	if err != nil || len(rest) != 0 {
		t.Fatalf("decode: %v (sobran %d)", err, len(rest))
	}
	if p.Version != cleanPathVersion || !bytes.Equal(p.X224, x224) || p.ServerAddr != "10.0.0.5:3389" ||
		len(p.CertChain) != 2 || !bytes.Equal(p.CertChain[0], certs[0]) || len(p.Error.FullBytes) != 0 {
		t.Fatalf("got %+v", p)
	}
}

func TestErrorPDU(t *testing.T) {
	cases := []struct {
		in   cleanPathErr
		x224 []byte
	}{
		{cleanPathErr{code: errGeneral, http: 403, tls: -1}, nil},
		{cleanPathErr{code: errGeneral, wsa: 10061, tls: -1}, nil},
		{cleanPathErr{code: errGeneral, tls: 0}, nil}, // alerta 0 (close notify) también viaja
		{cleanPathErr{code: errNegotiation, tls: -1}, []byte{0x03, 0x00}},
	}
	for _, c := range cases {
		var p fullPDU
		if _, err := asn1.Unmarshal(errorPDU(c.in, c.x224), &p); err != nil {
			t.Fatalf("%+v: %v", c.in, err)
		}
		var e fullErr
		if _, err := asn1.Unmarshal(p.Error.Bytes, &e); err != nil {
			t.Fatalf("%+v: error: %v", c.in, err)
		}
		if e.Code != c.in.code || e.HTTP != c.in.http || e.WSA != c.in.wsa || e.TLS != c.in.tls || !bytes.Equal(p.X224, c.x224) {
			t.Errorf("%+v: got %+v x224=% x", c.in, e, p.X224)
		}
	}
}

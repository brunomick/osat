package rdgateway

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
)

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEncodeChannelCreate(t *testing.T) {
	want := unhex(t, "08 00 00 00 16 00 00 00 01 00 3D 0D 03 00 06 00 61 00 62 00 00 00")
	if got := encodeChannelCreate("ab", 3389); !bytes.Equal(got, want) {
		t.Fatalf("got % X", got)
	}
}

func TestEncodeTunnelAuth(t *testing.T) {
	want := unhex(t, "06 00 00 00 12 00 00 00 00 00 06 00 50 00 43 00 00 00")
	if got := encodeTunnelAuth("PC"); !bytes.Equal(got, want) {
		t.Fatalf("got % X", got)
	}
}

func TestEncodeHandshake(t *testing.T) {
	want := unhex(t, "01 00 00 00 0E 00 00 00 01 00 00 00 00 00")
	if got := encodeHandshakeRequest(); !bytes.Equal(got, want) {
		t.Fatalf("got % X", got)
	}
}

func TestReadPacketTooLong(t *testing.T) {
	if _, err := readPacket(bytes.NewReader(unhex(t, "0A 00 00 00 00 00 02 00"))); err == nil {
		t.Fatal("aceptó un paquete de 128 KiB")
	}
}

func TestReadPacketRoundTrip(t *testing.T) {
	p, err := readPacket(bytes.NewReader(encodeData([]byte("hola"))))
	if err != nil || p.typ != pktData {
		t.Fatalf("%+v, %v", p, err)
	}
	d, err := dataPayload(p)
	if err != nil || string(d) != "hola" {
		t.Fatalf("%q, %v", d, err)
	}
}

func TestResponseCodeTunnel(t *testing.T) {
	// serverVersion=1, statusCode=0x800759DB, fieldsPresent=0, reserved=0
	raw := unhex(t, "05 00 00 00 12 00 00 00 01 00 DB 59 07 80 00 00 00 00")
	p, err := readPacket(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	code, err := responseCode(p)
	if err != nil || code != 0x800759DB {
		t.Fatalf("code = %#x, %v", code, err)
	}
}

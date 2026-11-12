package rdgateway

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// Vectores de MS-NLMP §4.2.4 (NTLMv2): usuario "User", dominio "Domain",
// contraseña "Password".
func TestNTOWFv2(t *testing.T) {
	want := unhex(t, "0c868a403bfd7a93a3001ef22ef02e3f")
	if got := ntowfv2("User", "Domain", "Password"); !bytes.Equal(got, want) {
		t.Fatalf("got % x", got)
	}
}

func TestNTProofStr(t *testing.T) {
	// TargetInfo: MsvAvNbDomainName "Domain", MsvAvNbComputerName "Server", EOL.
	ti := append(avPair(avNbDomainName, utf16le("Domain")), avPair(avNbComputerName, utf16le("Server"))...)
	ti = append(ti, avPair(avEOL, nil)...)
	temp := ntlmv2Temp(make([]byte, 8), bytes.Repeat([]byte{0xaa}, 8), ti)
	proof := hmacMD5(ntowfv2("User", "Domain", "Password"), unhex(t, "0123456789abcdef"), temp)
	if want := unhex(t, "68cd0ab851e51c96aabc927bebef6a1c"); !bytes.Equal(proof, want) {
		t.Fatalf("got % x", proof)
	}
}

func TestParseChallenge(t *testing.T) {
	c, err := parseChallenge(fakeChallenge())
	if err != nil {
		t.Fatal(err)
	}
	if string(c.serverChallenge) != "12345678" || c.flags&ntlmUnicode == 0 {
		t.Fatalf("%+v", c)
	}
	if _, err := parseChallenge([]byte("NTLMSSP\x00\x01\x00\x00\x00")); err == nil {
		t.Fatal("aceptó un NEGOTIATE como desafío")
	}
}

func TestAuthenticateAddsBindingAndTarget(t *testing.T) {
	cbt := bytes.Repeat([]byte{7}, 16)
	neg := ntlmNegotiate()
	auth, err := ntlmAuthenticate(neg, fakeChallenge(), "EMPRESA", "Nombre.Apellido", "pw", "PC", "HTTP/rds.corp", cbt)
	if err != nil {
		t.Fatal(err)
	}
	ti := ntlmAuthTargetInfo(t, auth)
	if !bytes.Equal(ti[avChannelBindings], cbt) {
		t.Errorf("channel binding = % x", ti[avChannelBindings])
	}
	if string(ti[avTargetName]) != string(utf16le("HTTP/rds.corp")) {
		t.Errorf("target name = %q", ti[avTargetName])
	}
	if ntlmUser(auth) != "Nombre.Apellido" {
		t.Errorf("usuario = %q", ntlmUser(auth))
	}
}

// ntlmAuthTargetInfo: los AV pairs dentro de la respuesta NTLMv2 de un AUTHENTICATE.
func ntlmAuthTargetInfo(t *testing.T, auth []byte) map[uint16][]byte {
	t.Helper()
	n, off := int(binary.LittleEndian.Uint16(auth[20:])), int(binary.LittleEndian.Uint32(auth[24:]))
	nt := auth[off : off+n]
	ti := nt[16+28:] // NTProofStr + encabezado de temp
	out := map[uint16][]byte{}
	for len(ti) >= 4 {
		id, l := binary.LittleEndian.Uint16(ti), int(binary.LittleEndian.Uint16(ti[2:]))
		if id == avEOL {
			break
		}
		out[id] = ti[4 : 4+l]
		ti = ti[4+l:]
	}
	return out
}

// El gateway falso de verdad verifica: con otro certificado, otra contraseña
// o el mensaje tocado, rechaza.
func TestFakeVerifiesNTLM(t *testing.T) {
	cert := []byte("certificado")
	neg := ntlmNegotiate()
	auth, err := ntlmAuthenticate(neg, fakeChallenge(), "EMPRESA", "Nombre.Apellido", "pw", "PC", "HTTP/x", channelBindings(cert))
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyNTLM(neg, fakeChallenge(), auth, "pw", cert); err != nil {
		t.Fatalf("rechazó uno bueno: %v", err)
	}
	if verifyNTLM(neg, fakeChallenge(), auth, "pw", []byte("otro")) == nil {
		t.Error("aceptó otro channel binding")
	}
	if verifyNTLM(neg, fakeChallenge(), auth, "otra", cert) == nil {
		t.Error("aceptó otra contraseña")
	}
	tampered := append([]byte(nil), auth...)
	tampered[60] ^= 1 // flags: el MIC ya no coincide
	if verifyNTLM(neg, fakeChallenge(), tampered, "pw", cert) == nil {
		t.Error("aceptó un mensaje tocado")
	}
}

// Un MsvAvFlags de menos de 4 bytes (desafío malformado) no puede tirar la app.
func TestAuthenticateShortAvFlags(t *testing.T) {
	ti := avPair(avFlags, []byte{1, 2}) // al final y sin EOL: no hay 4 bytes para leer
	chal := make([]byte, 48, 48+len(ti))
	copy(chal, "NTLMSSP\x00")
	binary.LittleEndian.PutUint32(chal[8:], 2)
	binary.LittleEndian.PutUint32(chal[20:], 0xa2898205)
	binary.LittleEndian.PutUint16(chal[40:], uint16(len(ti)))
	binary.LittleEndian.PutUint32(chal[44:], 48)
	chal = append(chal, ti...)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic: %v", r)
		}
	}()
	ntlmAuthenticate(ntlmNegotiate(), chal, "D", "u", "p", "PC", "HTTP/x", make([]byte, 16))
}

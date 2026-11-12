package importer

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/md5"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"osat/internal/store"
)

// Un confCons.xml real de mRemoteNG 1.76 (publicado en el writeup de la máquina
// "Bastion" de Hack The Box), cifrado con la contraseña de fábrica.
const (
	bastionProtected = "ZSvKI7j224Gf/twXpaP5G2QFZMLr1iO1f5JKdtIKL6eUg+eWkL5tKO886au0ofFPW0oop8R8ddXKAx4KK7sAk6AA"
	bastionPassword  = "aEWNFV5uGcjUHF0uS17QTdT9kVqtKCPeoC0Nw5dmaPFjNQ2kt/zO5xDqE4HdVmHAowVRdC7emf7lWWA10dQKiw=="
)

func TestMRemoteNGRealFile(t *testing.T) {
	c := mrCrypto{gcm: true, iter: 1000, password: mremotengDefaultPassword}
	if p, err := c.decrypt(bastionProtected); err != nil || p != "ThisIsNotProtected" {
		t.Fatalf("Protected = %q, %v", p, err)
	}
	if p, err := c.decrypt(bastionPassword); err != nil || p != "thXLHM96BeKL0ER2" {
		t.Fatalf("Password = %q, %v", p, err)
	}
}

// mrEncrypt cifra como mRemoteNG (para armar archivos de prueba).
func mrEncrypt(t *testing.T, gcm bool, password, plain string) string {
	t.Helper()
	if gcm {
		salt, nonce := make([]byte, 16), make([]byte, 16)
		rand.Read(salt)
		rand.Read(nonce)
		key, _ := pbkdf2.Key(sha1.New, password, salt, 1000, 32)
		block, _ := aes.NewCipher(key)
		aead, _ := cipher.NewGCMWithNonceSize(block, 16)
		out := append(append(append([]byte{}, salt...), nonce...), aead.Seal(nil, nonce, []byte(plain), salt)...)
		return base64.StdEncoding.EncodeToString(out)
	}
	key := md5.Sum([]byte(password))
	block, _ := aes.NewCipher(key[:])
	pad := 16 - len(plain)%16
	b := append([]byte(plain), []byte(strings.Repeat(string(rune(pad)), pad))...)
	iv := make([]byte, 16)
	rand.Read(iv)
	ct := make([]byte, len(b))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, b)
	return base64.StdEncoding.EncodeToString(append(iv, ct...))
}

func TestMRemoteNGLegacyCBC(t *testing.T) {
	c := mrCrypto{password: mremotengDefaultPassword}
	if p, err := c.decrypt(mrEncrypt(t, false, mremotengDefaultPassword, "vieja-pw")); err != nil || p != "vieja-pw" {
		t.Fatalf("CBC = %q, %v", p, err)
	}
}

func TestMRemoteNGPasswordsAndInheritance(t *testing.T) {
	old := puttySessions
	t.Cleanup(func() { puttySessions = old })
	puttySessions = func() map[string]map[string]string {
		return map[string]map[string]string{"aws%20keys": {"PublicKeyFile": `C:\keys\aws.ppk`}}
	}
	enc := func(s string) string { return mrEncrypt(t, true, mremotengDefaultPassword, s) }
	xml := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<mrng:Connections xmlns:mrng="http://mremoteng.org" Name="Connections" EncryptionEngine="AES" BlockCipherMode="GCM" KdfIterations="1000" FullFileEncryption="false" Protected="%s" ConfVersion="2.6">
    <Node Name="Windows" Type="Container" Username="admin" Domain="CORP" Password="%s">
        <Node Name="dc01" Type="Connection" Hostname="10.0.1.9" Protocol="RDP" InheritUsername="true" InheritDomain="true" InheritPassword="true" />
        <Node Name="dc02" Type="Connection" Hostname="10.0.1.10" Protocol="RDP" Username="otro" Password="%s" InheritDomain="true" />
    </Node>
    <Node Name="web" Type="Connection" Hostname="10.0.0.5" Protocol="SSH2" Username="ubuntu" PuttySession="aws keys" />
    <Node Name="rota" Type="Connection" Hostname="10.0.0.6" Protocol="SSH2" Username="root" Password="no-es-base64" />
</mrng:Connections>`, enc("ThisIsNotProtected"), enc("pw-carpeta"), enc("pw-propia"))
	r, err := Scan(MRemoteNG, write(t, "confCons.xml", []byte(xml)), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	dc1 := byName(t, r.Items, "dc01")
	if dc1.User != "admin" || dc1.Domain != "CORP" || dc1.Secret != "pw-carpeta" || !dc1.HasSecret {
		t.Errorf("dc01 (hereda todo) = %+v", dc1)
	}
	dc2 := byName(t, r.Items, "dc02")
	if dc2.User != "otro" || dc2.Domain != "CORP" || dc2.Secret != "pw-propia" {
		t.Errorf("dc02 = %+v", dc2)
	}
	web := byName(t, r.Items, "web")
	if web.AuthType != store.AuthKey || web.KeyPath != `C:\keys\aws.ppk` || !web.HasKey || web.HasSecret {
		t.Errorf("web (clave de la sesión de PuTTY) = %+v", web)
	}
	if rota := byName(t, r.Items, "rota"); rota.HasSecret {
		t.Errorf("rota trae una contraseña que no se pudo descifrar: %+v", rota)
	}
	if len(r.Skipped) != 1 || !strings.Contains(r.Skipped[0], "descifrar") {
		t.Errorf("omitidas = %v", r.Skipped)
	}
}

func TestMRemoteNGMasterPassword(t *testing.T) {
	enc := func(s string) string { return mrEncrypt(t, true, "maestra", s) }
	xml := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<mrng:Connections xmlns:mrng="http://mremoteng.org" BlockCipherMode="GCM" KdfIterations="1000" FullFileEncryption="false" Protected="%s">
    <Node Name="db" Type="Connection" Hostname="db" Protocol="SSH2" Username="root" Password="%s" />
</mrng:Connections>`, enc("ThisIsProtected"), enc("pw-db"))
	p := write(t, "confCons.xml", []byte(xml))
	if _, err := Scan(MRemoteNG, p, "", nil); !errors.Is(err, ErrMasterPassword) {
		t.Fatalf("sin maestra: %v", err)
	}
	if _, err := Scan(MRemoteNG, p, "otra", nil); !errors.Is(err, ErrBadMasterPassword) {
		t.Fatalf("maestra incorrecta: %v", err)
	}
	r, err := Scan(MRemoteNG, p, "maestra", nil)
	if err != nil {
		t.Fatal(err)
	}
	if db := byName(t, r.Items, "db"); db.Secret != "pw-db" {
		t.Errorf("db = %+v", db)
	}
}

// winscpEncrypt cifra como WinSCP (EncryptPassword en Security.cpp), con relleno al azar.
func winscpEncrypt(password, user, host string) string {
	enc := func(b byte) string { return fmt.Sprintf("%02X", ^b^0xA3) }
	rnd := func(n int64) byte { v, _ := rand.Int(rand.Reader, big.NewInt(n)); return byte(v.Int64()) }
	pw := user + host + password
	shift := 0
	if len(pw) < 50 {
		shift = int(rnd(int64(50 - len(pw))))
	}
	out := enc(0xFF) + enc(0) + enc(byte(len(pw))) + enc(byte(shift))
	for i := 0; i < shift; i++ {
		out += enc(rnd(256))
	}
	for i := 0; i < len(pw); i++ {
		out += enc(pw[i])
	}
	for len(out) < 100 {
		out += enc(rnd(256))
	}
	return out
}

func TestWinSCPPassword(t *testing.T) {
	for _, pw := range []string{"secreta", "P@ss w0rd!ñ", strings.Repeat("x", 60)} {
		if got := winscpPassword(winscpEncrypt(pw, "root", "10.0.0.5"), "root", "10.0.0.5"); got != pw {
			t.Errorf("%q → %q", pw, got)
		}
	}
	// Con otro usuario/host no coincide la clave: vacía, no basura.
	if got := winscpPassword(winscpEncrypt("secreta", "root", "a"), "root", "b"); got != "" {
		t.Errorf("con otro host = %q", got)
	}
}

func TestWinSCPIniPasswords(t *testing.T) {
	ini := "[Sessions\\web]\nHostName=10.0.0.5\nUserName=root\nPassword=" + winscpEncrypt("pw-web", "root", "10.0.0.5") + "\n"
	r, err := Scan(WinSCP, write(t, "WinSCP.ini", []byte(ini)), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if web := byName(t, r.Items, "web"); web.Secret != "pw-web" || !web.HasSecret {
		t.Errorf("web = %+v", web)
	}
}

// winscpScramble es ScramblePassword de WinSCP (Cryptography.cpp).
func winscpScramble(password string) []byte {
	n := len(password)
	padding := ((n+3)/17)*17 + 17 - 3 - n
	buf := make([]byte, 0, padding+3+n)
	for len(buf) < padding {
		p, _ := rand.Int(rand.Reader, big.NewInt(256))
		if c := byte(p.Int64()); c != 0 && (c < '0' || c > '9') {
			buf = append(buf, c)
		}
	}
	buf = append(buf, byte('0'+n%10), byte('0'+n/10%10), byte('0'+n/100%10))
	buf = append(buf, password...)
	last := 31
	for i, c := range buf {
		last = (last+int(c))%255 + 1
		buf[i] = winscpScrambleTable[last]
	}
	return buf
}

// winscpMasterEncrypt cifra como WinSCP con contraseña maestra
// (ScramblePassword + AES256EncyptWithMAC + SetExternalEncryptedPassword).
func winscpMasterEncrypt(password, master string) string {
	enc := func(b byte) string { return fmt.Sprintf("%02X", ^b^0xA3) }
	salt := make([]byte, 16)
	rand.Read(salt)
	kbuf, _ := pbkdf2.Key(sha1.New, master, salt, 1000, 66)
	block, _ := aes.NewCipher(kbuf[:32])
	ct := winscpScramble(password)
	var nonce, stream [16]byte
	for i := range ct {
		if i%16 == 0 {
			for j := 0; j < 8; j++ {
				if nonce[j]++; nonce[j] != 0 {
					break
				}
			}
			block.Encrypt(stream[:], nonce[:])
		}
		ct[i] ^= stream[i%16]
	}
	h := hmac.New(sha1.New, kbuf[32:64])
	h.Write(ct)
	blob := append(append(salt, ct...), h.Sum(nil)[:10]...)
	return enc(0xFF) + enc(0x01) + fmt.Sprintf("%X", blob)
}

func TestWinSCPScramble(t *testing.T) {
	// Una contraseña de 4 caracteres ocupa 17 bytes, como guarda WinSCP.
	if n := len(winscpScramble("1234")); n != 17 {
		t.Fatalf("1234 ocupa %d bytes, WinSCP guarda 17", n)
	}
	for _, pw := range []string{"", "1234", "13-caracteres", "14-caracteres!", "contraseña con ñ y espacios", strings.Repeat("9", 120)} {
		if got, ok := winscpUnscramble(winscpScramble(pw)); !ok || got != pw {
			t.Errorf("%q → %q (%v)", pw, got, ok)
		}
	}
	if _, ok := winscpUnscramble([]byte("no es scrambled")); ok {
		t.Error("aceptó datos que no son una contraseña de WinSCP")
	}
}

func TestWinSCPMasterPassword(t *testing.T) {
	long := strings.Repeat("larga-", 8) // más de un bloque de AES
	ini := "[Configuration\\Security]\nUseMasterPassword=1\n\n" +
		"[Sessions\\web]\nHostName=10.0.0.5\nUserName=root\nPassword=" + winscpMasterEncrypt("pw-web", "Maestra123") + "\n\n" +
		"[Sessions\\db]\nHostName=db\nUserName=root\nPassword=" + winscpMasterEncrypt(long, "Maestra123") + "\n"
	p := write(t, "WinSCP.ini", []byte(ini))
	if _, err := Scan(WinSCP, p, "", nil); !errors.Is(err, ErrMasterPassword) {
		t.Fatalf("sin maestra: %v", err)
	}
	if _, err := Scan(WinSCP, p, "otra", nil); !errors.Is(err, ErrBadMasterPassword) {
		t.Fatalf("maestra incorrecta: %v", err)
	}
	r, err := Scan(WinSCP, p, "Maestra123", nil)
	if err != nil {
		t.Fatal(err)
	}
	if web := byName(t, r.Items, "web"); web.Secret != "pw-web" {
		t.Errorf("web = %q", web.Secret)
	}
	if db := byName(t, r.Items, "db"); db.Secret != long {
		t.Errorf("db = %q", db.Secret)
	}
	// Con maestra pero sin contraseñas guardadas no hace falta pedirla.
	if _, err := Scan(WinSCP, write(t, "WinSCP.ini", []byte("[Configuration\\Security]\nUseMasterPassword=1\n\n[Sessions\\x]\nHostName=x\n")), "", nil); err != nil {
		t.Errorf("sin contraseñas guardadas: %v", err)
	}
}

package importer

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/md5"
	"crypto/pbkdf2"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"strings"

	"osat/internal/i18n"
)

// Contraseñas guardadas por las otras herramientas, para importarlas junto
// con cada sesión (PuTTY no guarda contraseñas).

// ErrMasterPassword: las contraseñas de mRemoteNG o WinSCP están protegidas
// con una contraseña maestra y hay que pedirla (la de fábrica de mRemoteNG se
// prueba sola).
var ErrMasterPassword = i18n.Err("data.thePasswordsAreProtectedWith")

// ErrBadMasterPassword: la contraseña maestra no es esa.
var ErrBadMasterPassword = i18n.Err("data.theMasterPasswordIsWrong")

// ---------- mRemoteNG ----------

// Contraseña con la que mRemoteNG cifra si no se le configuró una propia.
const mremotengDefaultPassword = "mR3m"

// mrCrypto descifra los campos Password (y Protected) de un confCons.xml.
type mrCrypto struct {
	gcm      bool // mRemoteNG ≥ 1.76; antes, AES-CBC
	iter     int
	password string
}

func newMRCrypto(root mrRoot, password string) mrCrypto {
	c := mrCrypto{gcm: !strings.EqualFold(root.BlockCipherMode, "CBC") && root.BlockCipherMode != "", iter: 1000, password: password}
	if n, err := strconv.Atoi(root.KdfIterations); err == nil && n > 0 {
		c.iter = n
	}
	return c
}

func (c mrCrypto) decrypt(b64 string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return "", err
	}
	if c.gcm {
		// sal (16) + nonce (16) + cifrado y tag; la clave sale de PBKDF2-SHA1 y la sal va como datos asociados.
		if len(data) < 16+16+16 {
			return "", i18n.Err("data.invalidEncryptedPassword")
		}
		salt, nonce, ct := data[:16], data[16:32], data[32:]
		key, err := pbkdf2.Key(sha1.New, c.password, salt, c.iter, 32)
		if err != nil {
			return "", err
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return "", err
		}
		aead, err := cipher.NewGCMWithNonceSize(block, 16)
		if err != nil {
			return "", err
		}
		plain, err := aead.Open(nil, nonce, ct, salt)
		return string(plain), err
	}
	// Formato viejo: AES-CBC con la clave MD5(contraseña) y el IV en los primeros 16 bytes.
	if len(data) < 32 || len(data)%16 != 0 {
		return "", i18n.Err("data.invalidEncryptedPassword")
	}
	key := md5.Sum([]byte(c.password))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	plain := make([]byte, len(data)-16)
	cipher.NewCBCDecrypter(block, data[:16]).CryptBlocks(plain, data[16:])
	pad := int(plain[len(plain)-1])
	if pad == 0 || pad > 16 || pad > len(plain) {
		return "", i18n.Err("data.wrongPassword")
	}
	return string(plain[:len(plain)-pad]), nil
}

// mrUnlock elige con qué contraseña descifrar: la de fábrica si alcanza, si
// no la maestra que se haya indicado. El atributo Protected lo confirma.
func mrUnlock(root mrRoot, master string) (mrCrypto, error) {
	c := newMRCrypto(root, mremotengDefaultPassword)
	if root.Protected == "" {
		return c, nil // archivos viejos sin la marca: se prueba con la de fábrica
	}
	if _, err := c.decrypt(root.Protected); err == nil {
		return c, nil
	}
	if master == "" {
		return c, ErrMasterPassword
	}
	c.password = master
	if _, err := c.decrypt(root.Protected); err != nil {
		return c, ErrBadMasterPassword
	}
	return c, nil
}

// ---------- WinSCP ----------

// winscpReader lee el formato "simple" de WinSCP (Security.cpp): cada byte va
// como dos dígitos hexa, negado y con XOR 0xA3.
type winscpReader struct{ s string }

func (r *winscpReader) next() byte {
	if len(r.s) < 2 {
		r.s = ""
		return 0
	}
	hi, lo := strings.IndexByte("0123456789ABCDEF", r.s[0]), strings.IndexByte("0123456789ABCDEF", r.s[1])
	r.s = r.s[2:]
	if hi < 0 || lo < 0 {
		return 0
	}
	return ^(byte(hi<<4|lo) ^ 0xA3)
}

const (
	winscpFlag     = 0xFF
	winscpExternal = 0x01 // cifrada con la contraseña maestra
)

// winscpPassword deshace el "cifrado" con el que WinSCP guarda las
// contraseñas cuando no se usa contraseña maestra: formato simple, con
// usuario+host adelante y relleno al azar.
func winscpPassword(enc, user, host string) string {
	r := &winscpReader{s: strings.ToUpper(strings.TrimSpace(enc))}
	length := r.next()
	isFlag := length == winscpFlag
	if isFlag {
		r.next() // versión interna
		length = r.next()
	}
	skip := int(r.next()) * 2
	if skip > len(r.s) {
		return ""
	}
	r.s = r.s[skip:]
	out := make([]byte, 0, length)
	for i := 0; i < int(length); i++ {
		out = append(out, r.next())
	}
	pw := string(out)
	if isFlag {
		key := user + host
		if !strings.HasPrefix(pw, key) {
			return ""
		}
		pw = pw[len(key):]
	}
	return pw
}

// winscpMasterPassword descifra una contraseña que WinSCP guardó con
// contraseña maestra (TWinConfiguration::DecryptPassword y Cryptography.cpp):
// marca FF 01 en formato simple y después, en hexa, sal (16) + datos + MAC
// (10), cifrados como los ZIP con AES (fileenc de Gladman): PBKDF2-SHA1 con
// 1000 iteraciones da la clave AES-256 y la del HMAC-SHA1; AES en modo
// contador little-endian que arranca en 1, y el MAC es sobre lo cifrado. Lo
// cifrado no es la contraseña sino su versión "scrambled" (winscpUnscramble).
func winscpMasterPassword(enc, master string) (string, error) {
	r := &winscpReader{s: strings.ToUpper(strings.TrimSpace(enc))}
	if r.next() != winscpFlag || r.next() != winscpExternal {
		return "", i18n.Err("data.unknownWinscpPasswordFormat")
	}
	data, err := hex.DecodeString(r.s)
	if err != nil || len(data) <= 16+10 {
		return "", i18n.Err("data.invalidWinscpPassword")
	}
	salt, ct, mac := data[:16], data[16:len(data)-10], data[len(data)-10:]
	kbuf, err := pbkdf2.Key(sha1.New, master, salt, 1000, 2*32+2)
	if err != nil {
		return "", err
	}
	h := hmac.New(sha1.New, kbuf[32:64])
	h.Write(ct)
	if !hmac.Equal(h.Sum(nil)[:10], mac) {
		return "", ErrBadMasterPassword
	}
	block, err := aes.NewCipher(kbuf[:32])
	if err != nil {
		return "", err
	}
	plain := make([]byte, len(ct))
	var nonce, stream [16]byte
	for i := range ct {
		if i%16 == 0 {
			for j := 0; j < 8; j++ { // contador little-endian en los primeros 8 bytes
				nonce[j]++
				if nonce[j] != 0 {
					break
				}
			}
			block.Encrypt(stream[:], nonce[:])
		}
		plain[i] = ct[i] ^ stream[i%16]
	}
	pw, ok := winscpUnscramble(plain)
	if !ok {
		return "", i18n.Err("data.invalidWinscpPassword")
	}
	return pw, nil
}

// winscpScrambleTable es SScrambleTable de Cryptography.cpp de WinSCP
// (GPL-3.0, como osat).
var winscpScrambleTable = [256]byte{
	0, 223, 235, 233, 240, 185, 88, 102, 22, 130, 27, 53, 79, 125, 66, 201,
	90, 71, 51, 60, 134, 104, 172, 244, 139, 84, 91, 12, 123, 155, 237, 151,
	192, 6, 87, 32, 211, 38, 149, 75, 164, 145, 52, 200, 224, 226, 156, 50,
	136, 190, 232, 63, 129, 209, 181, 120, 28, 99, 168, 94, 198, 40, 238, 112,
	55, 217, 124, 62, 227, 30, 36, 242, 208, 138, 174, 231, 26, 54, 214, 148,
	37, 157, 19, 137, 187, 111, 228, 39, 110, 17, 197, 229, 118, 246, 153, 80,
	21, 128, 69, 117, 234, 35, 58, 67, 92, 7, 132, 189, 5, 103, 10, 15,
	252, 195, 70, 147, 241, 202, 107, 49, 20, 251, 133, 76, 204, 73, 203, 135,
	184, 78, 194, 183, 1, 121, 109, 11, 143, 144, 171, 161, 48, 205, 245, 46,
	31, 72, 169, 131, 239, 160, 25, 207, 218, 146, 43, 140, 127, 255, 81, 98,
	42, 115, 173, 142, 114, 13, 2, 219, 57, 56, 24, 126, 3, 230, 47, 215,
	9, 44, 159, 33, 249, 18, 93, 95, 29, 113, 220, 89, 97, 182, 248, 64,
	68, 34, 4, 82, 74, 196, 213, 165, 179, 250, 108, 254, 59, 14, 236, 175,
	85, 199, 83, 106, 77, 178, 167, 225, 45, 247, 163, 158, 8, 221, 61, 191,
	119, 16, 253, 105, 186, 23, 170, 100, 216, 65, 162, 122, 150, 176, 154, 193,
	206, 222, 188, 152, 210, 243, 96, 41, 86, 180, 101, 177, 166, 141, 212, 116,
}

// winscpUnscramble deshace ScramblePassword de WinSCP, que se aplica a la
// contraseña antes de cifrarla con la maestra: relleno al azar sin dígitos +
// el largo en 3 dígitos (unidades primero) + la contraseña en UTF-8, cada
// byte pasado por la tabla según el anterior. Es UnscramblePassword de
// Cryptography.cpp.
func winscpUnscramble(b []byte) (string, bool) {
	var unscramble [256]byte
	for i, v := range winscpScrambleTable {
		unscramble[v] = byte(i)
	}
	s := append([]byte{}, b...)
	last := 31
	for i, c := range s {
		x := int(unscramble[c]) - 1 - last%255
		if x <= 0 {
			x += 255
		}
		s[i] = byte(x)
		last = (last+x)%255 + 1
	}
	p := 0
	for p < len(s) && (s[p] < '0' || s[p] > '9') {
		p++
	}
	if len(s)-p < 3 {
		return "", false
	}
	n := int(s[p]-'0') + 10*int(s[p+1]-'0') + 100*int(s[p+2]-'0')
	total := ((n+3)/17)*17 + 17
	if n < 0 || total != len(s) || total-p-3 != n {
		return "", false
	}
	return string(s[len(s)-n:]), true
}

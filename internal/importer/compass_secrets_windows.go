//go:build windows

package importer

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// safeStorage de Electron en Windows es el OSCrypt de Chromium: la clave
// (AES-256) está en "Local State" → os_crypt.encrypted_key, en base64 y con el
// prefijo "DPAPI", protegida con DPAPI (solo la abre el mismo usuario de
// Windows en esta PC). Cada dato es "v10" + nonce (12 bytes) + AES-GCM.
// Sin "v10" es un dato viejo protegido directamente con DPAPI.
type winSafeStorage struct{ key []byte }

func openSafeStorage(dir string) (safeStorage, error) {
	b, err := os.ReadFile(filepath.Join(dir, "Local State"))
	if errors.Is(err, os.ErrNotExist) {
		return winSafeStorage{}, nil // sin clave: solo datos DPAPI directos
	}
	if err != nil {
		return nil, err
	}
	var st struct {
		OSCrypt struct {
			EncryptedKey string `json:"encrypted_key"`
		} `json:"os_crypt"`
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, err
	}
	if st.OSCrypt.EncryptedKey == "" {
		return winSafeStorage{}, nil
	}
	enc, err := base64.StdEncoding.DecodeString(st.OSCrypt.EncryptedKey)
	if err != nil || !bytes.HasPrefix(enc, []byte("DPAPI")) {
		return nil, errors.New("la clave de Compass tiene un formato desconocido")
	}
	key, err := dpapiDecrypt(enc[len("DPAPI"):])
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errors.New("la clave de Compass tiene un largo inesperado")
	}
	return winSafeStorage{key: key}, nil
}

func (s winSafeStorage) decrypt(b []byte) ([]byte, error) {
	if !bytes.HasPrefix(b, []byte("v10")) {
		return dpapiDecrypt(b)
	}
	if s.key == nil {
		return nil, errors.New("falta la clave de Compass")
	}
	const nonceLen = 12
	b = b[3:]
	if len(b) < nonceLen+16 {
		return nil, errors.New("dato cifrado incompleto")
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, b[:nonceLen], b[nonceLen:], nil)
}

// dpapiDecrypt abre un dato protegido con DPAPI por el usuario actual.
func dpapiDecrypt(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, errors.New("dato vacío")
	}
	in := windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return bytes.Clone(unsafe.Slice(out.Data, out.Size)), nil
}

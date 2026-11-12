package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/zalando/go-keyring"
)

// En Android los secretos van a secrets.bin, en la carpeta privada de la app, cifrado con
// AES-GCM. La clave la pasa la app de Android en OSAT_SECRET_KEY (base64, 32 bytes): la guarda
// envuelta con una clave del Android Keystore, así que el archivo solo no alcanza para leerlos.

var secretsMu sync.Mutex

func init() {
	secretSet = func(service, user, secret string) error {
		return editSecrets(func(m map[string]string) { m[service+"\x00"+user] = secret })
	}
	secretGet = func(service, user string) (string, error) {
		secretsMu.Lock()
		defer secretsMu.Unlock()
		m, err := loadSecrets()
		if err != nil {
			return "", err
		}
		v, ok := m[service+"\x00"+user]
		if !ok {
			return "", keyring.ErrNotFound
		}
		return v, nil
	}
	secretDelete = func(service, user string) error {
		found := false
		err := editSecrets(func(m map[string]string) {
			_, found = m[service+"\x00"+user]
			delete(m, service+"\x00"+user)
		})
		if err == nil && !found {
			return keyring.ErrNotFound
		}
		return err
	}
}

func editSecrets(change func(map[string]string)) error {
	secretsMu.Lock()
	defer secretsMu.Unlock()
	m, err := loadSecrets()
	if err != nil {
		return err
	}
	change(m)
	return saveSecrets(m)
}

func secretsAEAD() (cipher.AEAD, string, error) {
	key, err := base64.StdEncoding.DecodeString(os.Getenv("OSAT_SECRET_KEY"))
	if err != nil || len(key) != 32 {
		return nil, "", errors.New("falta la clave de los secretos (OSAT_SECRET_KEY)")
	}
	dir, err := ConfigDir()
	if err != nil {
		return nil, "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, "", err
	}
	aead, err := cipher.NewGCM(block)
	return aead, filepath.Join(dir, "secrets.bin"), err
}

func loadSecrets() (map[string]string, error) {
	aead, path, err := secretsAEAD()
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	n := aead.NonceSize()
	if len(data) < n {
		return nil, errors.New("secrets.bin dañado")
	}
	plain, err := aead.Open(nil, data[:n], data[n:], nil)
	if err != nil {
		return nil, err
	}
	return m, json.Unmarshal(plain, &m)
}

func saveSecrets(m map[string]string) error {
	aead, path, err := secretsAEAD()
	if err != nil {
		return err
	}
	plain, err := json.Marshal(m)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, aead.Seal(nonce, nonce, plain, nil), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

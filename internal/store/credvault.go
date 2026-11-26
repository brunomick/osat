package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"

	"osat/internal/i18n"
)

// Cifrado de las credenciales de los sitios. Los datos de cada credencial (cuenta, usuario y contraseña) van al
// almacén del sistema cifrados con AES-256-GCM, y la clave queda aparte, en credentials.key (en Windows protegida
// con DPAPI, ver keyprotect_windows.go). Así otro programa que lea el almacén del sistema solo ve datos cifrados.
// No pide nada: no es una contraseña. Un programa que vaya a buscar a propósito el archivo de osat, corriendo como
// la misma persona, sí puede descifrarlas.
//
// Si la clave se pierde (se borró, o la carpeta de datos vino de otra PC), la lista (etiqueta y dominio, en
// credentials.json) queda y cada credencial se vuelve a cargar editándola.

// CodeCredsLost: los datos de la credencial no se pueden descifrar con la clave de esta PC.
const CodeCredsLost = "CREDS_LOST"

// CredsError viaja al frontend como "OSAT:{json}", como los demás errores con código.
type CredsError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *CredsError) Error() string {
	b, _ := json.Marshal(e)
	return "OSAT:" + string(b)
}

// sealedData son los datos de una credencial cifrados (nonce + texto cifrado).
type sealedData struct {
	Sealed []byte `json:"sealed"`
}

func (s *Store) credKeyPath() string { return filepath.Join(s.dir, "credentials.key") }

func gcmOf(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// credKeyLocked devuelve la clave, leyéndola (o creándola) la primera vez (s.keyMu tomado). Una clave dañada se
// reemplaza por una nueva: lo cifrado con la vieja queda perdido, pero lo nuevo se puede guardar.
func (s *Store) credKeyLocked() ([]byte, error) {
	if s.siteKey != nil {
		return s.siteKey, nil
	}
	b, err := os.ReadFile(s.credKeyPath())
	if err == nil {
		if key, err := unprotectKey(b); err == nil && len(key) == 32 {
			s.siteKey = key
			return key, nil
		}
		slog.Warn("credenciales de los sitios: la clave no se pudo leer, se crea otra", "err", err)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	p, err := protectKey(key)
	if err != nil {
		return nil, err
	}
	tmp := s.credKeyPath() + ".tmp"
	if err := os.WriteFile(tmp, p, 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, s.credKeyPath()); err != nil {
		return nil, err
	}
	s.siteKey = key
	return key, nil
}

func (s *Store) credKeyGet() ([]byte, error) {
	s.keyMu.Lock()
	defer s.keyMu.Unlock()
	return s.credKeyLocked()
}

// readCred lee del almacén los datos de la credencial id. Los de antes del cifrado (sin cifrar) también se leen.
func (s *Store) readCred(id string) (CredData, error) {
	raw, err := secretGet(keyringService, credKey(id))
	if err != nil {
		return CredData{}, i18n.Err("data.couldnTReadTheSiteCred", err)
	}
	plain := []byte(raw)
	var p sealedData
	if json.Unmarshal(plain, &p) == nil && p.Sealed != nil {
		lost := &CredsError{Code: CodeCredsLost, Message: i18n.T("data.credsLost")}
		key, err := s.credKeyGet()
		if err != nil {
			return CredData{}, lost
		}
		g, err := gcmOf(key)
		if err != nil || len(p.Sealed) < g.NonceSize() {
			return CredData{}, lost
		}
		n := g.NonceSize()
		if plain, err = g.Open(nil, p.Sealed[:n], p.Sealed[n:], []byte(credKey(id))); err != nil {
			return CredData{}, lost
		}
	}
	var d CredData
	if err := json.Unmarshal(plain, &d); err != nil {
		return CredData{}, i18n.Err("data.couldnTReadTheSiteCred", err)
	}
	return d, nil
}

// writeCred guarda cifrados los datos de la credencial id.
func (s *Store) writeCred(id string, d CredData) error {
	plain, err := json.Marshal(d)
	if err != nil {
		return err
	}
	key, err := s.credKeyGet()
	if err != nil {
		return i18n.Err("data.couldnTSaveTheSecret", err)
	}
	g, err := gcmOf(key)
	if err != nil {
		return err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	b, _ := json.Marshal(sealedData{Sealed: g.Seal(nonce, nonce, plain, []byte(credKey(id)))})
	if err := secretSet(keyringService, credKey(id), string(b)); err != nil {
		return i18n.Err("data.couldnTSaveTheSecret", err)
	}
	return nil
}

// SealCreds cifra las credenciales que están sin cifrar (las de antes de que existiera el cifrado). Osat la llama
// al arrancar.
func (s *Store) SealCreds() error {
	s.mu.RLock()
	ids := make([]string, len(s.creds))
	for i, c := range s.creds {
		ids[i] = c.ID
	}
	s.mu.RUnlock()
	var errs []error
	for _, id := range ids {
		raw, err := secretGet(keyringService, credKey(id))
		if err != nil {
			continue
		}
		var p sealedData
		if json.Unmarshal([]byte(raw), &p) == nil && p.Sealed != nil {
			continue
		}
		var d CredData
		if json.Unmarshal([]byte(raw), &d) != nil {
			continue
		}
		if err := s.writeCred(id, d); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

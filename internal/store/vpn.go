package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"osat/internal/i18n"
)

// VPN es la VPN WireGuard guardada en vpn.json: el .conf sin la clave privada
// (que va al keyring) y si está activada.
type VPN struct {
	Conf    string `json:"conf"`
	Enabled bool   `json:"enabled"`
}

// vpnKeyID es la entrada del keyring con la clave privada de WireGuard.
const vpnKeyID = "vpn-private-key"

func (s *Store) vpnPath() string { return filepath.Join(s.dir, "vpn.json") }

// VPN devuelve la VPN guardada (Conf vacío si no hay).
func (s *Store) VPN() VPN {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var v VPN
	if b, err := os.ReadFile(s.vpnPath()); err == nil {
		json.Unmarshal(b, &v)
	}
	return v
}

// SaveVPN guarda la VPN y, si key no está vacía, su clave privada en el keyring.
func (s *Store) SaveVPN(v VPN, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key != "" {
		if err := secretSet(keyringService, vpnKeyID, key); err != nil {
			return i18n.Err("data.couldnTSaveTheSecret", err)
		}
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.vpnPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.vpnPath())
}

// VPNKey lee la clave privada de la VPN ("" si no hay).
func (s *Store) VPNKey() string {
	v, err := secretGet(keyringService, vpnKeyID)
	if err != nil {
		return ""
	}
	return v
}

// DeleteVPN borra la VPN y su clave.
func (s *Store) DeleteVPN() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	deleteSecret(keyringService, vpnKeyID)
	if err := os.Remove(s.vpnPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

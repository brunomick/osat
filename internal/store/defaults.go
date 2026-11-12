package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/zalando/go-keyring"

	"osat/internal/i18n"
)

// DefaultCreds son las credenciales por defecto: las usan las conexiones con
// UseDefaultCreds y, si les falta, las que no tienen usuario o contraseña
// propios. Se guardan en defaults.json; la contraseña, en el keyring.
type DefaultCreds struct {
	User      string `json:"user"`
	Domain    string `json:"domain,omitempty"` // solo RDP
	HasSecret bool   `json:"hasSecret"`
}

// DefaultCredsInput es lo que envía el frontend. Secret vacío + KeepSecret=true
// conserva la contraseña guardada.
type DefaultCredsInput struct {
	DefaultCreds
	Secret     string `json:"secret"`
	KeepSecret bool   `json:"keepSecret"`
}

// defaultSecretID es la entrada del keyring de la contraseña por defecto (no choca con los uuid de las conexiones).
const defaultSecretID = "default-credentials"

func (s *Store) defaultsPath() string { return filepath.Join(s.dir, "defaults.json") }

func (s *Store) loadDefaults() error {
	b, err := os.ReadFile(s.defaultsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &s.defaults); err != nil {
		return i18n.Err("data.invalidDefaultsJsonV", err)
	}
	return nil
}

func (s *Store) saveDefaultsLocked() error {
	b, err := json.MarshalIndent(s.defaults, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.defaultsPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.defaultsPath())
}

// Defaults devuelve las credenciales por defecto (sin la contraseña).
func (s *Store) Defaults() DefaultCreds {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.defaults
}

// SaveDefaults guarda las credenciales por defecto y su contraseña en el keyring.
func (s *Store) SaveDefaults(in DefaultCredsInput) (DefaultCreds, error) {
	d := DefaultCreds{User: strings.TrimSpace(in.User), Domain: strings.TrimSpace(in.Domain)}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case in.Secret != "":
		if err := keyring.Set(keyringService, defaultSecretID, in.Secret); err != nil {
			return DefaultCreds{}, i18n.Err("data.couldnTSaveTheSecret", err)
		}
		d.HasSecret = true
	case in.KeepSecret:
		d.HasSecret = s.defaults.HasSecret
	default:
		deleteSecret(keyringService, defaultSecretID)
	}
	s.defaults = d
	return d, s.saveDefaultsLocked()
}

// DefaultSecret lee la contraseña por defecto ("" si no hay).
func (s *Store) DefaultSecret() string {
	if !s.Defaults().HasSecret {
		return ""
	}
	v, err := keyring.Get(keyringService, defaultSecretID)
	if err != nil {
		return ""
	}
	return v
}

// WithDefaults devuelve c con el usuario (y el dominio, en RDP) por defecto
// donde corresponde: siempre si usa las credenciales por defecto, y si no,
// solo los que le faltan.
func (s *Store) WithDefaults(c Connection) Connection {
	if c.Protocol == ProtoVNC {
		return c // VNC no tiene usuario
	}
	d := s.Defaults()
	if d.User != "" && (c.UseDefaultCreds || c.User == "") {
		c.User = d.User
	}
	if c.Protocol == ProtoRDP && d.Domain != "" && (c.UseDefaultCreds || c.Domain == "") {
		c.Domain = d.Domain
	}
	return c
}

// SecretFor devuelve la contraseña con la que conectar c: la por defecto si
// la usa, si no la suya y, si no tiene, la por defecto. Para una clave o el
// agente no hay contraseña por defecto (el secreto sería la passphrase).
func (s *Store) SecretFor(c Connection) string {
	if c.UseDefaultCreds {
		return s.DefaultSecret()
	}
	if c.HasSecret {
		if v := s.Secret(c.ID); v != "" {
			return v
		}
	}
	if c.AuthType == AuthPassword || c.Protocol == ProtoVNC {
		return s.DefaultSecret()
	}
	return ""
}

// SetUseDefaultCreds marca (o desmarca) las conexiones del grupo group y sus
// subgrupos ("" = todas) para que usen las credenciales por defecto. Devuelve cuántas cambiaron.
func (s *Store) SetUseDefaultCreds(group string, on bool) (int, error) {
	group = NormalizeGroup(group)
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, c := range s.conns {
		if group != "" && !isWithin(c.Group, group) || c.UseDefaultCreds == on {
			continue
		}
		c.UseDefaultCreds = on
		if on && c.Protocol != ProtoVNC && c.Protocol != ProtoRDP {
			c.AuthType, c.KeyPath = AuthPassword, "" // SSH: la por defecto es una contraseña
		}
		s.conns[id] = c
		n++
	}
	if n == 0 {
		return 0, nil
	}
	return n, s.saveLocked()
}

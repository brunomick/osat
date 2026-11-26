package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"osat/internal/i18n"
)

// Credenciales para los sitios web: un baúl único, sin relación con los sitios guardados. En
// credentials.json solo van el id, la etiqueta y el dominio; cuenta, usuario y contraseña van juntos en una
// entrada del keyring por credencial (credKey).

// Cred es una credencial tal como queda en credentials.json. Domain (opcional) es el host donde se usa: la
// llave del sitio la marca en esas páginas (y sus subdominios) y solo ahí se puede rellenar el login.
type Cred struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Domain string `json:"domain,omitempty"`
}

// CredData son los datos de una credencial (lo que va al keyring).
type CredData struct {
	Account  string `json:"account"`
	User     string `json:"user"`
	Password string `json:"password"`
}

// CredInput es lo que manda el frontend para crear (ID vacío) o editar una credencial.
// Password vacío con KeepPassword conserva la contraseña guardada.
type CredInput struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	Domain       string `json:"domain"`
	Account      string `json:"account"`
	User         string `json:"user"`
	Password     string `json:"password"`
	KeepPassword bool   `json:"keepPassword"`
}

var (
	domainRe = regexp.MustCompile(`^[a-z0-9-]+(\.[a-z0-9-]+)*$`)
	portRe   = regexp.MustCompile(`^[0-9]+$`)
)

// NormalizeCredDomain deja el dominio de una credencial como host en minúsculas. Acepta una dirección
// entera ("https://panel.x.com/login" → "panel.x.com"), con puerto, o con "*." adelante. Vacío es válido
// (credencial sin dominio).
func NormalizeCredDomain(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", nil
	}
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, ":"); i >= 0 && portRe.MatchString(s[i+1:]) {
		s = s[:i]
	}
	s = strings.Trim(strings.TrimPrefix(s, "*."), ".")
	if !domainRe.MatchString(s) {
		return "", i18n.Err("data.siteCredBadDomain")
	}
	return s, nil
}

// credKey es el nombre de la entrada del keyring de una credencial (no choca con los uuid de las conexiones).
func credKey(id string) string { return "site-cred:" + id }

func (s *Store) credsPath() string { return filepath.Join(s.dir, "credentials.json") }

type credsFile struct {
	Version int    `json:"version"`
	Creds   []Cred `json:"creds"`
}

func (s *Store) loadCreds() error {
	b, err := os.ReadFile(s.credsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var f credsFile
	if err := json.Unmarshal(b, &f); err != nil {
		return i18n.Err("data.invalidCredentialsJsonV", err)
	}
	s.creds = f.Creds
	return nil
}

func (s *Store) saveCredsLocked() error {
	b, err := json.MarshalIndent(credsFile{Version: 1, Creds: s.creds}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.credsPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.credsPath())
}

func (s *Store) credIndexLocked(id string) int {
	for i, c := range s.creds {
		if c.ID == id {
			return i
		}
	}
	return -1
}

// checkCredLabelLocked normaliza la etiqueta y comprueba que no esté vacía ni repetida
// (sin contar la credencial selfID, que se está editando).
func (s *Store) checkCredLabelLocked(label, selfID string) (string, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return "", i18n.Err("data.theSiteCredNeedsALabel")
	}
	for _, c := range s.creds {
		if c.ID != selfID && strings.EqualFold(c.Label, label) {
			return "", i18n.Err("data.siteCredLabelExists", label)
		}
	}
	return label, nil
}

// addCredLocked guarda una credencial nueva en el keyring y la suma en memoria; no guarda credentials.json.
func (s *Store) addCredLocked(in CredInput) (Cred, error) {
	label, err := s.checkCredLabelLocked(in.Label, "")
	if err != nil {
		return Cred{}, err
	}
	domain, err := NormalizeCredDomain(in.Domain)
	if err != nil {
		return Cred{}, err
	}
	c := Cred{ID: uuid.NewString(), Label: label, Domain: domain}
	if err := s.writeCred(c.ID, CredData{Account: in.Account, User: in.User, Password: in.Password}); err != nil {
		return Cred{}, err
	}
	s.creds = append(s.creds, c)
	return c, nil
}

// Creds son todas las credenciales, en el orden de alta.
func (s *Store) Creds() []Cred {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Cred{}, s.creds...)
}

// SaveCred crea (in.ID vacío) o edita una credencial.
func (s *Store) SaveCred(in CredInput) (Cred, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.creds
	if in.ID == "" {
		c, err := s.addCredLocked(in)
		if err != nil {
			return Cred{}, err
		}
		if err := s.saveCredsLocked(); err != nil {
			s.creds = old
			deleteSecret(keyringService, credKey(c.ID))
			return Cred{}, err
		}
		return c, nil
	}
	i := s.credIndexLocked(in.ID)
	if i < 0 {
		return Cred{}, i18n.Err("data.siteCredDoesNotExist")
	}
	label, err := s.checkCredLabelLocked(in.Label, in.ID)
	if err != nil {
		return Cred{}, err
	}
	domain, err := NormalizeCredDomain(in.Domain)
	if err != nil {
		return Cred{}, err
	}
	d := CredData{Account: in.Account, User: in.User, Password: in.Password}
	if in.KeepPassword && in.Password == "" {
		prev, err := s.readCred(in.ID)
		if err != nil {
			return Cred{}, err
		}
		d.Password = prev.Password
	}
	if err := s.writeCred(in.ID, d); err != nil {
		return Cred{}, err
	}
	c := Cred{ID: in.ID, Label: label, Domain: domain}
	s.creds = append([]Cred{}, old...)
	s.creds[i] = c
	if err := s.saveCredsLocked(); err != nil {
		s.creds = old
		return Cred{}, err
	}
	return c, nil
}

// CredData lee del keyring los datos de la credencial id.
func (s *Store) CredData(id string) (CredData, error) {
	s.mu.RLock()
	i := s.credIndexLocked(id)
	s.mu.RUnlock()
	if i < 0 {
		return CredData{}, i18n.Err("data.siteCredDoesNotExist")
	}
	return s.readCred(id)
}

// DeleteCred quita la credencial y la borra del keyring.
func (s *Store) DeleteCred(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.credIndexLocked(id)
	if i < 0 {
		return nil
	}
	old := s.creds
	s.creds = append(append([]Cred{}, old[:i]...), old[i+1:]...)
	if err := s.saveCredsLocked(); err != nil {
		s.creds = old
		return err
	}
	deleteSecret(keyringService, credKey(id))
	return nil
}

// ImportCreds da de alta las credenciales de un .osat. Una cuya etiqueta ya existe se saltea
// (para no pisar nada; así importar dos veces no duplica) y se informa. Devuelve cuántas agregó.
func (s *Store) ImportCreds(in []CredInput) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.creds
	var added []string
	var errs []error
	for _, c := range in {
		c.ID, c.KeepPassword = "", false
		nc, err := s.addCredLocked(c)
		if err != nil {
			errs = append(errs, fmt.Errorf("«%s»: %w", strings.TrimSpace(c.Label), err))
			continue
		}
		added = append(added, nc.ID)
	}
	if len(added) > 0 {
		if err := s.saveCredsLocked(); err != nil {
			s.creds = old
			for _, id := range added {
				deleteSecret(keyringService, credKey(id))
			}
			return 0, err
		}
	}
	return len(added), errors.Join(errs...)
}

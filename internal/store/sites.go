package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"

	"osat/internal/i18n"
)

// Site es un sitio web guardado (un panel de administración, una consola web).
// Se guardan aparte de las conexiones, en sites.json, y no llevan secretos.
type Site struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	URL   string `json:"url"` // solo http:// o https://
	Order int    `json:"order"`
}

// Validate normaliza y comprueba el sitio.
func (st *Site) Validate() error {
	st.Name = strings.TrimSpace(st.Name)
	st.URL = strings.TrimSpace(st.URL)
	if st.Name == "" {
		return i18n.Err("data.theSiteNeedsAName")
	}
	u, err := url.Parse(st.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return i18n.Err("data.theSiteUrlMustBe")
	}
	return nil
}

func (s *Store) sitesPath() string { return filepath.Join(s.dir, "sites.json") }

type sitesFile struct {
	Version int    `json:"version"`
	Sites   []Site `json:"sites"`
}

func (s *Store) loadSites() error {
	b, err := os.ReadFile(s.sitesPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var f sitesFile
	if err := json.Unmarshal(b, &f); err != nil {
		return i18n.Err("data.invalidSitesJsonV", err)
	}
	s.sites = map[string]Site{}
	for _, st := range f.Sites {
		s.sites[st.ID] = st
	}
	return nil
}

func (s *Store) saveSitesLocked() error {
	out := sitesFile{Version: 1, Sites: s.sitesLocked()}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.sitesPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.sitesPath())
}

// sitesLocked devuelve todos, ordenados por Order (y por id si empatan).
func (s *Store) sitesLocked() []Site {
	out := make([]Site, 0, len(s.sites))
	for _, st := range s.sites {
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// nextSiteOrderLocked es el Order que deja un sitio nuevo al final.
func (s *Store) nextSiteOrderLocked() int {
	n := 0
	for _, st := range s.sites {
		if st.Order > n {
			n = st.Order
		}
	}
	return n + 1
}

// Sites son todos los sitios guardados, en el orden de la barra lateral.
func (s *Store) Sites() []Site {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sitesLocked()
}

// SaveSite crea (ID vacío, al final de la lista) o actualiza (nombre y URL) un sitio.
func (s *Store) SaveSite(st Site) (Site, error) {
	if err := st.Validate(); err != nil {
		return Site{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if st.ID == "" {
		st.ID = uuid.NewString()
		st.Order = s.nextSiteOrderLocked()
	} else {
		old, ok := s.sites[st.ID]
		if !ok {
			return Site{}, i18n.Err("data.siteDoesNotExist")
		}
		st.Order = old.Order
	}
	if s.sites == nil {
		s.sites = map[string]Site{}
	}
	s.sites[st.ID] = st
	return st, s.saveSitesLocked()
}

func (s *Store) DeleteSite(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sites[id]; !ok {
		return nil
	}
	delete(s.sites, id)
	return s.saveSitesLocked()
}

// PlaceSite mueve el sitio id antes de beforeID ("" = al final) y renumera todos de 1 a n.
func (s *Store) PlaceSite(id, beforeID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sites[id]; !ok {
		return i18n.Err("data.siteDoesNotExist")
	}
	if beforeID != "" {
		if _, ok := s.sites[beforeID]; !ok {
			return i18n.Err("data.siteDoesNotExist")
		}
	}
	var ids []string
	for _, st := range s.sitesLocked() {
		if st.ID != id {
			ids = append(ids, st.ID)
		}
	}
	at := len(ids)
	for i, x := range ids {
		if x == beforeID {
			at = i
			break
		}
	}
	ids = append(ids[:at], append([]string{id}, ids[at:]...)...)
	for i, x := range ids {
		st := s.sites[x]
		st.Order = i + 1
		s.sites[x] = st
	}
	return s.saveSitesLocked()
}

// ImportSites da de alta los sitios de un .osat al final de la lista. Uno con la
// misma URL que uno existente no se duplica. Devuelve cuántos agregó; los
// inválidos se saltean y se informan.
func (s *Store) ImportSites(in []Site) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	have := make(map[string]bool, len(s.sites))
	for _, st := range s.sites {
		have[st.URL] = true
	}
	n := 0
	var errs []error
	for _, st := range in {
		if err := st.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("sitio «%s»: %w", st.Name, err))
			continue
		}
		if have[st.URL] {
			continue
		}
		have[st.URL] = true
		st.ID = uuid.NewString()
		st.Order = s.nextSiteOrderLocked()
		if s.sites == nil {
			s.sites = map[string]Site{}
		}
		s.sites[st.ID] = st
		n++
	}
	if n > 0 {
		if err := s.saveSitesLocked(); err != nil {
			return 0, err
		}
	}
	return n, errors.Join(errs...)
}

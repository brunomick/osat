package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"

	"osat/internal/i18n"
)

// Snippet es un comando guardado para mandarlo a una terminal. Se guardan
// aparte de las conexiones, en snippets.json, y no llevan secretos.
type Snippet struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Folder  string `json:"folder"`  // ruta "A/B" para ordenarlos en el panel; "" = sin carpeta
	Command string `json:"command"` // puede tener varias líneas y variables {{nombre}}
	Mode    string `json:"mode"`    // SnippetPaste o SnippetRun
	Group   string `json:"group"`   // grupo de conexiones donde aparece (con sus subgrupos); "" = global
}

// Modos de un snippet (Snippet.Mode).
const (
	SnippetPaste = "paste" // se escribe sin Enter
	SnippetRun   = "run"   // se escribe con Enter
)

// Validate normaliza y comprueba el snippet.
func (sn *Snippet) Validate() error {
	sn.Name = strings.TrimSpace(sn.Name)
	sn.Folder = NormalizeGroup(sn.Folder)
	sn.Group = NormalizeGroup(sn.Group)
	if sn.Name == "" {
		return i18n.Err("data.theSnippetNeedsAName")
	}
	if strings.TrimSpace(sn.Command) == "" {
		return i18n.Err("data.theCommandIsEmpty")
	}
	switch sn.Mode {
	case SnippetPaste, SnippetRun:
	default:
		return i18n.Err("data.unknownSnippetModeQ", sn.Mode)
	}
	return nil
}

func (s *Store) snippetsPath() string { return filepath.Join(s.dir, "snippets.json") }

type snippetsFile struct {
	Version  int       `json:"version"`
	Snippets []Snippet `json:"snippets"`
}

func (s *Store) loadSnippets() error {
	b, err := os.ReadFile(s.snippetsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var f snippetsFile
	if err := json.Unmarshal(b, &f); err != nil {
		return i18n.Err("data.invalidSnippetsJsonV", err)
	}
	s.snippets = map[string]Snippet{}
	for _, sn := range f.Snippets {
		s.snippets[sn.ID] = sn
	}
	return nil
}

func (s *Store) saveSnippetsLocked() error {
	out := snippetsFile{Version: 1, Snippets: s.snippetsLocked()}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.snippetsPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.snippetsPath())
}

// saveWithSnippetsLocked guarda las conexiones y, si cambiaron los snippets
// (porque siguieron a un grupo), también snippets.json.
func (s *Store) saveWithSnippetsLocked(snippetsChanged bool) error {
	if err := s.saveLocked(); err != nil {
		return err
	}
	if snippetsChanged {
		return s.saveSnippetsLocked()
	}
	return nil
}

// snippetsLocked devuelve todos, ordenados por carpeta, nombre e id.
func (s *Store) snippetsLocked() []Snippet {
	out := make([]Snippet, 0, len(s.snippets))
	for _, sn := range s.snippets {
		out = append(out, sn)
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := strings.ToLower(out[i].Folder), strings.ToLower(out[j].Folder); a != b {
			return a < b
		}
		if a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name); a != b {
			return a < b
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Snippets son todos los snippets guardados.
func (s *Store) Snippets() []Snippet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snippetsLocked()
}

// SaveSnippet crea (ID vacío) o actualiza un snippet.
func (s *Store) SaveSnippet(sn Snippet) (Snippet, error) {
	if err := sn.Validate(); err != nil {
		return Snippet{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if sn.ID == "" {
		sn.ID = uuid.NewString()
	} else if _, ok := s.snippets[sn.ID]; !ok {
		return Snippet{}, i18n.Err("data.snippetDoesNotExist")
	}
	if s.snippets == nil {
		s.snippets = map[string]Snippet{}
	}
	s.snippets[sn.ID] = sn
	return sn, s.saveSnippetsLocked()
}

func (s *Store) DeleteSnippet(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.snippets[id]; !ok {
		return nil
	}
	delete(s.snippets, id)
	return s.saveSnippetsLocked()
}

// ImportSnippets da de alta los snippets de un .osat con su grupo tal como
// vienen. Uno igual a uno existente (mismo nombre, carpeta y grupo) no se
// duplica. Devuelve cuántos agregó; los inválidos se saltean y se informan.
func (s *Store) ImportSnippets(in []Snippet) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	type key struct{ name, folder, group string }
	have := make(map[key]bool, len(s.snippets))
	for _, sn := range s.snippets {
		have[key{sn.Name, sn.Folder, sn.Group}] = true
	}
	n := 0
	var errs []error
	for _, sn := range in {
		if err := sn.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("snippet «%s»: %w", sn.Name, err))
			continue
		}
		k := key{sn.Name, sn.Folder, sn.Group}
		if have[k] {
			continue
		}
		have[k] = true
		sn.ID = uuid.NewString()
		if s.snippets == nil {
			s.snippets = map[string]Snippet{}
		}
		s.snippets[sn.ID] = sn
		n++
	}
	if n > 0 {
		if err := s.saveSnippetsLocked(); err != nil {
			return 0, err
		}
	}
	return n, errors.Join(errs...)
}

// moveSnippetsLocked pasa los snippets del grupo from (y sus subgrupos) al grupo
// to, como las conexiones. Devuelve si cambió alguno (para guardar).
func (s *Store) moveSnippetsLocked(from, to string) bool {
	changed := false
	for id, sn := range s.snippets {
		if g, ok := moveGroupPath(sn.Group, from, to); ok {
			sn.Group = g
			s.snippets[id] = sn
			changed = true
		}
	}
	return changed
}

// dropSnippetsWithinLocked borra los snippets del grupo g y sus subgrupos. Con g
// vacío borra los de cualquier grupo (los globales no son de ninguno y quedan).
// Devuelve si borró alguno (para guardar).
func (s *Store) dropSnippetsWithinLocked(g string) bool {
	changed := false
	for id, sn := range s.snippets {
		if sn.Group != "" && (g == "" || isWithin(sn.Group, g)) {
			delete(s.snippets, id)
			changed = true
		}
	}
	return changed
}

package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"

	"osat/internal/i18n"
)

// Un espacio de trabajo es el conjunto de pestañas abiertas y cómo están
// repartidas en el mosaico. Go no mira lo que tiene adentro (Data): su forma la
// define el frontend. workspaces.json guarda la última sesión (para restaurarla
// al abrir osat) y los espacios con nombre. No lleva secretos.

// Workspace es un espacio de trabajo con nombre.
type Workspace struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Data json.RawMessage `json:"data"` // un objeto JSON, opaco para Go
}

func (s *Store) workspacesPath() string { return filepath.Join(s.dir, "workspaces.json") }

type workspacesFile struct {
	Version int             `json:"version"`
	Last    json.RawMessage `json:"last,omitempty"`
	Named   []Workspace     `json:"named"`
}

// cleanWorkspaceData comprueba que sea un objeto JSON y lo devuelve compacto.
func cleanWorkspaceData(data json.RawMessage) (json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil || obj == nil {
		return nil, i18n.Err("data.theWorkspaceDataIsNot")
	}
	var out bytes.Buffer
	if err := json.Compact(&out, data); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// loadWorkspaces lee workspaces.json. Se escribe muy seguido (la última sesión), así que un
// archivo que no se puede leer o interpretar no frena el arranque: se aparta como
// workspaces.json.bak, se registra y se sigue con el estado vacío.
func (s *Store) loadWorkspaces() {
	b, err := os.ReadFile(s.workspacesPath())
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	var f workspacesFile
	if err == nil {
		if err = json.Unmarshal(b, &f); err != nil {
			err = i18n.Err("data.invalidWorkspacesJsonV", err)
		}
	}
	if err != nil {
		s.setAsideWorkspaces(err)
		return
	}
	// Al guardar con sangría los datos quedan con espacios: se vuelven a compactar.
	s.lastWorkspace = compactJSON(f.Last)
	s.workspaces = map[string]Workspace{}
	for _, w := range f.Named {
		w.Data = compactJSON(w.Data)
		s.workspaces[w.ID] = w
	}
}

// setAsideWorkspaces renombra el workspaces.json que no sirve a workspaces.json.bak (pisando uno anterior).
func (s *Store) setAsideWorkspaces(cause error) {
	path := s.workspacesPath()
	if err := os.Rename(path, path+".bak"); err != nil {
		slog.Warn("workspaces.json no se pudo leer y no se pudo apartar: se empieza sin espacios de trabajo", "err", cause, "rename", err)
		return
	}
	slog.Warn("workspaces.json no se pudo leer: se apartó como workspaces.json.bak y se empieza sin espacios de trabajo", "err", cause)
}

func compactJSON(b json.RawMessage) json.RawMessage {
	var out bytes.Buffer
	if err := json.Compact(&out, b); err != nil || out.Len() == 0 {
		return b
	}
	return out.Bytes()
}

func (s *Store) saveWorkspacesLocked() error {
	out := workspacesFile{Version: 1, Last: s.lastWorkspace, Named: s.workspacesLocked()}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.workspacesPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.workspacesPath())
}

// workspacesLocked devuelve los espacios con nombre, ordenados por nombre.
func (s *Store) workspacesLocked() []Workspace {
	out := make([]Workspace, 0, len(s.workspaces))
	for _, w := range s.workspaces {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name); a != b {
			return a < b
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// nameTakenLocked: otro espacio (no el de id) ya se llama así, sin distinguir mayúsculas.
func (s *Store) nameTakenLocked(id, name string) bool {
	for _, w := range s.workspaces {
		if w.ID != id && strings.EqualFold(w.Name, name) {
			return true
		}
	}
	return false
}

// WorkspaceLast devuelve la última sesión guardada (nil si no hay).
func (s *Store) WorkspaceLast() json.RawMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastWorkspace
}

// SetWorkspaceLast guarda la última sesión. data vacío o null la borra.
func (s *Store) SetWorkspaceLast(data json.RawMessage) error {
	var clean json.RawMessage
	if t := bytes.TrimSpace(data); len(t) > 0 && !bytes.Equal(t, []byte("null")) {
		var err error
		if clean, err = cleanWorkspaceData(data); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastWorkspace = clean
	return s.saveWorkspacesLocked()
}

// Workspaces son los espacios con nombre, ordenados por nombre.
func (s *Store) Workspaces() []Workspace {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.workspacesLocked()
}

// SaveWorkspace crea (id vacío) o actualiza un espacio con nombre.
func (s *Store) SaveWorkspace(id, name string, data json.RawMessage) (Workspace, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Workspace{}, i18n.Err("data.theWorkspaceNeedsAName")
	}
	clean, err := cleanWorkspaceData(data)
	if err != nil {
		return Workspace{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		id = uuid.NewString()
	} else if _, ok := s.workspaces[id]; !ok {
		return Workspace{}, i18n.Err("data.workspaceDoesNotExist")
	}
	if s.nameTakenLocked(id, name) {
		return Workspace{}, i18n.Err("data.thereIsAlreadyAWorkspace", name)
	}
	if s.workspaces == nil {
		s.workspaces = map[string]Workspace{}
	}
	w := Workspace{ID: id, Name: name, Data: clean}
	s.workspaces[id] = w
	return w, s.saveWorkspacesLocked()
}

// RenameWorkspace cambia el nombre de un espacio, sin tocar su contenido.
func (s *Store) RenameWorkspace(id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return i18n.Err("data.theWorkspaceNeedsAName")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.workspaces[id]
	if !ok {
		return i18n.Err("data.workspaceDoesNotExist")
	}
	if s.nameTakenLocked(id, name) {
		return i18n.Err("data.thereIsAlreadyAWorkspace", name)
	}
	w.Name = name
	s.workspaces[id] = w
	return s.saveWorkspacesLocked()
}

func (s *Store) DeleteWorkspace(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.workspaces[id]; !ok {
		return nil
	}
	delete(s.workspaces, id)
	return s.saveWorkspacesLocked()
}

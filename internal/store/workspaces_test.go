package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

// reloadWorkspaces lee de nuevo workspaces.json, como al reiniciar la app.
func reloadWorkspaces(t *testing.T, s *Store) *Store {
	t.Helper()
	n := &Store{dir: s.dir, path: s.path, conns: map[string]Connection{}}
	n.loadWorkspaces()
	return n
}

func TestWorkspaceLastIsSavedAndReloaded(t *testing.T) {
	s := newTestStore(t)
	if s.WorkspaceLast() != nil {
		t.Fatalf("sin archivo no hay última sesión: %s", s.WorkspaceLast())
	}
	data := json.RawMessage(`{ "version": 1, "tabs": [ {"ref":"t1","title":"Año ñandú"} ] }`)
	if err := s.SetWorkspaceLast(data); err != nil {
		t.Fatal(err)
	}
	got := string(reloadWorkspaces(t, s).WorkspaceLast())
	if want := `{"version":1,"tabs":[{"ref":"t1","title":"Año ñandú"}]}`; got != want {
		t.Fatalf("última sesión = %s, quería %s", got, want)
	}
	// null o vacío la borra.
	for _, empty := range []string{"null", "", "  "} {
		s.SetWorkspaceLast(data)
		if err := s.SetWorkspaceLast(json.RawMessage(empty)); err != nil {
			t.Fatalf("borrar con %q: %v", empty, err)
		}
		if reloadWorkspaces(t, s).WorkspaceLast() != nil {
			t.Fatalf("no se borró con %q", empty)
		}
	}
}

func TestWorkspaceDataMustBeAnObject(t *testing.T) {
	s := newTestStore(t)
	for _, bad := range []string{`[1]`, `"x"`, `5`, `{oops`, `{"a":1} {"b":2}`} {
		if err := s.SetWorkspaceLast(json.RawMessage(bad)); err == nil {
			t.Errorf("la última sesión aceptó %s", bad)
		}
		if _, err := s.SaveWorkspace("", "x", json.RawMessage(bad)); err == nil {
			t.Errorf("un espacio con nombre aceptó %s", bad)
		}
	}
	if _, err := s.SaveWorkspace("", "x", nil); err == nil {
		t.Error("aceptó datos vacíos")
	}
	if _, err := os.Stat(s.workspacesPath()); err == nil {
		t.Fatal("los datos inválidos no deberían crear el archivo")
	}
}

func TestWorkspacesAreSavedAndReloaded(t *testing.T) {
	s := newTestStore(t)
	if got := s.Workspaces(); got == nil || len(got) != 0 {
		t.Fatalf("sin archivo debería ser una lista vacía, fue %#v", got)
	}
	web, err := s.SaveWorkspace("", "  Web  ", json.RawMessage(`{"tabs":[1]}`))
	if err != nil {
		t.Fatal(err)
	}
	if web.ID == "" || web.Name != "Web" {
		t.Fatalf("se esperaba id y nombre sin espacios: %+v", web)
	}
	if _, err := s.SaveWorkspace("", "base", json.RawMessage(`{"tabs":[2]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveWorkspace("", "Alfa", json.RawMessage(`{"tabs":[3]}`)); err != nil {
		t.Fatal(err)
	}

	names := func(l []Workspace) string {
		var out []string
		for _, w := range l {
			out = append(out, w.Name)
		}
		return strings.Join(out, ",")
	}
	again := reloadWorkspaces(t, s)
	if got := names(again.Workspaces()); got != "Alfa,base,Web" { // por nombre, sin distinguir mayúsculas
		t.Fatalf("orden = %s", got)
	}
	for _, w := range again.Workspaces() {
		if w.Name == "Web" && string(w.Data) != `{"tabs":[1]}` {
			t.Fatalf("datos de Web = %s", w.Data)
		}
	}

	// Actualizar conserva el id y cambia los datos (y puede cambiar el nombre).
	up, err := s.SaveWorkspace(web.ID, "Web 2", json.RawMessage(`{"tabs":[9]}`))
	if err != nil || up.ID != web.ID {
		t.Fatalf("actualizar: %+v, %v", up, err)
	}
	again = reloadWorkspaces(t, s)
	if len(again.Workspaces()) != 3 || names(again.Workspaces()) != "Alfa,base,Web 2" {
		t.Fatalf("después de actualizar: %s", names(again.Workspaces()))
	}
	if _, err := s.SaveWorkspace("no-existe", "x", json.RawMessage(`{}`)); err == nil {
		t.Fatal("actualizó un espacio que no existe")
	}
	if _, err := s.SaveWorkspace("", "  ", json.RawMessage(`{}`)); err == nil {
		t.Fatal("guardó un espacio sin nombre")
	}
}

func TestWorkspaceRenameAndDelete(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.SaveWorkspace("", "A", json.RawMessage(`{"k":"a"}`))
	b, _ := s.SaveWorkspace("", "B", json.RawMessage(`{"k":"b"}`))

	if err := s.RenameWorkspace(a.ID, " Principal "); err != nil {
		t.Fatal(err)
	}
	for _, w := range reloadWorkspaces(t, s).Workspaces() {
		if w.ID == a.ID && (w.Name != "Principal" || string(w.Data) != `{"k":"a"}`) {
			t.Fatalf("renombrar no debe tocar los datos: %+v", w)
		}
	}
	if s.RenameWorkspace("no-existe", "x") == nil || s.RenameWorkspace(a.ID, " ") == nil {
		t.Fatal("renombró un espacio inexistente o sin nombre")
	}

	if err := s.DeleteWorkspace(b.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteWorkspace("no-existe"); err != nil {
		t.Fatal(err)
	}
	if got := reloadWorkspaces(t, s).Workspaces(); len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("lo borrado también tiene que salir del archivo: %+v", got)
	}
}

func TestWorkspaceNamesAreUnique(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.SaveWorkspace("", "Prod", json.RawMessage(`{}`))
	b, _ := s.SaveWorkspace("", "Lab", json.RawMessage(`{}`))
	if _, err := s.SaveWorkspace("", "prod", json.RawMessage(`{}`)); err == nil {
		t.Fatal("creó otro con el mismo nombre (sin distinguir mayúsculas)")
	}
	if err := s.RenameWorkspace(b.ID, "PROD"); err == nil {
		t.Fatal("renombró a un nombre que ya existe")
	}
	if _, err := s.SaveWorkspace(b.ID, "Prod", json.RawMessage(`{}`)); err == nil {
		t.Fatal("actualizó con el nombre de otro")
	}
	// Su propio nombre sí se puede repetir (actualizar con el espacio actual).
	if _, err := s.SaveWorkspace(a.ID, "Prod", json.RawMessage(`{"nuevo":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.RenameWorkspace(a.ID, "PROD"); err != nil {
		t.Fatal(err) // solo cambian las mayúsculas
	}
	if len(s.Workspaces()) != 2 {
		t.Fatalf("hay %d, quería 2", len(s.Workspaces()))
	}
}

// La última sesión y los espacios con nombre conviven en el mismo archivo sin pisarse.
func TestWorkspaceLastAndNamedShareTheFile(t *testing.T) {
	s := newTestStore(t)
	s.SaveWorkspace("", "A", json.RawMessage(`{"k":1}`))
	s.SetWorkspaceLast(json.RawMessage(`{"last":true}`))
	s.SaveWorkspace("", "B", json.RawMessage(`{"k":2}`))
	again := reloadWorkspaces(t, s)
	if string(again.WorkspaceLast()) != `{"last":true}` || len(again.Workspaces()) != 2 {
		t.Fatalf("última %s, espacios %+v", again.WorkspaceLast(), again.Workspaces())
	}
	if _, err := os.Stat(s.workspacesPath() + ".tmp"); err == nil {
		t.Fatal("quedó el archivo temporal")
	}
}

// Un workspaces.json dañado no frena el arranque: se aparta como .bak, se empieza vacío y se puede volver a guardar.
func TestCorruptWorkspacesFileDoesNotStopStartup(t *testing.T) {
	for name, content := range map[string]string{
		"JSON cortado":    `{"version": 1, "last": {"tabs": [`,
		"basura":          "\x00\x01 no es json",
		"tipo equivocado": `{"version": 1, "named": "no es una lista"}`,
	} {
		t.Run(name, func(t *testing.T) {
			keyring.MockInit()
			cfg := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", cfg)
			t.Setenv("AppData", cfg)
			t.Setenv("HOME", cfg)
			dir, err := ConfigDir()
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "workspaces.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}

			s, err := New()
			if err != nil {
				t.Fatalf("New() falló por un workspaces.json dañado: %v", err)
			}
			if s.WorkspaceLast() != nil || len(s.Workspaces()) != 0 {
				t.Fatalf("debería empezar vacío: %s, %+v", s.WorkspaceLast(), s.Workspaces())
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("el archivo dañado tendría que haberse apartado")
			}
			if b, err := os.ReadFile(path + ".bak"); err != nil || string(b) != content {
				t.Fatalf("workspaces.json.bak = %q, %v", b, err)
			}

			// Se puede volver a guardar, y el archivo nuevo se lee bien al reiniciar.
			if err := s.SetWorkspaceLast(json.RawMessage(`{"tabs":[1]}`)); err != nil {
				t.Fatal(err)
			}
			if _, err := s.SaveWorkspace("", "Web", json.RawMessage(`{"tabs":[2]}`)); err != nil {
				t.Fatal(err)
			}
			again, err := New()
			if err != nil {
				t.Fatal(err)
			}
			if string(again.WorkspaceLast()) != `{"tabs":[1]}` || len(again.Workspaces()) != 1 {
				t.Fatalf("después de guardar: %s, %+v", again.WorkspaceLast(), again.Workspaces())
			}
			if b, _ := os.ReadFile(path + ".bak"); string(b) != content {
				t.Fatal("el .bak no debería cambiar")
			}
		})
	}
}

// El .bak anterior se pisa con el último archivo dañado.
func TestCorruptWorkspacesFileReplacesOldBak(t *testing.T) {
	s := newTestStore(t)
	os.WriteFile(s.workspacesPath()+".bak", []byte("viejo"), 0o600)
	os.WriteFile(s.workspacesPath(), []byte("{roto"), 0o600)
	s.loadWorkspaces()
	if b, _ := os.ReadFile(s.workspacesPath() + ".bak"); string(b) != "{roto" {
		t.Fatalf(".bak = %q", b)
	}
}

// Los otros dos archivos siguen exigiendo que estén bien (no se cambió su comportamiento).
func TestCorruptForwardsAndSnippetsStillFail(t *testing.T) {
	s := newTestStore(t)
	os.WriteFile(s.forwardsPath(), []byte("{roto"), 0o600)
	os.WriteFile(s.snippetsPath(), []byte("{roto"), 0o600)
	if s.loadForwards() == nil || s.loadSnippets() == nil {
		t.Fatal("forwards.json y snippets.json dañados deberían seguir dando error")
	}
}

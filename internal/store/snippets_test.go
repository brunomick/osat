package store

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

// reloadSnippets lee de nuevo snippets.json, como al reiniciar la app.
func reloadSnippets(t *testing.T, s *Store) *Store {
	t.Helper()
	n := &Store{dir: s.dir, path: s.path, conns: map[string]Connection{}}
	if err := n.loadSnippets(); err != nil {
		t.Fatal(err)
	}
	return n
}

func mustSaveSnippet(t *testing.T, s *Store, sn Snippet) Snippet {
	t.Helper()
	if sn.Mode == "" {
		sn.Mode = SnippetPaste
	}
	if sn.Command == "" {
		sn.Command = "ls"
	}
	out, err := s.SaveSnippet(sn)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// snippetGroups: grupo de cada snippet, por nombre.
func snippetGroups(s *Store) map[string]string {
	m := map[string]string{}
	for _, sn := range s.Snippets() {
		m[sn.Name] = sn.Group
	}
	return m
}

func TestSnippetValidation(t *testing.T) {
	ok := Snippet{Name: " reiniciar ", Folder: " Web / Nginx/ ", Command: "sudo systemctl restart nginx", Mode: SnippetRun, Group: `Prod\Web`}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	if ok.Name != "reiniciar" || ok.Folder != "Web/Nginx" || ok.Group != "Prod/Web" {
		t.Fatalf("no se normalizó: %+v", ok)
	}
	multi := Snippet{Name: "x", Command: "cd /srv\nls -la\n", Mode: SnippetPaste}
	if err := multi.Validate(); err != nil || multi.Command != "cd /srv\nls -la\n" {
		t.Fatalf("un comando de varias líneas debe quedar tal cual: %q, %v", multi.Command, err)
	}
	bad := []Snippet{
		{Command: "ls", Mode: SnippetPaste},
		{Name: "  ", Command: "ls", Mode: SnippetPaste},
		{Name: "x", Mode: SnippetPaste},
		{Name: "x", Command: " \n\t", Mode: SnippetRun},
		{Name: "x", Command: "ls"},
		{Name: "x", Command: "ls", Mode: "ejecutar"},
	}
	for _, sn := range bad {
		if err := sn.Validate(); err == nil {
			t.Errorf("%+v no debería valer", sn)
		}
	}
	s := newTestStore(t)
	if _, err := s.SaveSnippet(bad[0]); err == nil {
		t.Fatal("guardó un snippet inválido")
	}
	if _, err := os.Stat(s.snippetsPath()); err == nil {
		t.Fatal("un snippet inválido no debería crear el archivo")
	}
}

func TestSnippetsAreSavedAndReloaded(t *testing.T) {
	s := newTestStore(t)
	if got := s.Snippets(); got == nil || len(got) != 0 {
		t.Fatalf("sin archivo debería ser una lista vacía, fue %#v", got)
	}
	a := mustSaveSnippet(t, s, Snippet{Name: "zeta", Folder: "Web", Command: "uptime"})
	b := mustSaveSnippet(t, s, Snippet{Name: "Alfa", Folder: "Web", Command: "df -h", Mode: SnippetRun, Group: "Prod"})
	c := mustSaveSnippet(t, s, Snippet{Name: "beta"})
	if a.ID == "" || a.ID == b.ID {
		t.Fatalf("ids: %q %q", a.ID, b.ID)
	}

	// Por carpeta (sin carpeta primero) y nombre, sin distinguir mayúsculas.
	names := func(l []Snippet) []string {
		var out []string
		for _, sn := range l {
			out = append(out, sn.Name)
		}
		return out
	}
	if got, want := names(s.Snippets()), []string{"beta", "Alfa", "zeta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("orden = %v, quería %v", got, want)
	}

	again := reloadSnippets(t, s)
	if !reflect.DeepEqual(again.Snippets(), s.Snippets()) {
		t.Fatalf("recarga:\n%+v\n%+v", again.Snippets(), s.Snippets())
	}

	// Editar conserva el id.
	b.Command = "df -h /srv\ndu -sh /srv"
	if _, err := s.SaveSnippet(b); err != nil {
		t.Fatal(err)
	}
	if got := reloadSnippets(t, s).Snippets(); len(got) != 3 {
		t.Fatalf("editar no debería agregar: %+v", got)
	}
	for _, sn := range reloadSnippets(t, s).Snippets() {
		if sn.ID == b.ID && sn.Command != b.Command {
			t.Fatalf("edición: %+v", sn)
		}
	}
	if _, err := s.SaveSnippet(Snippet{ID: "no-existe", Name: "x", Command: "ls", Mode: SnippetPaste}); err == nil {
		t.Fatal("actualizó un snippet que no existe")
	}

	if err := s.DeleteSnippet(c.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSnippet("no-existe"); err != nil {
		t.Fatal(err)
	}
	if got := reloadSnippets(t, s).Snippets(); len(got) != 2 {
		t.Fatalf("lo borrado también tiene que salir del archivo: %+v", got)
	}
}

func TestSnippetsFileHasNoTempLeftovers(t *testing.T) {
	s := newTestStore(t)
	mustSaveSnippet(t, s, Snippet{Name: "x"})
	if _, err := os.Stat(s.snippetsPath() + ".tmp"); err == nil {
		t.Fatal("quedó el archivo temporal")
	}
	b, err := os.ReadFile(s.snippetsPath())
	if err != nil || !strings.Contains(string(b), `"version": 1`) {
		t.Fatalf("snippets.json = %s, %v", b, err)
	}
}

func TestImportSnippetsDoesNotDuplicate(t *testing.T) {
	s := newTestStore(t)
	mustSaveSnippet(t, s, Snippet{Name: "uptime", Folder: "Sys", Command: "uptime", Group: "Prod"})

	in := []Snippet{
		{ID: "ajeno", Name: "uptime", Folder: "Sys", Command: "uptime -p", Mode: SnippetRun, Group: "Prod"}, // igual nombre, carpeta y grupo
		{Name: "uptime", Folder: "Sys", Command: "uptime", Mode: SnippetPaste},                              // otro alcance
		{Name: "uptime", Folder: "Otra", Command: "uptime", Mode: SnippetPaste, Group: "Prod"},              // otra carpeta
		{Name: "df", Command: "df -h", Mode: SnippetRun, Group: "Grupo/Que/No/Existe"},
		{Name: "df", Command: "df -h", Mode: SnippetRun, Group: "Grupo/Que/No/Existe"}, // repetido dentro del archivo
	}
	n, err := s.ImportSnippets(in)
	if err != nil || n != 3 {
		t.Fatalf("ImportSnippets = %d, %v (quería 3)", n, err)
	}
	if n, err := s.ImportSnippets(in); err != nil || n != 0 {
		t.Fatalf("importar dos veces lo mismo agregó %d, %v", n, err)
	}
	all := reloadSnippets(t, s).Snippets()
	if len(all) != 4 {
		t.Fatalf("hay %d, quería 4: %+v", len(all), all)
	}
	seen := map[string]bool{}
	for _, sn := range all {
		if sn.ID == "" || sn.ID == "ajeno" || seen[sn.ID] {
			t.Fatalf("ids mal: %+v", all)
		}
		seen[sn.ID] = true
		if sn.Name == "uptime" && sn.Folder == "Sys" && sn.Group == "Prod" && sn.Command != "uptime" {
			t.Fatal("el importado pisó al existente")
		}
	}
	// El grupo viene tal cual, aunque no exista en esta PC, y no se crea.
	if snippetGroups(s)["df"] != "Grupo/Que/No/Existe" || len(s.Groups()) != 0 {
		t.Fatalf("grupo: %v, grupos %v", snippetGroups(s), s.Groups())
	}
}

func TestImportSnippetsSkipsInvalid(t *testing.T) {
	s := newTestStore(t)
	n, err := s.ImportSnippets([]Snippet{
		{Name: "bueno", Command: "ls", Mode: SnippetPaste},
		{Name: "malo", Command: "", Mode: SnippetPaste},
	})
	if n != 1 || err == nil || !strings.Contains(err.Error(), "malo") {
		t.Fatalf("ImportSnippets = %d, %v", n, err)
	}
	if got := reloadSnippets(t, s).Snippets(); len(got) != 1 || got[0].Name != "bueno" {
		t.Fatalf("guardados: %+v", got)
	}
}

func TestSnippetsFollowGroupRename(t *testing.T) {
	s := newTestStore(t, Connection{ID: "1", Group: "A"}, Connection{ID: "2", Group: "A/B"}, Connection{ID: "3", Group: "AB"})
	mustSaveSnippet(t, s, Snippet{Name: "global"})
	mustSaveSnippet(t, s, Snippet{Name: "a", Group: "A"})
	mustSaveSnippet(t, s, Snippet{Name: "ab", Group: "A/B"})
	mustSaveSnippet(t, s, Snippet{Name: "otro", Group: "AB"}) // mismo prefijo de texto, otro grupo

	if err := s.MoveGroup("A", "Z/A"); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"global": "", "a": "Z/A", "ab": "Z/A/B", "otro": "AB"}
	if got := snippetGroups(s); !reflect.DeepEqual(got, want) {
		t.Fatalf("grupos = %v, quería %v", got, want)
	}
	// Y quedó guardado.
	if got := snippetGroups(reloadSnippets(t, s)); !reflect.DeepEqual(got, want) {
		t.Fatalf("recarga: grupos = %v, quería %v", got, want)
	}
	// Un movimiento que no cambia nada no toca los snippets.
	if err := s.MoveGroup("Z/A", "Z/A"); err != nil {
		t.Fatal(err)
	}
}

func TestSnippetsFollowGroupPlace(t *testing.T) {
	s := newTestStore(t, Connection{ID: "1", Group: "A"}, Connection{ID: "2", Group: "A/B"}, Connection{ID: "3", Group: "C"})
	mustSaveSnippet(t, s, Snippet{Name: "a", Group: "A"})
	mustSaveSnippet(t, s, Snippet{Name: "ab", Group: "A/B"})
	mustSaveSnippet(t, s, Snippet{Name: "c", Group: "C"})

	if err := s.PlaceGroup("A", "C/A", ""); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"a": "C/A", "ab": "C/A/B", "c": "C"}
	if got := snippetGroups(reloadSnippets(t, s)); !reflect.DeepEqual(got, want) {
		t.Fatalf("grupos = %v, quería %v", got, want)
	}
}

func TestSnippetsAreDeletedWithTheirGroup(t *testing.T) {
	keyring.MockInit()
	s := newTestStore(t, Connection{ID: "1", Group: "A"}, Connection{ID: "2", Group: "A/B"}, Connection{ID: "3", Group: "AB"})
	mustSaveSnippet(t, s, Snippet{Name: "global"})
	mustSaveSnippet(t, s, Snippet{Name: "a", Group: "A"})
	mustSaveSnippet(t, s, Snippet{Name: "ab", Group: "A/B"})
	mustSaveSnippet(t, s, Snippet{Name: "otro", Group: "AB"})

	if _, err := s.DeleteWithin("A"); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"global": "", "otro": "AB"}
	if got := snippetGroups(reloadSnippets(t, s)); !reflect.DeepEqual(got, want) {
		t.Fatalf("grupos = %v, quería %v", got, want)
	}

	// Borrar todo se lleva los de cualquier grupo y deja los globales.
	if _, err := s.DeleteWithin(""); err != nil {
		t.Fatal(err)
	}
	if got := snippetGroups(reloadSnippets(t, s)); !reflect.DeepEqual(got, map[string]string{"global": ""}) {
		t.Fatalf("grupos = %v, quería solo el global", got)
	}
}

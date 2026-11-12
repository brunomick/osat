package store

import (
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/zalando/go-keyring"
)

// newTestStore arma un Store sobre una carpeta temporal, sin tocar el keyring.
func newTestStore(t *testing.T, conns ...Connection) *Store {
	t.Helper()
	dir := t.TempDir()
	s := &Store{
		dir:   dir,
		path:  filepath.Join(dir, "connections.json"),
		conns: map[string]Connection{},
	}
	for _, c := range conns {
		s.conns[c.ID] = c
	}
	return s
}

// reload lee de nuevo el archivo, como al reiniciar la app.
func reload(t *testing.T, s *Store) *Store {
	t.Helper()
	n := &Store{dir: s.dir, path: s.path, conns: map[string]Connection{}}
	if err := n.load(); err != nil {
		t.Fatal(err)
	}
	return n
}

func groupOf(t *testing.T, s *Store, id string) string {
	t.Helper()
	c, ok := s.Get(id)
	if !ok {
		t.Fatalf("falta la conexión %q", id)
	}
	return c.Group
}

func TestNormalizeGroup(t *testing.T) {
	cases := map[string]string{
		"":               "",
		"  ":             "",
		"/":              "",
		"A":              "A",
		" A / B ":        "A/B",
		"A//B/":          "A/B",
		"/A/B":           "A/B",
		`A\B`:            "A/B",
		"Producción/Web": "Producción/Web",
	}
	for in, want := range cases {
		if got := NormalizeGroup(in); got != want {
			t.Errorf("NormalizeGroup(%q) = %q, quería %q", in, got, want)
		}
	}
}

func TestAddGroupPersistsEmptyGroups(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddGroup(" Web / Frontend "); err != nil {
		t.Fatal(err)
	}
	if err := s.AddGroup("Base de datos"); err != nil {
		t.Fatal(err)
	}
	want := []string{"Web/Frontend", "Base de datos"} // en el orden en que se crearon
	if got := reload(t, s).Groups(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Groups() = %v, quería %v", got, want)
	}
	if err := s.AddGroup("  /  "); err == nil {
		t.Fatal("un nombre vacío debería fallar")
	}
}

func TestMoveConnection(t *testing.T) {
	s := newTestStore(t, Connection{ID: "a", Name: "a", Group: "X"})
	if err := s.MoveConnection("a", " Y / Z "); err != nil {
		t.Fatal(err)
	}
	if got := groupOf(t, reload(t, s), "a"); got != "Y/Z" {
		t.Fatalf("grupo = %q, quería Y/Z", got)
	}
	if err := s.MoveConnection("a", ""); err != nil {
		t.Fatal(err)
	}
	if got := groupOf(t, s, "a"); got != "" {
		t.Fatalf("grupo = %q, quería sin grupo", got)
	}
	if err := s.MoveConnection("nope", "X"); err == nil {
		t.Fatal("mover una conexión inexistente debería fallar")
	}
}

func TestMoveGroupRewritesTree(t *testing.T) {
	s := newTestStore(t,
		Connection{ID: "1", Group: "A"},
		Connection{ID: "2", Group: "A/B"},
		Connection{ID: "3", Group: "A/B/C"},
		Connection{ID: "4", Group: "AB"}, // mismo prefijo de texto, otro grupo
		Connection{ID: "5", Group: "Z"},
	)
	if err := s.AddGroup("A/Vacio"); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveGroup("A", "Z/A"); err != nil {
		t.Fatal(err)
	}
	s = reload(t, s)
	for id, want := range map[string]string{"1": "Z/A", "2": "Z/A/B", "3": "Z/A/B/C", "4": "AB", "5": "Z"} {
		if got := groupOf(t, s, id); got != want {
			t.Errorf("conexión %s: grupo %q, quería %q", id, got, want)
		}
	}
	// Las rutas de los servidores y la del grupo vacío, ya movidas ("A" no queda).
	got := s.Groups()
	sort.Strings(got)
	if want := []string{"AB", "Z", "Z/A", "Z/A/B", "Z/A/B/C", "Z/A/Vacio"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Groups() = %v, quería %v", got, want)
	}
}

func TestMoveGroupRejectsCycles(t *testing.T) {
	s := newTestStore(t, Connection{ID: "1", Group: "A/B"})
	if err := s.MoveGroup("A", "A/B/A"); err == nil {
		t.Fatal("mover un grupo dentro de sí mismo debería fallar")
	}
	if err := s.MoveGroup("A", "A"); err != nil {
		t.Fatalf("mover un grupo a donde ya está no debería fallar: %v", err)
	}
	if err := s.MoveGroup("", "X"); err == nil {
		t.Fatal("mover \"sin grupo\" debería fallar")
	}
	if err := s.MoveGroup("A", ""); err == nil {
		t.Fatal("mover un grupo a un nombre vacío debería fallar")
	}
	if got := groupOf(t, s, "1"); got != "A/B" {
		t.Fatalf("grupo = %q: un movimiento rechazado no debe cambiar nada", got)
	}
}

func TestMoveGroupMergesIntoExisting(t *testing.T) {
	s := newTestStore(t, Connection{ID: "1", Group: "A/X"}, Connection{ID: "2", Group: "B/X"})
	if err := s.MoveGroup("A/X", "B/X"); err != nil {
		t.Fatal(err)
	}
	if groupOf(t, s, "1") != "B/X" || groupOf(t, s, "2") != "B/X" {
		t.Fatal("los dos servidores deberían quedar en B/X")
	}
}

func TestGroupSurvivesLosingItsLastServer(t *testing.T) {
	s := newTestStore(t, Connection{ID: "1", Group: "A/B"})
	if err := s.saveLocked(); err != nil { // como un archivo de una versión sin grupos guardados
		t.Fatal(err)
	}
	s = reload(t, s)
	if err := s.MoveConnection("1", "Otro"); err != nil {
		t.Fatal(err)
	}
	want := []string{"A/B", "Otro"}
	if got := reload(t, s).Groups(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Groups() = %v, quería %v: el grupo vacío debe seguir ahí", got, want)
	}
	// Y se elimina a propósito, no por quedarse vacío.
	if _, err := s.DeleteWithin("A/B"); err != nil {
		t.Fatal(err)
	}
	if got := reload(t, s).Groups(); !reflect.DeepEqual(got, []string{"Otro"}) {
		t.Fatalf("Groups() = %v, quería [Otro]", got)
	}
}

func TestPlaceGroupOrder(t *testing.T) {
	s := newTestStore(t)
	for _, g := range []string{"A", "B", "C", "A/x"} {
		if err := s.AddGroup(g); err != nil {
			t.Fatal(err)
		}
	}
	steps := []struct {
		from, to, before string
		want             []string
	}{
		{"C", "C", "A", []string{"C", "A", "B", "A/x"}},       // antes de un hermano
		{"A", "A", "", []string{"C", "B", "A", "A/x"}},        // al final, con su subgrupo
		{"B", "A/B", "A/x", []string{"C", "A", "A/B", "A/x"}}, // a otro grupo, en un lugar
	}
	for _, st := range steps {
		if err := s.PlaceGroup(st.from, st.to, st.before); err != nil {
			t.Fatal(err)
		}
		if got := reload(t, s).Groups(); !reflect.DeepEqual(got, st.want) {
			t.Fatalf("después de %s→%s antes de %q: Groups() = %v, quería %v", st.from, st.to, st.before, got, st.want)
		}
	}
	// Renombrar no lo cambia de lugar.
	if err := s.MoveGroup("C", "D"); err != nil {
		t.Fatal(err)
	}
	if got, want := s.Groups(), []string{"D", "A", "A/B", "A/x"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Groups() = %v, quería %v", got, want)
	}
	if err := s.PlaceGroup("A/B", "A/B", "D"); err == nil {
		t.Fatal("ubicar un grupo antes de uno de otro nivel debería fallar")
	}
}

// idsIn: las conexiones de un grupo en el orden en que se muestran.
func idsIn(s *Store, group string) []string {
	var out []string
	for _, c := range s.List() {
		if c.Group == group {
			out = append(out, c.ID)
		}
	}
	return out
}

func TestPlaceConnectionOrder(t *testing.T) {
	s := newTestStore(t,
		Connection{ID: "a", Name: "a", Group: "G"},
		Connection{ID: "b", Name: "b", Group: "G"},
		Connection{ID: "c", Name: "c", Group: "G"},
		Connection{ID: "x", Name: "x", Group: "H"},
	)
	if got, want := idsIn(s, "G"), []string{"a", "b", "c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sin orden propio, por nombre: %v, quería %v", got, want)
	}
	if err := s.PlaceConnection("c", "G", "a"); err != nil {
		t.Fatal(err)
	}
	if got, want := idsIn(reload(t, s), "G"), []string{"c", "a", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("G = %v, quería %v", got, want)
	}
	if err := s.PlaceConnection("c", "G", ""); err != nil {
		t.Fatal(err)
	}
	if got, want := idsIn(s, "G"), []string{"a", "b", "c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("al final: G = %v, quería %v", got, want)
	}
	// A otro grupo, en un lugar; y una que llega con MoveConnection va al final.
	if err := s.PlaceConnection("b", "H", "x"); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveConnection("a", "H"); err != nil {
		t.Fatal(err)
	}
	if got, want := idsIn(reload(t, s), "H"), []string{"b", "x", "a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("H = %v, quería %v", got, want)
	}
}

func TestDeleteWithin(t *testing.T) {
	keyring.MockInit()
	s := newTestStore(t,
		Connection{ID: "a", Name: "a", Host: "h", User: "u", Port: 22, Group: "A"},
		Connection{ID: "b", Name: "b", Host: "h", User: "u", Port: 22, Group: "A/B"},
		Connection{ID: "ab", Name: "ab", Host: "h", User: "u", Port: 22, Group: "AB"}, // mismo prefijo de texto, otro grupo
		Connection{ID: "z", Name: "z", Host: "h", User: "u", Port: 22},
	)
	for _, g := range []string{"A", "A/B", "A/Vacio", "AB"} {
		if err := s.AddGroup(g); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"a", "b", "ab"} {
		if err := s.SetSecret(id, "pw-"+id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.SaveForward(Forward{ConnID: "b", LocalPort: 5432, Port: 5432}); err != nil {
		t.Fatal(err)
	}

	n, err := s.DeleteWithin("A")
	if err != nil || n != 2 {
		t.Fatalf("DeleteWithin(A) = %d, %v; quería 2 conexiones", n, err)
	}
	s = reload(t, s)
	if err := s.loadForwards(); err != nil {
		t.Fatal(err)
	}
	if got, want := idsIn(s, "AB"), []string{"ab"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("AB = %v, quería %v", got, want)
	}
	if _, ok := s.Get("b"); ok {
		t.Fatal("b debería haberse eliminado")
	}
	if got, want := s.Groups(), []string{"AB"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Groups() = %v, quería %v", got, want)
	}
	if s.Secret("a") != "" || s.Secret("ab") != "pw-ab" {
		t.Fatal("se debían borrar las contraseñas del grupo, y solo esas")
	}
	if len(s.Forwards("b")) != 0 {
		t.Fatal("se debían borrar los reenvíos del grupo")
	}

	// "" = todo.
	if n, err := s.DeleteWithin(""); err != nil || n != 2 {
		t.Fatalf("DeleteWithin(\"\") = %d, %v; quería 2 conexiones", n, err)
	}
	s = reload(t, s)
	if len(s.List()) != 0 || len(s.Groups()) != 0 {
		t.Fatalf("quedó algo: %v %v", s.List(), s.Groups())
	}
}

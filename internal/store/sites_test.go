package store

import (
	"reflect"
	"testing"
)

// reloadSites lee de nuevo sites.json, como al reiniciar la app.
func reloadSites(t *testing.T, s *Store) *Store {
	t.Helper()
	n := &Store{dir: s.dir, path: s.path, conns: map[string]Connection{}}
	if err := n.loadSites(); err != nil {
		t.Fatal(err)
	}
	return n
}

func mustSaveSite(t *testing.T, s *Store, name, url string) Site {
	t.Helper()
	out, err := s.SaveSite(Site{Name: name, URL: url})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func siteNames(s *Store) []string {
	var out []string
	for _, st := range s.Sites() {
		out = append(out, st.Name)
	}
	return out
}

func TestSitesSaveListOrder(t *testing.T) {
	s := newTestStore(t)
	a := mustSaveSite(t, s, "A", "https://a.example.com")
	mustSaveSite(t, s, "B", "http://b.example.com/x?y=1")
	mustSaveSite(t, s, "C", "https://c.example.com")
	if got := siteNames(s); !reflect.DeepEqual(got, []string{"A", "B", "C"}) {
		t.Fatalf("orden = %q", got)
	}
	// Actualizar cambia nombre y URL, no el lugar.
	a.Name, a.URL = "  A2 ", " https://a2.example.com "
	up, err := s.SaveSite(a)
	if err != nil || up.Name != "A2" || up.URL != "https://a2.example.com" || up.Order != a.Order {
		t.Fatalf("SaveSite = %+v, %v", up, err)
	}
	if got := siteNames(s); !reflect.DeepEqual(got, []string{"A2", "B", "C"}) {
		t.Fatalf("orden tras editar = %q", got)
	}
	if _, err := s.SaveSite(Site{ID: "nope", Name: "x", URL: "https://x.com"}); err == nil {
		t.Fatal("actualizó un sitio inexistente")
	}
	if err := s.DeleteSite(a.ID); err != nil {
		t.Fatal(err)
	}
	if got := siteNames(s); !reflect.DeepEqual(got, []string{"B", "C"}) {
		t.Fatalf("orden tras borrar = %q", got)
	}
	// Uno nuevo va al final aunque se haya borrado el primero.
	mustSaveSite(t, s, "D", "https://d.example.com")
	if got := siteNames(s); !reflect.DeepEqual(got, []string{"B", "C", "D"}) {
		t.Fatalf("orden con el nuevo = %q", got)
	}
}

func TestSiteRejectsBadURL(t *testing.T) {
	s := newTestStore(t)
	for _, u := range []string{"ftp://x", "javascript:alert(1)", "", "https://", "http:///path", "example.com", "https://%zz"} {
		if _, err := s.SaveSite(Site{Name: "x", URL: u}); err == nil {
			t.Errorf("aceptó la URL %q", u)
		}
	}
	if _, err := s.SaveSite(Site{Name: "  ", URL: "https://x.com"}); err == nil {
		t.Error("aceptó un nombre vacío")
	}
	if len(s.Sites()) != 0 {
		t.Fatalf("quedó guardado algo: %+v", s.Sites())
	}
}

func TestPlaceSite(t *testing.T) {
	s := newTestStore(t)
	a := mustSaveSite(t, s, "A", "https://a.com")
	b := mustSaveSite(t, s, "B", "https://b.com")
	c := mustSaveSite(t, s, "C", "https://c.com")
	if err := s.PlaceSite(c.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if got := siteNames(s); !reflect.DeepEqual(got, []string{"C", "A", "B"}) {
		t.Fatalf("C antes de A = %q", got)
	}
	if err := s.PlaceSite(c.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got := siteNames(s); !reflect.DeepEqual(got, []string{"A", "B", "C"}) {
		t.Fatalf("C al final = %q", got)
	}
	if err := s.PlaceSite(a.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	if got := siteNames(s); !reflect.DeepEqual(got, []string{"B", "A", "C"}) {
		t.Fatalf("A antes de C = %q", got)
	}
	for i, st := range s.Sites() {
		if st.Order != i+1 {
			t.Fatalf("%s tiene Order %d, quería %d", st.Name, st.Order, i+1)
		}
	}
	if err := s.PlaceSite("nope", ""); err == nil {
		t.Fatal("movió un sitio inexistente")
	}
	if err := s.PlaceSite(b.ID, "nope"); err == nil {
		t.Fatal("aceptó un destino inexistente")
	}
}

func TestSitesPersist(t *testing.T) {
	s := newTestStore(t)
	a := mustSaveSite(t, s, "A", "https://a.com")
	mustSaveSite(t, s, "B", "https://b.com")
	c := mustSaveSite(t, s, "C", "https://c.com")
	if err := s.PlaceSite(c.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSite(a.ID); err != nil {
		t.Fatal(err)
	}
	r := reloadSites(t, s)
	if !reflect.DeepEqual(r.Sites(), s.Sites()) || len(r.Sites()) != 2 {
		t.Fatalf("recargado = %+v, en memoria = %+v", r.Sites(), s.Sites())
	}
	if got := siteNames(r); !reflect.DeepEqual(got, []string{"C", "B"}) {
		t.Fatalf("orden recargado = %q", got)
	}
}

func TestImportSitesSkipsSameURL(t *testing.T) {
	s := newTestStore(t)
	mustSaveSite(t, s, "Viejo", "https://a.com")
	n, err := s.ImportSites([]Site{{Name: "Otro nombre", URL: "https://a.com"}, {Name: "B", URL: "https://b.com"}, {Name: "B2", URL: "https://b.com"}, {Name: "mal", URL: "ftp://x"}})
	if n != 1 || err == nil {
		t.Fatalf("ImportSites = %d, %v (quería 1 y el error del inválido)", n, err)
	}
	if got := siteNames(s); !reflect.DeepEqual(got, []string{"Viejo", "B"}) {
		t.Fatalf("sitios = %q", got)
	}
}

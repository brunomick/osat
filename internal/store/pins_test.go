package store

import (
	"reflect"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestPinsAddRenameRemove(t *testing.T) {
	s := newTestStore(t, Connection{ID: "a", Name: "a"})
	for _, p := range []string{"/var/log", "/var/log/", "/srv"} {
		if err := s.PinDir("a", p); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RenamePin("a", "/srv", "web"); err != nil {
		t.Fatal(err)
	}
	want := []PinnedDir{{Path: "/var/log"}, {Path: "/srv", Name: "web"}}
	if got := reload(t, s).conns["a"].Pins; !reflect.DeepEqual(got, want) {
		t.Fatalf("pins = %+v, quería %+v", got, want)
	}
	if err := s.RenamePin("a", "/nada", "x"); err == nil {
		t.Fatal("renombró una carpeta no fijada")
	}
	if err := s.UnpinDir("a", "/var/log"); err != nil {
		t.Fatal(err)
	}
	if got := s.conns["a"].Pins; !reflect.DeepEqual(got, want[1:]) {
		t.Fatalf("pins = %+v", got)
	}
	if err := s.PinDir("zz", "/x"); err == nil {
		t.Fatal("fijó en una conexión inexistente")
	}
}

func TestFavoritesOrder(t *testing.T) {
	s := newTestStore(t,
		Connection{ID: "a", Name: "a"}, Connection{ID: "b", Name: "b"},
		Connection{ID: "c", Name: "c"}, Connection{ID: "d", Name: "d"},
	)
	favs := func(s *Store) []string {
		var ids []string
		for _, c := range s.conns {
			if c.Favorite {
				ids = append(ids, c.ID)
			}
		}
		// por FavOrder
		for i := range ids {
			for j := i + 1; j < len(ids); j++ {
				if s.conns[ids[j]].FavOrder < s.conns[ids[i]].FavOrder {
					ids[i], ids[j] = ids[j], ids[i]
				}
			}
		}
		return ids
	}
	for _, id := range []string{"c", "a", "b"} {
		if err := s.SetFavorite(id, true); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := favs(s), []string{"c", "a", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("favs = %v, quería %v", got, want)
	}
	if err := s.PlaceFavorite("b", "c"); err != nil {
		t.Fatal(err)
	}
	if got, want := favs(reload(t, s)), []string{"b", "c", "a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("favs = %v, quería %v", got, want)
	}
	if err := s.PlaceFavorite("b", ""); err != nil {
		t.Fatal(err)
	}
	if got, want := favs(s), []string{"c", "a", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("al final: %v, quería %v", got, want)
	}
	if err := s.PlaceFavorite("d", ""); err == nil {
		t.Fatal("ubicó una que no es favorita")
	}
	if err := s.SetFavorite("a", false); err != nil {
		t.Fatal(err)
	}
	if c := s.conns["a"]; c.Favorite || c.FavOrder != 0 {
		t.Fatalf("a = %+v", c)
	}
}

func TestSaveKeepsPinsAndFavorites(t *testing.T) {
	keyring.MockInit()
	s := newTestStore(t)
	c, err := s.Save(ConnectionInput{Connection: Connection{Name: "a", Host: "h", User: "u", AuthType: AuthAgent,
		Pins: []PinnedDir{{Path: "/srv"}}, Favorite: true, FavOrder: 9}})
	if err != nil {
		t.Fatal(err)
	}
	if c.Favorite || c.FavOrder != 0 || len(c.Pins) != 1 {
		t.Fatalf("nueva: %+v", c)
	}
	if err := s.SetFavorite(c.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.PinDir(c.ID, "/var"); err != nil {
		t.Fatal(err)
	}
	// El formulario no manda nada de esto; y si mandara, no cuenta.
	in := c
	in.Name, in.Pins, in.Favorite, in.FavOrder = "b", nil, false, 0
	got, err := s.Save(ConnectionInput{Connection: in})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Favorite || got.FavOrder != 1 || len(got.Pins) != 2 {
		t.Fatalf("editada: %+v", got)
	}
}

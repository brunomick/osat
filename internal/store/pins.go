package store

import (
	"path"
	"sort"

	"osat/internal/i18n"
)

// updateConn aplica fn a la conexión connID y guarda.
func (s *Store) updateConn(connID string, fn func(c *Connection) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.conns[connID]
	if !ok {
		return i18n.Err("app.connectionDoesNotExist")
	}
	if err := fn(&c); err != nil {
		return err
	}
	s.conns[connID] = c
	return s.saveLocked()
}

func cleanPinPath(p string) (string, error) {
	if p == "" {
		return "", i18n.Err("data.theFolderHasNoPath")
	}
	return path.Clean(p), nil
}

// PinDir fija una carpeta del servidor (si ya estaba fijada, no hace nada).
func (s *Store) PinDir(connID, p string) error {
	p, err := cleanPinPath(p)
	if err != nil {
		return err
	}
	return s.updateConn(connID, func(c *Connection) error {
		for _, x := range c.Pins {
			if x.Path == p {
				return nil
			}
		}
		c.Pins = append(append([]PinnedDir(nil), c.Pins...), PinnedDir{Path: p})
		return nil
	})
}

// UnpinDir suelta una carpeta fijada.
func (s *Store) UnpinDir(connID, p string) error {
	p, err := cleanPinPath(p)
	if err != nil {
		return err
	}
	return s.updateConn(connID, func(c *Connection) error {
		var out []PinnedDir
		for _, x := range c.Pins {
			if x.Path != p {
				out = append(out, x)
			}
		}
		c.Pins = out
		return nil
	})
}

// RenamePin le pone nombre a una carpeta fijada ("" = se muestra la ruta).
func (s *Store) RenamePin(connID, p, name string) error {
	p, err := cleanPinPath(p)
	if err != nil {
		return err
	}
	return s.updateConn(connID, func(c *Connection) error {
		pins := append([]PinnedDir(nil), c.Pins...)
		for i := range pins {
			if pins[i].Path == p {
				pins[i].Name = name
				c.Pins = pins
				return nil
			}
		}
		return i18n.Err("data.theFolderIsNotPinned")
	})
}

// SetFavorite agrega o quita una conexión de Favoritos. Al agregar va al final.
func (s *Store) SetFavorite(connID string, fav bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.conns[connID]
	if !ok {
		return i18n.Err("app.connectionDoesNotExist")
	}
	if c.Favorite == fav {
		return nil
	}
	c.Favorite = fav
	c.FavOrder = 0
	if fav {
		c.FavOrder = s.nextFavOrderLocked()
	}
	s.conns[connID] = c
	return s.saveLocked()
}

// nextFavOrderLocked: el FavOrder que deja una favorita al final de Favoritos.
func (s *Store) nextFavOrderLocked() int {
	n := 0
	for _, c := range s.conns {
		if c.Favorite && c.FavOrder > n {
			n = c.FavOrder
		}
	}
	return n + 1
}

// PlaceFavorite deja la favorita connID antes de beforeID ("" o una que no sea
// favorita = al final) y renumera Favoritos 1..n.
func (s *Store) PlaceFavorite(connID, beforeID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.conns[connID]
	if !ok || !c.Favorite {
		return i18n.Err("data.theConnectionIsNotIn")
	}
	var favs []Connection
	for _, x := range s.conns {
		if x.Favorite && x.ID != connID {
			favs = append(favs, x)
		}
	}
	sort.Slice(favs, func(i, j int) bool {
		if favs[i].FavOrder != favs[j].FavOrder {
			return favs[i].FavOrder < favs[j].FavOrder
		}
		return favs[i].Name < favs[j].Name
	})
	i := len(favs)
	for j, x := range favs {
		if x.ID == beforeID {
			i = j
			break
		}
	}
	favs = append(favs[:i], append([]Connection{c}, favs[i:]...)...)
	for j, x := range favs {
		x.FavOrder = j + 1
		s.conns[x.ID] = x
	}
	return s.saveLocked()
}

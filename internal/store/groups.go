package store

import (
	"strings"
	"time"

	"osat/internal/i18n"
)

// Los grupos se guardan en una lista en el orden que eligió el usuario
// (arrastrándolos): cada nivel del árbol muestra sus subgrupos en el orden en
// que aparecen por primera vez en la lista. Los nuevos van al final.

// Los grupos son carpetas anidadas: el grupo de una conexión es una ruta
// "Producción/Web". Una conexión sin grupo tiene Group == "".

// NormalizeGroup deja una ruta de grupo en su forma canónica: sin espacios
// sobrantes, sin segmentos vacíos y con "/" como separador.
func NormalizeGroup(g string) string {
	parts := strings.Split(strings.ReplaceAll(g, `\`, "/"), "/")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "/")
}

// moveGroupPath devuelve dónde queda el grupo g si el grupo from pasa a ser to
// (arrastrando también todo lo que cuelga de él). ok es false si g no cambia.
func moveGroupPath(g, from, to string) (string, bool) {
	switch {
	case g == from:
		return to, true
	case strings.HasPrefix(g, from+"/"):
		return to + g[len(from):], true
	}
	return g, false
}

// keepGroup registra el grupo de una conexión como propio: así sigue existiendo
// aunque después se quede sin servidores, como una carpeta vacía.
func (s *Store) keepGroup(g string) {
	if g != "" && s.groupIndex(g) < 0 {
		s.groups = append(s.groups, g)
	}
}

func (s *Store) groupIndex(g string) int {
	for i, x := range s.groups {
		if x == g {
			return i
		}
	}
	return -1
}

func (s *Store) groupsLocked() []string {
	return append([]string(nil), s.groups...)
}

// isWithin: g es ancestor o está adentro de él.
func isWithin(g, ancestor string) bool { return g == ancestor || strings.HasPrefix(g, ancestor+"/") }

// parentGroup: "A/B" → "A"; "A" → "".
func parentGroup(g string) string {
	if i := strings.LastIndex(g, "/"); i >= 0 {
		return g[:i]
	}
	return ""
}

// Groups devuelve los grupos creados a mano. Para el árbol completo hay que
// sumarles los de cada conexión.
func (s *Store) Groups() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.groupsLocked()
}

// AddGroup crea un grupo (vacío) en la ruta indicada.
func (s *Store) AddGroup(path string) error {
	g := NormalizeGroup(path)
	if g == "" {
		return i18n.Err("data.typeANameForThe")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keepGroup(g)
	return s.saveLocked()
}

// MoveConnection pasa una conexión a otro grupo ("" = sin grupo).
func (s *Store) MoveConnection(id, group string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.conns[id]
	if !ok {
		return i18n.Err("app.connectionDoesNotExist")
	}
	if g := NormalizeGroup(group); g != c.Group {
		c.Group = g
		c.Order = s.nextOrderLocked(g) // al final de su nuevo grupo
	}
	c.UpdatedAt = time.Now().Unix()
	s.conns[id] = c
	s.keepGroup(c.Group)
	return s.saveLocked()
}

// nextOrderLocked: el Order que deja una conexión al final del grupo g.
func (s *Store) nextOrderLocked(g string) int {
	n := 0
	for _, c := range s.conns {
		if c.Group == g && c.Order > n {
			n = c.Order
		}
	}
	return n + 1
}

// PlaceConnection pasa una conexión al grupo group y la ubica antes de la
// conexión before ("" o una que no esté en ese grupo = al final).
func (s *Store) PlaceConnection(id, group, before string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.conns[id]
	if !ok {
		return i18n.Err("app.connectionDoesNotExist")
	}
	c.Group = NormalizeGroup(group)
	c.UpdatedAt = time.Now().Unix()
	s.conns[id] = c
	s.keepGroup(c.Group)

	// Los demás del grupo en su orden actual, con esta intercalada; después
	// se renumeran 1..n.
	var sibs []Connection
	for _, x := range s.listLocked() {
		if x.Group == c.Group && x.ID != id {
			sibs = append(sibs, x)
		}
	}
	i := len(sibs)
	for j, x := range sibs {
		if x.ID == before {
			i = j
			break
		}
	}
	sibs = append(sibs[:i], append([]Connection{c}, sibs[i:]...)...)
	for j, x := range sibs {
		x.Order = j + 1
		s.conns[x.ID] = x
	}
	return s.saveLocked()
}

// MoveGroup renombra o mueve el grupo from a la ruta to, junto con sus
// subgrupos y servidores. Si to ya existe, se unen.
func (s *Store) MoveGroup(from, to string) error {
	from, to = NormalizeGroup(from), NormalizeGroup(to)
	if from == "" || to == "" {
		return i18n.Err("data.theGroupHasNoName")
	}
	if from == to {
		return nil
	}
	if strings.HasPrefix(to, from+"/") {
		return i18n.Err("data.canTMoveAGroup")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveWithSnippetsLocked(s.moveGroupLocked(from, to))
}

// moveGroupLocked renombra from a to en los servidores, los snippets y en la
// lista de grupos, sin cambiar de lugar en la lista (si to ya existía, queda
// el primero). Devuelve si cambió algún snippet.
func (s *Store) moveGroupLocked(from, to string) bool {
	now := time.Now().Unix()
	for id, c := range s.conns {
		if g, ok := moveGroupPath(c.Group, from, to); ok {
			c.Group, c.UpdatedAt = g, now
			s.conns[id] = c
		}
	}
	moved := make([]string, 0, len(s.groups))
	seen := make(map[string]bool, len(s.groups))
	for _, g := range s.groups {
		if g, _ = moveGroupPath(g, from, to); !seen[g] {
			seen[g] = true
			moved = append(moved, g)
		}
	}
	s.groups = moved
	return s.moveSnippetsLocked(from, to)
}

// PlaceGroup mueve el grupo from a la ruta to (como MoveGroup) y lo ubica, con
// todo lo que tiene adentro, antes de su grupo hermano before ("" = al final
// de su nivel).
func (s *Store) PlaceGroup(from, to, before string) error {
	from, to, before = NormalizeGroup(from), NormalizeGroup(to), NormalizeGroup(before)
	if from == "" || to == "" {
		return i18n.Err("data.theGroupHasNoName")
	}
	if strings.HasPrefix(to, from+"/") {
		return i18n.Err("data.canTMoveAGroup")
	}
	if before != "" && (parentGroup(before) != parentGroup(to) || before == to) {
		return i18n.Err("data.invalidPositionForTheGroup")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snippetsMoved := s.moveGroupLocked(from, to)

	var sub, rest []string
	for _, g := range s.groups {
		if isWithin(g, to) {
			sub = append(sub, g)
		} else {
			rest = append(rest, g)
		}
	}
	if len(sub) == 0 {
		sub = []string{to}
	}
	// Antes del primero de before; si no, después del último de su nivel.
	i := -1
	if before != "" {
		for j, g := range rest {
			if isWithin(g, before) {
				i = j
				break
			}
		}
	}
	if i < 0 {
		i = len(rest)
		if p := parentGroup(to); p != "" {
			for j, g := range rest {
				if isWithin(g, p) {
					i = j + 1
				}
			}
		}
	}
	s.groups = append(append(append(make([]string, 0, len(s.groups)), rest[:i]...), sub...), rest[i:]...)
	return s.saveWithSnippetsLocked(snippetsMoved)
}

// DeleteWithin elimina el grupo group con todo lo que tiene adentro:
// subgrupos, servidores (con sus secretos y reenvíos) y snippets. group "" = todo
// (menos los snippets globales).
// Devuelve cuántas conexiones eliminó.
func (s *Store) DeleteWithin(group string) (int, error) {
	g := NormalizeGroup(group)
	s.mu.Lock()
	defer s.mu.Unlock()
	inside := func(c Connection) bool { return g == "" || isWithin(c.Group, g) }
	for _, c := range s.conns { // un bastión que usa una conexión de afuera no se borra
		if j, ok := s.conns[c.JumpID]; ok && !inside(c) && inside(j) {
			return 0, i18n.Err("data.sJumpsThroughSWhich", c.Name, j.Name)
		}
	}
	n := 0
	var ferr error
	for id, c := range s.conns {
		if g != "" && !isWithin(c.Group, g) {
			continue
		}
		delete(s.conns, id)
		deleteSecret(keyringService, id)
		deleteSecret(legacyKeyringService, id)
		if err := s.dropForwardsLocked(id); err != nil && ferr == nil {
			ferr = err
		}
		n++
	}
	var kept []string
	for _, x := range s.groups {
		if g != "" && !isWithin(x, g) {
			kept = append(kept, x)
		}
	}
	s.groups = kept
	if err := s.saveWithSnippetsLocked(s.dropSnippetsWithinLocked(g)); err != nil {
		return n, err
	}
	return n, ferr
}

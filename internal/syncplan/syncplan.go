// Package syncplan decide qué hay que copiar, crear o borrar para sincronizar
// una carpeta de esta PC con una del servidor. Es lógica pura: recibe los dos
// listados (rutas relativas, con "/" como separador) y devuelve las acciones,
// sin tocar archivos ni conexiones.
//
// Reglas:
//   - se excluye lo que coincide (path.Match) con un patrón por el nombre, por
//     la ruta relativa o por cualquier carpeta de la ruta;
//   - time+size: distinto si el tamaño difiere o la fecha difiere más de 2 s;
//     sync copia solo si el origen es más nuevo, mirror copia si es distinto;
//   - size: distinto solo si el tamaño difiere, y se copia sin mirar fechas;
//   - content: distinto si el tamaño o el hash difieren (quien llama calcula
//     los hashes solo para los pares de igual tamaño);
//   - both: gana el más nuevo y nunca se borra; si la fecha no alcanza para
//     decidir, se omite con su motivo;
//   - los enlaces simbólicos, y lo que es carpeta de un lado y archivo del
//     otro, se omiten con su motivo.
package syncplan

import (
	"path"
	"sort"
	"strings"
)

// Operaciones de una Action.
const (
	OpUpload       = "upload"
	OpDownload     = "download"
	OpDeleteLocal  = "deleteLocal"
	OpDeleteRemote = "deleteRemote"
	OpMkdirLocal   = "mkdirLocal"
	OpMkdirRemote  = "mkdirRemote"
	OpSkip         = "skip"
)

// Valores de Options.
const (
	DirUp   = "up"   // PC → servidor
	DirDown = "down" // servidor → PC
	DirBoth = "both"

	ModeSync   = "sync"
	ModeMirror = "mirror"

	CompareTimeSize = "time+size"
	CompareSize     = "size"
	CompareContent  = "content"
)

// Tolerancia al comparar fechas (algunos sistemas de archivos redondean).
const timeSlack = 2

type Entry struct {
	Rel     string
	IsDir   bool
	IsLink  bool
	Size    int64
	ModTime int64 // segundos Unix
	Hash    string
}

type Action struct {
	Rel    string `json:"rel"`
	Op     string `json:"op"`
	Reason string `json:"reason"`
	Size   int64  `json:"size"`
}

type Options struct {
	Direction string
	Mode      string
	Compare   string
	Delete    bool
	Exclude   []string
}

// Plan devuelve las acciones ordenadas por ruta, con los borrados al final y
// en orden inverso (lo de adentro antes que su carpeta).
func Plan(local, remote []Entry, opts Options) []Action {
	loc := index(local, opts.Exclude)
	rem := index(remote, opts.Exclude)
	rels := make([]string, 0, len(loc)+len(rem))
	for r := range loc {
		rels = append(rels, r)
	}
	for r := range rem {
		if _, ok := loc[r]; !ok {
			rels = append(rels, r)
		}
	}
	sort.Strings(rels)

	var acts, dels []Action
	for _, r := range rels {
		l, hasL := loc[r]
		s, hasS := rem[r]
		a, ok := decide(r, l, s, hasL, hasS, opts)
		if !ok {
			continue
		}
		if a.Op == OpDeleteLocal || a.Op == OpDeleteRemote {
			dels = append(dels, a)
		} else {
			acts = append(acts, a)
		}
	}
	for i := len(dels) - 1; i >= 0; i-- {
		acts = append(acts, dels[i])
	}
	return acts
}

// side describe una dirección: de dónde a dónde y cómo se nombra cada lado.
type side struct {
	copyOp, mkdirOp, deleteOp string
	src, dst                  string // "la PC" / "el servidor"
}

var (
	up   = side{OpUpload, OpMkdirRemote, OpDeleteRemote, "la PC", "el servidor"}
	down = side{OpDownload, OpMkdirLocal, OpDeleteLocal, "el servidor", "la PC"}
)

func decide(rel string, l, s Entry, hasL, hasS bool, o Options) (Action, bool) {
	if (hasL && l.IsLink) || (hasS && s.IsLink) {
		return Action{Rel: rel, Op: OpSkip, Reason: "enlace simbólico (no se sigue)"}, true
	}
	if hasL && hasS && l.IsDir != s.IsDir {
		return Action{Rel: rel, Op: OpSkip, Reason: "en un lado es carpeta y en el otro archivo"}, true
	}
	switch o.Direction {
	case DirUp:
		return oneWay(rel, l, s, hasL, hasS, up, o)
	case DirDown:
		return oneWay(rel, s, l, hasS, hasL, down, o)
	case DirBoth:
		return bothWays(rel, l, s, hasL, hasS, o)
	}
	return Action{}, false
}

// oneWay decide para una sola dirección, con src el origen y dst el destino.
func oneWay(rel string, src, dst Entry, hasSrc, hasDst bool, sd side, o Options) (Action, bool) {
	switch {
	case hasSrc && !hasDst:
		if src.IsDir {
			return Action{Rel: rel, Op: sd.mkdirOp, Reason: "no existe en " + sd.dst}, true
		}
		return Action{Rel: rel, Op: sd.copyOp, Reason: "no existe en " + sd.dst, Size: src.Size}, true
	case !hasSrc && hasDst:
		if !o.Delete {
			return Action{}, false
		}
		return Action{Rel: rel, Op: sd.deleteOp, Reason: "no está en " + sd.src, Size: dst.Size}, true
	case src.IsDir:
		return Action{}, false
	}

	newer := src.ModTime-dst.ModTime > timeSlack
	older := dst.ModTime-src.ModTime > timeSlack
	copyAct := func(reason string) (Action, bool) {
		return Action{Rel: rel, Op: sd.copyOp, Reason: reason, Size: src.Size}, true
	}
	switch o.Compare {
	case CompareSize:
		if src.Size == dst.Size {
			return Action{}, false
		}
		return copyAct("distinto tamaño")
	case CompareContent:
		reason, differ, ok := contentDiffers(src, dst)
		if !ok {
			return Action{Rel: rel, Op: OpSkip, Reason: reason}, true
		}
		if !differ {
			return Action{}, false
		}
		return copyAct(reason)
	}
	// time+size
	if o.Mode == ModeMirror {
		switch {
		case newer:
			return copyAct("más nuevo en " + sd.src)
		case older:
			return copyAct("más viejo en " + sd.src)
		case src.Size != dst.Size:
			return copyAct("distinto tamaño")
		}
		return Action{}, false
	}
	switch {
	case newer:
		return copyAct("más nuevo en " + sd.src)
	case !older && src.Size != dst.Size:
		return Action{Rel: rel, Op: OpSkip, Reason: "distinto tamaño con la misma fecha"}, true
	}
	return Action{}, false
}

// bothWays decide cuando se sincroniza en las dos direcciones: gana el más
// nuevo y no se borra nada.
func bothWays(rel string, l, s Entry, hasL, hasS bool, o Options) (Action, bool) {
	switch {
	case hasL && !hasS:
		return oneWay(rel, l, s, true, false, up, o)
	case !hasL && hasS:
		return oneWay(rel, s, l, true, false, down, o)
	case l.IsDir:
		return Action{}, false
	}

	var reason string
	switch o.Compare {
	case CompareSize:
		if l.Size == s.Size {
			return Action{}, false
		}
		reason = "distinto tamaño"
	case CompareContent:
		r, differ, ok := contentDiffers(l, s)
		if !ok {
			return Action{Rel: rel, Op: OpSkip, Reason: r}, true
		}
		if !differ {
			return Action{}, false
		}
		reason = r
	default:
		if l.Size == s.Size && abs(l.ModTime-s.ModTime) <= timeSlack {
			return Action{}, false
		}
		reason = "distinto tamaño"
	}
	switch {
	case l.ModTime-s.ModTime > timeSlack:
		return Action{Rel: rel, Op: OpUpload, Reason: "más nuevo en la PC", Size: l.Size}, true
	case s.ModTime-l.ModTime > timeSlack:
		return Action{Rel: rel, Op: OpDownload, Reason: "más nuevo en el servidor", Size: s.Size}, true
	}
	return Action{Rel: rel, Op: OpSkip, Reason: reason + " con la misma fecha"}, true
}

// contentDiffers compara por contenido. ok es false si con tamaños iguales
// falta algún hash (no se pudo calcular).
func contentDiffers(a, b Entry) (reason string, differ, ok bool) {
	if a.Size != b.Size {
		return "distinto contenido", true, true
	}
	if a.Hash == "" || b.Hash == "" {
		return "no se pudo comparar el contenido", false, false
	}
	return "distinto contenido", a.Hash != b.Hash, true
}

// index arma el mapa por ruta relativa, sin lo excluido.
func index(entries []Entry, exclude []string) map[string]Entry {
	m := make(map[string]Entry, len(entries))
	for _, e := range entries {
		if !Excluded(e.Rel, exclude) {
			m[e.Rel] = e
		}
	}
	return m
}

// Excluded dice si rel coincide con algún patrón: por la ruta completa, por
// cualquier carpeta de la ruta (también como ruta parcial, "build/out") o por
// cualquiera de sus nombres. Los patrones vacíos o mal formados no excluyen.
func Excluded(rel string, patterns []string) bool {
	parts := strings.Split(rel, "/")
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		for i, name := range parts {
			if ok, _ := path.Match(p, name); ok {
				return true
			}
			if ok, _ := path.Match(p, strings.Join(parts[:i+1], "/")); ok {
				return true
			}
		}
	}
	return false
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

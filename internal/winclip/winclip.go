// Package winclip lee archivos del portapapeles del sistema para pegarlos en los
// exploradores de osat. Hay dos clases:
//
//   - Rutas (CF_HDROP): lo que copia el Explorador. Los archivos ya están en disco.
//   - Archivos virtuales (FileGroupDescriptorW + FileContents): lo que copia, por
//     ejemplo, el cliente de Escritorio remoto desde el servidor. Los datos no
//     están en esta PC: se piden recién al pegar, uno por uno.
//
// Solo hay en Windows; en los demás sistemas Current devuelve KindNone.
package winclip

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"osat/internal/i18n"
	"osat/internal/remote"
)

// Kind es qué archivos hay en el portapapeles del sistema.
type Kind string

const (
	KindNone    Kind = ""
	KindPaths   Kind = "paths"   // rutas de archivos en disco (Explorador)
	KindVirtual Kind = "virtual" // archivos que hay que pedir (escritorio remoto, adjuntos...)
)

// Info es lo que hay en el portapapeles, sin leer los archivos.
type Info struct {
	// Seq cambia cada vez que cambia el portapapeles: el frontend lo compara con el
	// del momento en que se copió en osat para saber qué se copió último.
	Seq  uint32 `json:"seq"`
	Kind Kind   `json:"kind"`
}

// Entry es un archivo o carpeta virtual (un FILEDESCRIPTORW).
type Entry struct {
	Name    string // ruta relativa, con \ o / entre carpetas
	Dir     bool
	ModTime time.Time // cero si no viene
	size    int64     // tamaño anunciado; -1 si no viene
}

// ErrNothing: no hay archivos en el portapapeles del sistema.
var ErrNothing = i18n.Err("winclip.nothing")

// parts separa la ruta relativa de una entrada y la valida: nada absoluto, ni
// "..", ni unidades o flujos alternativos (":"), ni componentes vacíos.
func parts(name string) ([]string, error) {
	name = strings.ReplaceAll(name, "/", `\`)
	ps := strings.Split(name, `\`)
	for _, p := range ps {
		if p == "" || p == "." || p == ".." || strings.ContainsAny(p, `:*?"<>|`) {
			return nil, i18n.Err("winclip.badName", name)
		}
	}
	return ps, nil
}

// save escribe las entradas en dir. open(i) da el contenido de la entrada i (solo
// se llama para archivos). Como pegar en los exploradores, nunca pisa nada: si un
// nombre de primer nivel ya existe en dir, esa entrada (y todo lo que tiene
// adentro) se renombra a "x - copia". Devuelve las rutas de primer nivel creadas.
func save(dir string, entries []Entry, open func(i int) (io.ReadCloser, error)) ([]string, error) {
	dir = filepath.Clean(dir)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil, i18n.Err("winclip.noDir", dir)
	}
	split := make([][]string, len(entries))
	topIsDir := map[string]bool{}
	var order []string
	for i, e := range entries {
		ps, err := parts(e.Name)
		if err != nil {
			return nil, err
		}
		split[i] = ps
		top := strings.ToLower(ps[0])
		if _, seen := topIsDir[top]; !seen {
			order = append(order, ps[0])
		}
		topIsDir[top] = topIsDir[top] || e.Dir || len(ps) > 1
	}
	// Nombre final de cada entrada de primer nivel (Windows no distingue mayúsculas).
	renamed := map[string]string{}
	taken := map[string]bool{}
	for _, top := range order {
		key := strings.ToLower(top)
		name := remote.CopyName(top, topIsDir[key], func(n string) bool {
			if taken[strings.ToLower(n)] {
				return true
			}
			_, err := os.Lstat(filepath.Join(dir, n))
			return err == nil
		})
		renamed[key] = name
		taken[strings.ToLower(name)] = true
	}
	var made []string
	for _, top := range order {
		made = append(made, filepath.Join(dir, renamed[strings.ToLower(top)]))
	}
	for i, e := range entries {
		ps := append([]string{renamed[strings.ToLower(split[i][0])]}, split[i][1:]...)
		dst := filepath.Join(append([]string{dir}, ps...)...)
		if e.Dir {
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return made, err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return made, err
		}
		if err := saveFile(dst, i, open); err != nil {
			return made, err
		}
		if !e.ModTime.IsZero() {
			_ = os.Chtimes(dst, e.ModTime, e.ModTime)
		}
	}
	return made, nil
}

func saveFile(dst string, i int, open func(i int) (io.ReadCloser, error)) error {
	src, err := open(i)
	if err != nil {
		return err
	}
	defer src.Close()
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, src); err != nil {
		f.Close()
		os.Remove(dst) // no dejar un archivo a medias
		return err
	}
	return f.Close()
}

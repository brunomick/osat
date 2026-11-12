// Package localfs da acceso al sistema de archivos de esta PC para el panel
// "Este equipo" (el lado local de un explorador estilo WinSCP).
package localfs

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"osat/internal/i18n"
	"osat/internal/remote"
	"osat/internal/textenc"
)

// Drives es la ruta especial de la lista de unidades (solo Windows).
const Drives = "::drives"

type Crumb struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type Listing struct {
	Path    string             `json:"path"`
	Parent  string             `json:"parent"` // "" si no hay nivel superior
	Crumbs  []Crumb            `json:"crumbs"`
	Entries []remote.FileEntry `json:"entries"`
}

func Home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return rootDir()
	}
	return h
}

// List lista una carpeta ("" = la carpeta personal).
func List(dir string) (Listing, error) {
	if dir == "" {
		dir = Home()
	}
	if dir == Drives {
		return listDrives(), nil
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return Listing{}, err
	}
	items, err := os.ReadDir(dir)
	if err != nil {
		return Listing{}, friendly(err)
	}
	out := Listing{Path: dir, Parent: parentOf(dir), Crumbs: crumbs(dir), Entries: []remote.FileEntry{}}
	for _, it := range items {
		p := filepath.Join(dir, it.Name())
		info, err := it.Info()
		if err != nil {
			continue // desapareció o no hay permiso para verlo
		}
		e := remote.FileEntry{Name: it.Name(), Path: p, Size: info.Size(), ModTime: info.ModTime().Unix(), IsDir: info.IsDir()}
		if info.Mode()&os.ModeSymlink != 0 {
			e.IsLink = true
			if t, err := os.Stat(p); err == nil {
				e.IsDir = t.IsDir()
				e.Size = t.Size()
			}
		}
		e.Mode = kindOf(e)
		out.Entries = append(out.Entries, e)
	}
	return out, nil
}

// kindOf describe el tipo para la columna "Tipo" (los permisos Unix no
// significan nada en Windows).
func kindOf(e remote.FileEntry) string {
	if e.IsDir {
		return "Carpeta"
	}
	if ext := strings.TrimPrefix(filepath.Ext(e.Name), "."); ext != "" && len(ext) <= 8 {
		return strings.ToUpper(ext)
	}
	return "Archivo"
}

func parentOf(dir string) string {
	p := filepath.Dir(dir)
	if p == dir {
		if hasDrives {
			return Drives
		}
		return ""
	}
	return p
}

func crumbs(dir string) []Crumb {
	var out []Crumb
	for d := dir; ; {
		name := filepath.Base(d)
		p := filepath.Dir(d)
		if p == d {
			out = append(out, Crumb{Name: strings.TrimSuffix(d, `\`), Path: d}) // "C:" o "/"
			break
		}
		out = append(out, Crumb{Name: name, Path: d})
		d = p
	}
	if hasDrives {
		out = append(out, Crumb{Name: i18n.T("native.thisComputer"), Path: Drives})
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// Tope del editor integrado (el mismo que para archivos remotos).
const maxEditSize = 2 * 1024 * 1024

var (
	ErrTooBig  = i18n.Err("remote.theFileIsTooBig")
	ErrNotText = i18n.Err("app.theFileLooksBinaryNot")
)

// readTextEnc lee el archivo y dice en qué codificación estaba (UTF-8 o, si no
// es UTF-8 válido, Windows-1252: ver textenc).
func readTextEnc(p string) (string, textenc.Encoding, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", textenc.UTF8, friendly(err)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxEditSize+1))
	if err != nil {
		return "", textenc.UTF8, friendly(err)
	}
	if len(b) > maxEditSize {
		return "", textenc.UTF8, ErrTooBig
	}
	s, enc, ok := textenc.Decode(b)
	if !ok {
		return "", enc, ErrNotText
	}
	return s, enc, nil
}

// ReadText lee un archivo de texto pequeño para el editor integrado.
func ReadText(p string) (string, error) {
	s, _, err := readTextEnc(p)
	return s, err
}

// WriteText sobrescribe el archivo en el lugar (como el Bloc de notas), así
// conserva sus permisos, atributos y fecha de creación. Lo guarda en la
// codificación que tenía (un .cpp en Windows-1252 sigue en Windows-1252).
func WriteText(p, content string) error {
	fi, err := os.Stat(p)
	if err != nil {
		return friendly(err)
	}
	// Si no se puede leer (binario, muy grande) se guarda como UTF-8.
	_, enc, _ := readTextEnc(p)
	data, err := textenc.Encode(content, enc)
	if err != nil {
		return err
	}
	return friendly(os.WriteFile(p, data, fi.Mode().Perm()))
}

func Mkdir(p string) error { return friendly(os.Mkdir(p, 0o755)) }

// sameFile dice si a y b son el mismo archivo o carpeta (sin seguir enlaces). Vale
// también cuando solo difieren en mayúsculas: según el sistema de archivos son el
// mismo (Windows, macOS) o dos distintos (Linux).
func sameFile(a, b string) bool {
	fa, err := os.Lstat(a)
	if err != nil {
		return false
	}
	fb, err := os.Lstat(b)
	return err == nil && os.SameFile(fa, fb)
}

// Rename cambia el nombre sin pisar nada que ya exista.
func Rename(from, to string) error {
	if _, err := os.Lstat(to); err == nil && !sameFile(from, to) {
		return i18n.Err("app.qAlreadyExists", filepath.Base(to))
	}
	return friendly(os.Rename(from, to))
}

// Move mueve archivos o carpetas a dir. Como en remote.Move, no pisa nada: si
// algún destino existe devuelve un error CONFLICT y no mueve ninguno.
func Move(paths []string, dir string) error {
	dir = filepath.Clean(dir)
	var found []string
	for _, p := range paths {
		p = filepath.Clean(p)
		if sameOrInside(dir, p) {
			return i18n.Err("remote.canTMoveQInto", filepath.Base(p))
		}
		if sameFile(filepath.Dir(p), dir) {
			continue
		}
		if _, err := os.Lstat(filepath.Join(dir, filepath.Base(p))); err == nil {
			found = append(found, filepath.Base(p))
		}
	}
	if len(found) > 0 {
		return remote.NewError(remote.CodeConflict, "Ya existe un elemento con ese nombre en la carpeta de destino.",
			map[string]string{"kind": "name", "names": strings.Join(found, "\n"), "count": strconv.Itoa(len(found))})
	}
	for _, p := range paths {
		p = filepath.Clean(p)
		if sameFile(filepath.Dir(p), dir) {
			continue
		}
		if err := os.Rename(p, filepath.Join(dir, filepath.Base(p))); err != nil {
			if !strings.EqualFold(filepath.VolumeName(p), filepath.VolumeName(dir)) {
				return i18n.Err("app.canTMoveBetweenDifferent")
			}
			return friendly(err)
		}
	}
	return nil
}

// Copy copia archivos y carpetas (con todo su contenido) a dir. Nunca pisa
// nada: si el nombre ya existe en dir (pegar en la misma carpeta) la copia se
// llama "x - copia.ext" (ver remote.CopyName). Devuelve las rutas creadas.
func Copy(paths []string, dir string) ([]string, error) {
	dir = filepath.Clean(dir)
	var out []string
	for _, p := range paths {
		p = filepath.Clean(p)
		fi, err := os.Lstat(p)
		if err != nil {
			return out, friendly(err)
		}
		if fi.IsDir() && sameOrInside(dir, p) {
			return out, i18n.Err("remote.canTCopyQInto", filepath.Base(p))
		}
		name := remote.CopyName(filepath.Base(p), fi.IsDir(), func(n string) bool {
			_, err := os.Lstat(filepath.Join(dir, n))
			return err == nil
		})
		dst := filepath.Join(dir, name)
		if err := copyEntry(p, fi, dst); err != nil {
			return out, friendly(err)
		}
		out = append(out, dst)
	}
	return out, nil
}

func copyEntry(src string, fi os.FileInfo, dst string) error {
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	case fi.IsDir():
		if err := os.Mkdir(dst, 0o755); err != nil {
			return err
		}
		children, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, c := range children {
			ci, err := c.Info()
			if err != nil {
				return err
			}
			if err := copyEntry(filepath.Join(src, c.Name()), ci, filepath.Join(dst, c.Name())); err != nil {
				return err
			}
		}
		_ = os.Chmod(dst, fi.Mode().Perm()) // al final: una carpeta de solo lectura no dejaría copiar adentro
		return nil
	case fi.Mode().IsRegular():
		return copyFile(src, fi, dst)
	default:
		return i18n.Err("remote.canTCopyQIt", filepath.Base(src))
	}
}

// copyFile copia el contenido, los permisos y la fecha de modificación. El
// destino se crea con O_EXCL: si apareció algo con ese nombre, falla en vez de pisarlo.
func copyFile(src string, fi os.FileInfo, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fi.Mode().Perm())
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(dst) // no dejar una copia a medias
		return err
	}
	_ = os.Chtimes(dst, time.Now(), fi.ModTime())
	return nil
}

// DeleteForever borra definitivamente cada elemento (lo que el frontend ofrece
// cuando no se puede mandar a la papelera). Es portable: lo usa app.go en todos
// los SO.
func DeleteForever(paths []string) error {
	for _, p := range paths {
		if err := os.RemoveAll(p); err != nil {
			return friendly(err)
		}
	}
	return nil
}

// sameOrInside: dir es p o está dentro de p.
func sameOrInside(dir, p string) bool {
	d, q := strings.ToLower(dir), strings.ToLower(p)
	if !caseInsensitive {
		d, q = dir, p
	}
	return d == q || strings.HasPrefix(d, strings.TrimSuffix(q, string(filepath.Separator))+string(filepath.Separator))
}

func friendly(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, os.ErrPermission):
		return i18n.Err("app.accessDenied")
	case errors.Is(err, os.ErrNotExist):
		return i18n.Err("app.doesNotExist")
	case errors.Is(err, os.ErrExist):
		return i18n.Err("app.alreadyExists")
	}
	return err
}

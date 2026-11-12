//go:build !windows

package localfs

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"osat/internal/i18n"
	"osat/internal/remote"
)

const (
	hasDrives       = false
	caseInsensitive = false
)

func rootDir() string { return "/" }

func listDrives() Listing { l, _ := List("/"); return l }

// errNoTrash: este elemento no puede ir a la papelera (otro sistema de archivos
// en Linux, o macOS que todavía no la tiene). Se sigue con los demás.
var errNoTrash = i18n.Err("app.canTBeMovedTo")

// Remove manda cada elemento a la papelera del usuario. Los que no pueden ir
// (errNoTrash) quedan intactos y al final se devuelve CodeNoTrash con sus
// posiciones en paths; cualquier otro error (no existe, acceso) corta.
func Remove(paths []string) error {
	var failed []string
	for i, p := range paths {
		err := trash(p)
		if errors.Is(err, errNoTrash) {
			failed = append(failed, strconv.Itoa(i))
			continue
		}
		if err != nil {
			return friendly(err)
		}
	}
	if len(failed) > 0 {
		// Solo posiciones: un nombre con salto de línea en data haría que el
		// frontend borre otra cosa.
		return remote.NewError(remote.CodeNoTrash, "No se puede enviar a la papelera.",
			map[string]string{"failed": strings.Join(failed, ",")})
	}
	return nil
}

// trash manda un elemento a la papelera del usuario (freedesktop.org Trash
// spec 1.0, sin $topdir/.Trash). En macOS todavía no hay papelera.
func trash(p string) error {
	if runtime.GOOS == "darwin" {
		return errNoTrash
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return err
	}
	dir := trashDir()
	files, info := filepath.Join(dir, "files"), filepath.Join(dir, "info")
	if err := os.MkdirAll(files, 0o700); err != nil {
		return errNoTrash // no se pudo preparar la papelera
	}
	if err := os.MkdirAll(info, 0o700); err != nil {
		return errNoTrash
	}
	// Elegir nombre libre (base, base.2, base.3...) reservando el .trashinfo.
	base := filepath.Base(abs)
	for n := 1; ; n++ {
		name := base
		if n > 1 {
			name = base + "." + strconv.Itoa(n)
		}
		if _, err := os.Lstat(filepath.Join(files, name)); err == nil {
			continue // ya ocupado en files/
		}
		f, err := os.OpenFile(filepath.Join(info, name+".trashinfo"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue // reservado por otro: probar el siguiente
		}
		if err != nil {
			return err
		}
		meta := fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n",
			(&url.URL{Path: abs}).EscapedPath(), time.Now().Format("2006-01-02T15:04:05"))
		_, werr := f.WriteString(meta)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			_ = os.Remove(filepath.Join(info, name+".trashinfo"))
			return werr
		}
		if err := os.Rename(abs, filepath.Join(files, name)); err != nil {
			_ = os.Remove(filepath.Join(info, name+".trashinfo")) // deshacer solo lo nuestro
			if errors.Is(err, syscall.EXDEV) {
				return errNoTrash // otro sistema de archivos: no puede ir a la papelera
			}
			return err
		}
		return nil
	}
}

// trashDir es la papelera del usuario: $XDG_DATA_HOME/Trash (si es absoluta) o
// $HOME/.local/share/Trash.
func trashDir() string {
	if d := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(d) {
		return filepath.Join(d, "Trash")
	}
	return filepath.Join(Home(), ".local", "share", "Trash")
}

func opener() string {
	if runtime.GOOS == "darwin" {
		return "open"
	}
	return "xdg-open"
}

func Open(p string) error { return exec.Command(opener(), p).Start() }

func Reveal(p string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", "-R", p).Start()
	}
	return exec.Command("xdg-open", filepath.Dir(p)).Start()
}

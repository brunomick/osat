package app

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"osat/internal/localfs"
	"osat/internal/winclip"
)

// ---------- Pegar en los exploradores lo copiado fuera de osat ----------
//
// "Pegar" en "Este equipo" o en un SFTP también acepta lo que hay en el
// portapapeles del sistema: rutas que copió el Explorador o archivos que copió
// otro programa sin que estén en disco, como el cliente de Escritorio remoto
// (vienen del servidor recién al pegar). Solo en Windows.

// ClipboardFiles dice qué archivos hay en el portapapeles del sistema.
func (a *App) ClipboardFiles() winclip.Info { return winclip.Current() }

// LocalPasteClipboard pega en dir los archivos del portapapeles del sistema y
// devuelve las rutas creadas. Como pegar en osat, nunca pisa nada.
func (a *App) LocalPasteClipboard(dir string) ([]string, error) {
	switch winclip.Current().Kind {
	case winclip.KindPaths:
		paths, err := winclip.Paths()
		if err != nil {
			return nil, err
		}
		return localfs.Copy(paths, dir)
	case winclip.KindVirtual:
		return winclip.SaveVirtual(dir)
	}
	return nil, winclip.ErrNothing
}

// clipTempPrefix: carpetas temporales con los archivos virtuales que se pegan en
// un SFTP. La subida queda en cola y sigue después de ClipboardUploadPaths, así
// que se borran al cerrar osat (y al abrirlo, por si quedó alguna).
const clipTempPrefix = "osat-pegar-"

// ClipboardUploadPaths devuelve las rutas para subir lo del portapapeles a un
// servidor. Los archivos virtuales se bajan antes a una carpeta temporal.
func (a *App) ClipboardUploadPaths() ([]string, error) {
	switch winclip.Current().Kind {
	case winclip.KindPaths:
		return winclip.Paths()
	case winclip.KindVirtual:
		dir, err := os.MkdirTemp("", clipTempPrefix)
		if err != nil {
			return nil, err
		}
		clipTemps.Lock()
		clipTemps.dirs = append(clipTemps.dirs, dir)
		clipTemps.Unlock()
		paths, err := winclip.SaveVirtual(dir)
		if err != nil {
			os.RemoveAll(dir)
			return nil, err
		}
		return paths, nil
	}
	return nil, winclip.ErrNothing
}

// clipTemps: las carpetas temporales que creó esta ventana de osat.
var clipTemps struct {
	sync.Mutex
	dirs []string
}

// cleanClipTemp borra al cerrar osat las carpetas temporales que creó.
func cleanClipTemp() {
	clipTemps.Lock()
	dirs := clipTemps.dirs
	clipTemps.dirs = nil
	clipTemps.Unlock()
	for _, d := range dirs {
		os.RemoveAll(d)
	}
}

// cleanOldClipTemp borra al abrir osat las que quedaron de un cierre inesperado.
// Solo las de más de un día: otra ventana de osat puede estar subiendo de una.
func cleanOldClipTemp() {
	dirs, _ := filepath.Glob(filepath.Join(os.TempDir(), clipTempPrefix+"*"))
	for _, d := range dirs {
		fi, err := os.Stat(d)
		if err == nil && fi.IsDir() && strings.HasPrefix(filepath.Base(d), clipTempPrefix) && time.Since(fi.ModTime()) > 24*time.Hour {
			os.RemoveAll(d)
		}
	}
}

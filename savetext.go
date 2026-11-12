package main

import (
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"osat/internal/i18n"
)

// SaveText guarda texto en un archivo .txt que elige el usuario (p. ej. lo que se hizo en una terminal).
// Devuelve la ruta, o "" si canceló.
func (a *App) SaveText(defaultName, text string) (string, error) {
	path, err := nativeSaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           i18n.T("native.saveTextTitle"),
		DefaultFilename: defaultName,
		Filters:         []runtime.FileFilter{{DisplayName: i18n.T("native.filterTxt"), Pattern: "*.txt"}},
	})
	if err != nil || path == "" {
		return "", err
	}
	if filepath.Ext(path) == "" {
		path += ".txt"
	}
	return path, os.WriteFile(path, []byte(text), 0o600)
}

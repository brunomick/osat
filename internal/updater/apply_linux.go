//go:build linux && !android

package updater

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"osat/internal/i18n"
)

// ErrNotWritable: el programa está en una carpeta del sistema (por ejemplo /usr/bin) y hay que actualizarlo a mano.
var ErrNotWritable = i18n.Err("update.notWritable")

// Detect dice si este osat se puede actualizar solo: hace falta poder escribir en su carpeta
// (instalar.sh lo deja en ~/.local/bin).
func Detect() (Kind, error) {
	exe, err := executable()
	if err != nil {
		return "", err
	}
	if !writableDir(filepath.Dir(exe)) {
		return "", ErrNotWritable
	}
	return KindLinux, nil
}

// Apply baja la versión nueva y reemplaza el programa (el que está corriendo sigue usando el viejo
// hasta cerrarse). Devuelve lo que lo vuelve a abrir al cerrar osat.
func Apply(ctx context.Context, rel Release, userAgent string, progress Progress) (Relaunch, error) {
	if _, err := Detect(); err != nil {
		return nil, err
	}
	asset, ok := PickAsset(rel, KindLinux)
	if !ok {
		return nil, i18n.Err("update.noAsset", rel.Version)
	}
	exe, err := executable()
	if err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "osat-update-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	dl := filepath.Join(tmp, asset.Name)
	if err := Download(ctx, asset.URL, dl, userAgent, progress); err != nil {
		return nil, err
	}
	next := exe + ".new" // en la misma carpeta: el rename de abajo es atómico
	if err := extractTarGzFile(dl, "osat-linux/osat", next); err != nil {
		os.Remove(next)
		return nil, err
	}
	if err := os.Rename(next, exe); err != nil {
		os.Remove(next)
		return nil, err
	}
	return func() error {
		// Espera a que este osat se cierre y abre el nuevo.
		cmd := exec.Command("sh", "-c", `while kill -0 "$1" 2>/dev/null; do sleep 0.2; done; exec "$2"`, "osat-update", fmt.Sprint(os.Getpid()), exe)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		return cmd.Start()
	}, nil
}

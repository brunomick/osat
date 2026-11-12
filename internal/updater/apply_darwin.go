package updater

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"osat/internal/i18n"
)

// ErrNotWritable: no se puede escribir donde está osat.app y hay que actualizarlo a mano.
var ErrNotWritable = i18n.Err("update.notWritable")

// bundle es la carpeta osat.app de este programa (…/osat.app/Contents/MacOS/osat).
func bundle() (string, error) {
	exe, err := executable()
	if err != nil {
		return "", err
	}
	b := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	if !strings.HasSuffix(b, ".app") {
		return "", i18n.Err("update.notApp")
	}
	return b, nil
}

// Detect dice si este osat se puede actualizar solo: hace falta poder escribir junto a osat.app.
func Detect() (Kind, error) {
	b, err := bundle()
	if err != nil {
		return "", err
	}
	if !writableDir(filepath.Dir(b)) {
		return "", ErrNotWritable
	}
	return KindMac, nil
}

// Apply baja la versión nueva y la deja descomprimida junto a osat.app. Al cerrar osat, el proceso
// que devuelve cambia osat.app por la nueva y la abre.
func Apply(ctx context.Context, rel Release, userAgent string, progress Progress) (Relaunch, error) {
	if _, err := Detect(); err != nil {
		return nil, err
	}
	asset, ok := PickAsset(rel, KindMac)
	if !ok {
		return nil, i18n.Err("update.noAsset", rel.Version)
	}
	b, err := bundle()
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
	stage, err := os.MkdirTemp(filepath.Dir(b), ".osat-update-") // mismo volumen: los mv de abajo no copian
	if err != nil {
		return nil, err
	}
	if err := extractZipAll(dl, stage); err != nil {
		os.RemoveAll(stage)
		return nil, err
	}
	next := filepath.Join(stage, "osat.app")
	if _, err := os.Stat(filepath.Join(next, "Contents", "MacOS")); err != nil {
		os.RemoveAll(stage)
		return nil, i18n.Err("update.missingInFile", "osat.app")
	}
	return func() error {
		script := `while kill -0 "$1" 2>/dev/null; do sleep 0.2; done
rm -rf "$2.old"
if mv "$2" "$2.old" && mv "$3" "$2"; then rm -rf "$2.old"; else mv "$2.old" "$2"; fi
rm -rf "$4"
open "$2"`
		cmd := exec.Command("sh", "-c", script, "osat-update", fmt.Sprint(os.Getpid()), b, next, stage)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		return cmd.Start()
	}, nil
}

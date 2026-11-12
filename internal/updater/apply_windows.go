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

// ErrNotWritable no se da en Windows (sin permisos se usa el instalador); está para que el código común compile igual.
var ErrNotWritable = i18n.Err("update.notWritable")

// Detect dice cómo se actualiza este osat: si su carpeta se puede escribir (el .zip portátil, o una
// copia suelta) se reemplaza osat.exe; si no (instalado en Archivos de programa), se corre el instalador.
func Detect() (Kind, error) {
	exe, err := executable()
	if err != nil {
		return "", err
	}
	if writableDir(filepath.Dir(exe)) {
		return KindWindowsPortable, nil
	}
	return KindWindowsInstaller, nil
}

// Apply baja la versión nueva y devuelve lo que la instala al cerrar osat.
func Apply(ctx context.Context, rel Release, userAgent string, progress Progress) (Relaunch, error) {
	kind, err := Detect()
	if err != nil {
		return nil, err
	}
	asset, ok := PickAsset(rel, kind)
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
	dl := filepath.Join(tmp, asset.Name)
	if err := Download(ctx, asset.URL, dl, userAgent, progress); err != nil {
		os.RemoveAll(tmp)
		return nil, err
	}

	var script string
	pid := os.Getpid()
	if kind == KindWindowsPortable {
		newExe := filepath.Join(tmp, "osat.exe")
		if err := extractZipFile(dl, "osat.exe", newExe); err != nil {
			os.RemoveAll(tmp)
			return nil, err
		}
		// Espera a que osat se cierre y copia el nuevo encima (reintenta un rato por si Windows todavía lo tiene abierto).
		script = fmt.Sprintf(`$ErrorActionPreference = 'SilentlyContinue'
Wait-Process -Id %d -Timeout 60
for ($i = 0; $i -lt 60; $i++) {
  try { Copy-Item -LiteralPath %s -Destination %s -Force -ErrorAction Stop; break } catch { Start-Sleep -Milliseconds 500 }
}
Start-Process -FilePath %s
Remove-Item -LiteralPath %s -Recurse -Force`, pid, psq(newExe), psq(exe), psq(exe), psq(tmp))
	} else {
		// El instalador pide permisos de administrador (Windows muestra el aviso) y corre sin preguntas (/S).
		// Si se cancela el aviso, vuelve a abrir la versión que estaba.
		script = fmt.Sprintf(`$ErrorActionPreference = 'SilentlyContinue'
Wait-Process -Id %d -Timeout 60
try { Start-Process -FilePath %s -ArgumentList '/S' -Verb RunAs -Wait -ErrorAction Stop } catch {}
Start-Process -FilePath %s
Remove-Item -LiteralPath %s -Recurse -Force`, pid, psq(dl), psq(exe), psq(tmp))
	}

	return func() error {
		cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-WindowStyle", "Hidden", "-Command", script)
		cmd.SysProcAttr = &syscall.SysProcAttr{
			HideWindow:    true,
			CreationFlags: 0x08000000 | 0x00000200, // CREATE_NO_WINDOW | CREATE_NEW_PROCESS_GROUP: sigue vivo al cerrarse osat
		}
		return cmd.Start()
	}, nil
}

// psq pone s entre comillas simples de PowerShell (las comillas de adentro se duplican).
func psq(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

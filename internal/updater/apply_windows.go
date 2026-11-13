package updater

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"

	"osat/internal/i18n"
)

// ErrNotWritable no se da en Windows (sin permisos se usa el instalador); está para que el código común compile igual.
var ErrNotWritable = i18n.Err("update.notWritable")

// uninstallKey es donde el instalador (build/windows/installer/project.nsi) anota la instalación: Wails
// la llama con la empresa y el producto, y la empresa, si wails.json no la dice, es el nombre del proyecto.
const uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\osatOSAT`

// userInstallDir es la carpeta de la instalación para este usuario, o "" si no hay.
func userInstallDir() string {
	k, err := registry.OpenKey(registry.CURRENT_USER, uninstallKey, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	dir, _, err := k.GetStringValue("InstallLocation")
	if err != nil {
		return ""
	}
	return dir
}

// UserInstallExe devuelve el osat instalado para este usuario cuando este es otro que no se puede
// actualizar sin permisos: la copia vieja de Archivos de programa, que tiene que abrir el nuevo (sus
// accesos directos siguen apuntando a ella y no se pueden borrar sin permisos de administrador).
func UserInstallExe() (string, bool) {
	exe, err := executable()
	if err != nil {
		return "", false
	}
	dir := userInstallDir()
	if dir == "" || strings.EqualFold(filepath.Clean(dir), filepath.Dir(exe)) || writableDir(filepath.Dir(exe)) {
		return "", false
	}
	target := filepath.Join(dir, "osat.exe")
	if _, err := os.Stat(target); err != nil {
		return "", false
	}
	return target, true
}

// Detect dice cómo se actualiza este osat. Instalado para el usuario, o en Archivos de programa (las
// versiones hasta la 1.1.0), se corre el instalador, que no pide permisos y deja osat instalado para
// el usuario. Si no, es el .zip portátil o una copia suelta: se reemplaza osat.exe.
func Detect() (Kind, error) {
	exe, err := executable()
	if err != nil {
		return "", err
	}
	if dir := userInstallDir(); dir != "" && strings.EqualFold(filepath.Clean(dir), filepath.Dir(exe)) {
		return KindWindowsInstaller, nil
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
		// El instalador corre sin preguntas (/S) y sin permisos de administrador: instala para el usuario.
		// Después se abre el osat de siempre; si era la copia de Archivos de programa, esa abre la
		// instalada para el usuario (UserInstallExe).
		script = fmt.Sprintf(`$ErrorActionPreference = 'SilentlyContinue'
Wait-Process -Id %d -Timeout 60
Start-Process -FilePath %s -ArgumentList '/S' -Wait
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

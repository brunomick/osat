package remote

import (
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"osat/internal/i18n"
)

// Nombres de dispositivo que Windows no permite como archivo (con o sin extensión).
var windowsReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// localName convierte un nombre de archivo remoto en un nombre local seguro.
// El nombre viene del servidor, así que no se confía en él: en Linux puede
// contener '\' o ':', que en Windows son separadores de ruta o alternate data
// streams y permitirían escribir fuera de la carpeta de destino.
func localName(name string) (string, error) {
	return localNameFor(runtime.GOOS, name)
}

func localNameFor(goos, name string) (string, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\x00") {
		return "", i18n.Err("remote.invalidRemoteFileNameQ", name)
	}
	if goos == "windows" {
		var b strings.Builder
		for _, r := range name {
			if r < 0x20 || strings.ContainsRune(`\:*?"<>|`, r) {
				b.WriteRune('_')
			} else {
				b.WriteRune(r)
			}
		}
		name = strings.TrimRight(b.String(), ". ")
		if name == "" {
			name = "_"
		}
		base := strings.ToUpper(strings.TrimSpace(strings.SplitN(name, ".", 2)[0]))
		if windowsReserved[base] {
			name = "_" + name
		}
	}
	// IsLocal aplica las reglas del SO actual; goos distinto solo pasa en tests.
	if goos == runtime.GOOS && !filepath.IsLocal(name) {
		return "", i18n.Err("remote.invalidRemoteFileNameQ", name)
	}
	return name, nil
}

// localPathUnder arma la ruta local para remotePath dentro de localRoot, donde
// remotePath debe estar dentro de remoteRoot. Cada componente pasa por
// localName, así que el resultado nunca sale de localRoot.
func localPathUnder(localRoot, remoteRoot, remotePath string) (string, error) {
	remoteRoot = path.Clean(remoteRoot)
	remotePath = path.Clean(remotePath)
	if remotePath == remoteRoot {
		return localRoot, nil
	}
	prefix := strings.TrimSuffix(remoteRoot, "/") + "/"
	if !strings.HasPrefix(remotePath, prefix) {
		return "", i18n.Err("remote.remotePathOutsideTheDownloaded", remotePath)
	}
	out := localRoot
	for _, part := range strings.Split(strings.TrimPrefix(remotePath, prefix), "/") {
		safe, err := localName(part)
		if err != nil {
			return "", err
		}
		out = filepath.Join(out, safe)
	}
	return out, nil
}

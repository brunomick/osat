//go:build !windows

package importer

import "osat/internal/i18n"

// Fuera de Windows la clave de safeStorage está en el Llavero (macOS) o en
// GNOME Keyring / KWallet (Linux): todavía no se lee. Las conexiones se
// importan sin contraseñas.
func openSafeStorage(string) (safeStorage, error) {
	return nil, i18n.Err("mongo.compassSecretsOS")
}

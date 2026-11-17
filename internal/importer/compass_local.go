package importer

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"osat/internal/i18n"
	"osat/internal/store"
)

// Conexiones que MongoDB Compass guarda en esta PC
// (packages/connection-storage/src/compass-main-connection-storage.ts): un
// archivo por conexión en <datos de Compass>/Connections/<id>.json, con
// {"connectionInfo": {...como en el archivo exportado...}, "connectionSecrets": "<base64>"}.
// connectionSecrets son las contraseñas en JSON, cifradas con safeStorage de
// Electron (ver compass_secrets_*.go). Compass guarda las favoritas y las
// últimas 10 que se usaron (recientes).

// compassDataDir es la carpeta de datos de Compass: %APPDATA%\MongoDB Compass en
// Windows, ~/Library/Application Support/MongoDB Compass en macOS y
// ~/.config/MongoDB Compass en Linux. Variable para los tests.
var compassDataDir = func() string {
	d, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, "MongoDB Compass")
}

type compassStored struct {
	ConnectionInfo    *compassConn `json:"connectionInfo"`
	ConnectionSecrets string       `json:"connectionSecrets"`
}

func scanCompassLocal(existing []store.Connection) (Result, error) {
	dir := compassDataDir()
	if dir == "" {
		return Result{}, ErrNotFound
	}
	connDir := filepath.Join(dir, "Connections")
	files, _ := filepath.Glob(filepath.Join(connDir, "*.json"))
	if len(files) == 0 {
		return Result{}, ErrNotFound
	}
	sort.Strings(files)

	var entries []compassEntry
	var skipped []string
	var safe safeStorage
	var safeErr error
	safeOpened := false
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return Result{}, err
		}
		var st compassStored
		if err := json.Unmarshal(b, &st); err != nil {
			skipped = append(skipped, i18n.T("mongo.compassBadStoredFile", filepath.Base(f)))
			continue
		}
		c := st.ConnectionInfo
		if c == nil || strings.TrimSpace(c.ConnectionOptions.ConnectionString) == "" {
			continue // de versiones viejas de Compass: Compass tampoco las muestra
		}
		e := compassEntry{conn: *c}
		e.recent = c.SavedConnectionType == "recent" || (c.SavedConnectionType == "" && c.Favorite == nil)
		if st.ConnectionSecrets != "" {
			if !safeOpened {
				safe, safeErr = openSafeStorage(dir)
				safeOpened = true
				if safeErr != nil {
					skipped = append(skipped, i18n.T("mongo.compassNoSecrets", safeErr))
				}
			}
			if safeErr == nil {
				sec, err := compassStoredSecrets(safe, st.ConnectionSecrets)
				if err != nil {
					skipped = append(skipped, i18n.T("mongo.compassSecretFailed", compassName(*c)))
				}
				e.sec = sec
			}
		}
		entries = append(entries, e)
	}
	items, more := compassToItems(entries, existing)
	return Result{Location: connDir, Items: items, Skipped: append(skipped, more...)}, nil
}

// compassStoredSecrets descifra el connectionSecrets de una conexión guardada:
// el JSON de las contraseñas, sin envolver en {"secrets": …} como en el exportado.
func compassStoredSecrets(safe safeStorage, enc string) (compassSecrets, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(enc))
	if err != nil {
		return compassSecrets{}, err
	}
	plain, err := safe.decrypt(raw)
	if err != nil {
		return compassSecrets{}, err
	}
	var sec compassSecrets
	if err := json.Unmarshal(plain, &sec); err != nil {
		return compassSecrets{}, err
	}
	return sec, nil
}

// compassName es el nombre con que Compass muestra la conexión (para los avisos).
func compassName(c compassConn) string {
	if c.Favorite != nil && strings.TrimSpace(c.Favorite.Name) != "" {
		return strings.TrimSpace(c.Favorite.Name)
	}
	return c.ConnectionOptions.ConnectionString[:min(len(c.ConnectionOptions.ConnectionString), 40)]
}

// safeStorage descifra lo que cifró safeStorage de Electron en esta PC.
type safeStorage interface {
	decrypt([]byte) ([]byte, error)
}

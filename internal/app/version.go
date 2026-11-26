package app

import "encoding/json"

// Resources son los archivos que osat lleva adentro y usa App. Los embebe el paquete main (main.go y main_android.go, en la
// raíz del repo), porque go:embed no llega a carpetas de más arriba, y los pasa con SetResources antes de NewApp.
type Resources struct {
	PackageJSON []byte // frontend/package.json: de ahí sale la versión, para no repetirla en Go
	Changelog   string // CHANGELOG.md, para mostrar las novedades después de actualizar (app_update.go)
	ChangelogEN string // CHANGELOG.en.md
	SiteFillJS  string // frontend/src/lib/sitefill.js: rellena el login de los sitios (app_sites.go)
}

var res Resources

// SetResources guarda los archivos embebidos. Va antes de NewApp.
func SetResources(r Resources) { res = r }

// Version es la versión de osat ("desconocida" si no se puede leer).
func Version() string { return appVersion() }

// appVersion devuelve la versión de osat ("desconocida" si no se puede leer).
func appVersion() string {
	var pkg struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(res.PackageJSON, &pkg) != nil || pkg.Version == "" {
		return "desconocida"
	}
	return pkg.Version
}

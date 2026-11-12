package main

import (
	_ "embed"
	"encoding/json"
)

// package.json del frontend: de ahí sale la versión, para no repetirla en Go.
//
//go:embed frontend/package.json
var packageJSON []byte

// El CHANGELOG, para mostrar las novedades después de actualizar (app_update.go).
//
//go:embed CHANGELOG.md
var changelogMD string

//go:embed CHANGELOG.en.md
var changelogENMD string

// appVersion devuelve la versión de osat ("desconocida" si no se puede leer).
func appVersion() string {
	var pkg struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(packageJSON, &pkg) != nil || pkg.Version == "" {
		return "desconocida"
	}
	return pkg.Version
}

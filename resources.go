package main

import (
	_ "embed"

	"osat/internal/app"
)

// Los archivos de la raíz del repo que usa App (internal/app): go:embed solo llega a la carpeta del paquete y a
// las de abajo, así que se embeben acá y se le pasan con app.SetResources.

//go:embed frontend/package.json
var packageJSON []byte

//go:embed CHANGELOG.md
var changelogMD string

//go:embed CHANGELOG.en.md
var changelogENMD string

//go:embed frontend/src/lib/sitefill.js
var siteFillJS string

func resources() app.Resources {
	return app.Resources{PackageJSON: packageJSON, Changelog: changelogMD, ChangelogEN: changelogENMD, SiteFillJS: siteFillJS}
}

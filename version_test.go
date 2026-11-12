package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// La versión vive en dos lugares (wails.json para el manifiesto del .exe y
// frontend/package.json para "Acerca de"): este test avisa si se desfasan.
// En una beta, package.json lleva "1.1.0-beta.1" y wails.json solo "1.1.0": la versión
// del .exe y del instalador de Windows tiene que ser numérica.
func TestVersionMatchesBetweenWailsAndPackageJSON(t *testing.T) {
	wailsRaw, err := os.ReadFile("wails.json")
	if err != nil {
		t.Fatalf("no se pudo leer wails.json: %v", err)
	}
	var wails struct {
		Info struct {
			ProductVersion string `json:"productVersion"`
		} `json:"info"`
	}
	if err := json.Unmarshal(wailsRaw, &wails); err != nil {
		t.Fatalf("wails.json no es un JSON válido: %v", err)
	}

	pkgRaw, err := os.ReadFile("frontend/package.json")
	if err != nil {
		t.Fatalf("no se pudo leer frontend/package.json: %v", err)
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(pkgRaw, &pkg); err != nil {
		t.Fatalf("frontend/package.json no es un JSON válido: %v", err)
	}

	if wails.Info.ProductVersion == "" || pkg.Version == "" {
		t.Fatalf("falta la versión: wails.json info.productVersion=%q, frontend/package.json version=%q",
			wails.Info.ProductVersion, pkg.Version)
	}
	if base, _, _ := strings.Cut(pkg.Version, "-"); wails.Info.ProductVersion != base {
		t.Errorf("las versiones no coinciden: wails.json info.productVersion=%q y frontend/package.json version=%q; igualá las dos, sin el -beta.N en wails.json (y las dos apariciones en frontend/package-lock.json)",
			wails.Info.ProductVersion, pkg.Version)
	}
}

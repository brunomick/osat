package app

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain le pasa a App los archivos de la raíz del repo, como hace main con los que embebe (resources.go).
func TestMain(m *testing.M) {
	read := func(p string) []byte {
		b, err := os.ReadFile(filepath.Join("..", "..", p))
		if err != nil {
			panic(err)
		}
		return b
	}
	SetResources(Resources{
		PackageJSON: read("frontend/package.json"),
		Changelog:   string(read("CHANGELOG.md")),
		ChangelogEN: string(read("CHANGELOG.en.md")),
		SiteFillJS:  string(read("frontend/src/lib/sitefill.js")),
	})
	os.Exit(m.Run())
}

package main

import (
	"os"
	"path/filepath"
	"testing"
)

const testChangelog = "# Cambios\n\n## [0.5.0] - 2026-11-12\n\n- Actualizaciones.\n\n## [0.4.0] - 2026-11-11\n\n- Otra cosa.\n"

func TestWhatsNewFreshInstall(t *testing.T) {
	dir := t.TempDir()
	if w := whatsNew(dir, "0.5.0", [2]string{testChangelog, testChangelog}); w.Version != "" {
		t.Errorf("una instalación nueva no muestra novedades: %+v", w)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "version")); string(b) != "0.5.0\n" {
		t.Errorf("version: %q", b)
	}
}

func TestWhatsNewFromOldVersionWithoutFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{}"), 0o600) // ya usaba osat (0.4.0 no guardaba la versión)
	w := whatsNew(dir, "0.5.0", [2]string{testChangelog, testChangelog})
	if w.Version != "0.5.0" || w.Notes != "- Actualizaciones." {
		t.Errorf("%+v", w)
	}
	if w := whatsNew(dir, "0.5.0", [2]string{testChangelog, testChangelog}); w.Version != "" {
		t.Errorf("la segunda vez ya no se muestra: %+v", w)
	}
}

func TestWhatsNewAfterUpdate(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "version"), []byte("0.4.0\n"), 0o600)
	if w := whatsNew(dir, "0.5.0", [2]string{testChangelog, testChangelog}); w.Version != "0.5.0" {
		t.Errorf("%+v", w)
	}
	// Volver a una versión anterior no muestra nada.
	if w := whatsNew(dir, "0.4.0", [2]string{testChangelog, testChangelog}); w.Version != "" {
		t.Errorf("%+v", w)
	}
	// Una versión sin sección en el CHANGELOG tampoco.
	if w := whatsNew(dir, "0.6.0", [2]string{testChangelog, testChangelog}); w.Version != "" {
		t.Errorf("%+v", w)
	}
}

func TestWhatsNewEnglish(t *testing.T) {
	en := "# Changelog\n\n## [0.5.0] - 2026-11-12\n\n- Updates.\n"
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "version"), []byte("0.4.0\n"), 0o600)
	if w := whatsNew(dir, "0.5.0", [2]string{en, testChangelog}); w.Notes != "- Updates." {
		t.Errorf("en inglés: %+v", w)
	}
	// Si al CHANGELOG en inglés le falta la versión, se usa el de español.
	os.WriteFile(filepath.Join(dir, "version"), []byte("0.4.0\n"), 0o600)
	if w := whatsNew(dir, "0.5.0", [2]string{"# Changelog\n", testChangelog}); w.Notes != "- Actualizaciones." {
		t.Errorf("sin la versión en inglés: %+v", w)
	}
}

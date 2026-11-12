package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"osat/internal/applog"
	"osat/internal/settings"
	"osat/internal/store"
)

func settingsApp(t *testing.T) *App {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("AppData", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)
	st, err := store.New()
	if err != nil {
		t.Fatal(err)
	}
	return &App{store: st, settings: settings.Defaults()}
}

// SettingsSave devuelve lo normalizado, lo guarda y cambia el nivel del log en caliente.
func TestSettingsSave(t *testing.T) {
	a := settingsApp(t)
	prev := slog.Default()
	if _, err := applog.Setup(a.store.Dir(), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		slog.SetDefault(prev)
		applog.Close()
	})
	debug := func() bool { return slog.Default().Enabled(context.Background(), slog.LevelDebug) }
	if debug() {
		t.Fatal("el nivel normal no debería registrar detalle")
	}

	in := a.SettingsGet()
	in.Terminal.FontSize = 500 // fuera de rango
	in.General.VerboseLog = true
	saved, err := a.SettingsSave(in)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Terminal.FontSize != 13 || !saved.General.VerboseLog {
		t.Fatalf("SettingsSave = %+v", saved)
	}
	if a.SettingsGet() != saved {
		t.Fatal("SettingsGet no devuelve lo guardado")
	}
	if !debug() {
		t.Fatal("el registro detallado no se aplicó")
	}
	onDisk, err := settings.Load(a.store.Dir())
	if err != nil || onDisk != saved {
		t.Fatalf("en disco: %+v, %v", onDisk, err)
	}

	in.General.VerboseLog = false
	if _, err := a.SettingsSave(in); err != nil {
		t.Fatal(err)
	}
	if debug() {
		t.Fatal("el nivel normal no volvió")
	}
}

// Si no se puede guardar, se devuelve el error y los ajustes en uso no cambian.
func TestSettingsSaveFailureKeepsCurrent(t *testing.T) {
	a := settingsApp(t)
	// Un directorio en el lugar de settings.json hace fallar el renombrado.
	if err := os.Mkdir(filepath.Join(a.store.Dir(), "settings.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	before := a.SettingsGet()
	in := before
	in.Terminal.FontSize = 20
	if _, err := a.SettingsSave(in); err == nil {
		t.Fatal("esperaba un error")
	}
	if a.SettingsGet() != before {
		t.Fatal("los ajustes en uso cambiaron a pesar del error")
	}
}

func TestSettingsDefaults(t *testing.T) {
	a := settingsApp(t)
	if a.SettingsDefaults() != settings.Defaults() {
		t.Fatal("SettingsDefaults no devuelve los valores por defecto")
	}
}

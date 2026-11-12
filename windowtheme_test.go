package main

import (
	"testing"

	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"osat/internal/settings"
)

func TestWindowTheme(t *testing.T) {
	if bg := windowBackground("light"); bg.R != 255 || bg.G != 255 || bg.B != 255 {
		t.Fatalf("claro = %+v", bg)
	}
	for _, th := range []string{"dark", "system", "", "x"} {
		if bg := windowBackground(th); bg.R != 10 || bg.G != 10 || bg.B != 10 {
			t.Fatalf("%q = %+v", th, bg)
		}
	}
	if bg := windowBackground("dracula"); bg.R != 0x28 || bg.G != 0x2a || bg.B != 0x36 {
		t.Fatalf("dracula = %+v", bg)
	}
	want := map[string]windows.Theme{
		"dark": windows.Dark, "light": windows.Light, "system": windows.SystemDefault, "": windows.Dark,
		"solarized-light": windows.Light, "dracula": windows.Dark, "one-dark": windows.Dark,
		"github-light": windows.Light, "rose-pine-dawn": windows.Light, "nord": windows.Dark,
	}
	// Cada tema de color tiene su fondo de ventana (sin él arrancaría con el negro de osat).
	for _, th := range settings.ColorThemes {
		if _, ok := themeBackgrounds[th]; !ok {
			t.Errorf("falta el fondo de ventana de %q", th)
		}
	}
	for in, w := range want {
		if got := startupWindowTheme(in); got != w {
			t.Fatalf("startupWindowTheme(%q) = %v", in, got)
		}
	}
}

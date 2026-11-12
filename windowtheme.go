package main

import (
	"runtime"

	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"osat/internal/settings"
)

// themeBackgrounds es el --bg de cada tema que no es el oscuro (app.css).
var themeBackgrounds = map[string]options.RGBA{
	settings.AppearanceLight:      {R: 255, G: 255, B: 255, A: 255},
	settings.ThemeDracula:         {R: 0x28, G: 0x2a, B: 0x36, A: 255},
	settings.ThemeSolarizedDark:   {R: 0x00, G: 0x2b, B: 0x36, A: 255},
	settings.ThemeSolarizedLight:  {R: 0xfd, G: 0xf6, B: 0xe3, A: 255},
	settings.ThemeGruvboxDark:     {R: 0x28, G: 0x28, B: 0x28, A: 255},
	settings.ThemeOneDark:         {R: 0x28, G: 0x2c, B: 0x34, A: 255},
	settings.ThemeNord:            {R: 0x2e, G: 0x34, B: 0x40, A: 255},
	settings.ThemeTokyoNight:      {R: 0x1a, G: 0x1b, B: 0x26, A: 255},
	settings.ThemeCatppuccinMocha: {R: 0x1e, G: 0x1e, B: 0x2e, A: 255},
	settings.ThemeMonokai:         {R: 0x27, G: 0x28, B: 0x22, A: 255},
	settings.ThemeGitHubDark:      {R: 0x0d, G: 0x11, B: 0x17, A: 255},
	settings.ThemeRosePine:        {R: 0x19, G: 0x17, B: 0x24, A: 255},
	settings.ThemeKanagawa:        {R: 0x1f, G: 0x1f, B: 0x28, A: 255},
	settings.ThemeEverforest:      {R: 0x2d, G: 0x35, B: 0x3b, A: 255},
	settings.ThemeGitHubLight:     {R: 0xff, G: 0xff, B: 0xff, A: 255},
	settings.ThemeCatppuccinLatte: {R: 0xef, G: 0xf1, B: 0xf5, A: 255},
	settings.ThemeRosePineDawn:    {R: 0xfa, G: 0xf4, B: 0xed, A: 255},
}

// windowBackground es el color de fondo de la ventana mientras carga: el --bg de cada tema (app.css).
func windowBackground(theme string) *options.RGBA {
	if bg, ok := themeBackgrounds[theme]; ok {
		return &bg
	}
	return &options.RGBA{R: 10, G: 10, B: 10, A: 255}
}

// lightTheme dice si el tema es de fondo claro (la barra de título de Windows va clara).
func lightTheme(theme string) bool {
	switch theme {
	case settings.AppearanceLight, settings.ThemeSolarizedLight, settings.ThemeGitHubLight,
		settings.ThemeCatppuccinLatte, settings.ThemeRosePineDawn:
		return true
	}
	return false
}

// startupWindowTheme es el tema de la ventana de Windows al arrancar. Con "sistema" el fondo es el oscuro
// (el frontend lo corrige en cuanto sabe el modo del sistema).
func startupWindowTheme(appearance string) windows.Theme {
	switch {
	case lightTheme(appearance):
		return windows.Light
	case appearance == settings.AppearanceSystem:
		return windows.SystemDefault
	}
	return windows.Dark
}

// SetWindowTheme cambia en vivo el fondo de la ventana y, en Windows, su tema. theme es el tema ya
// resuelto por el frontend: "dark", "light" o uno de los temas de color (con "sistema" el frontend
// manda el que corresponda).
func (a *App) SetWindowTheme(theme string) {
	if a.ctx == nil {
		return
	}
	bg := windowBackground(theme)
	nativeWindowSetBackgroundColour(a.ctx, bg.R, bg.G, bg.B, bg.A)
	if runtime.GOOS != "windows" {
		return // en macOS y Linux no hay barra propia que cambiar
	}
	if lightTheme(theme) {
		nativeWindowSetLightTheme(a.ctx)
	} else {
		nativeWindowSetDarkTheme(a.ctx)
	}
}

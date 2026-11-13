//go:build !android

package main

import (
	"embed"
	"fmt"
	"log"
	"log/slog"
	"os"
	"runtime"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"osat/internal/applog"
	"osat/internal/i18n"
	"osat/internal/settings"
	"osat/internal/store"
)

//go:embed all:frontend/dist
var assets embed.FS

// El ícono de la ventana en Linux: sin él, el escritorio muestra uno genérico si no asocia la
// ventana con el .desktop (por ejemplo, si se abre el programa sin haber corrido instalar.sh).
//
//go:embed build/appicon.png
var appIcon []byte

func main() {
	defer applog.Recover("main")
	if openUserInstall() {
		return
	}

	// Primero los ajustes (sin registrar nada: el log todavía no existe), después el log con
	// su nivel y recién ahí lo que haya que contar. Si el log no se puede abrir, osat sigue sin él.
	dir, err := store.ConfigDir()
	cfg := settings.Defaults()
	var cfgErr error
	if err == nil {
		cfg, cfgErr = settings.Load(dir)
		_, err = applog.Setup(dir, cfg.General.VerboseLog)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "osat: no se pudo abrir el archivo de log:", err)
	}
	i18n.SetLanguage(cfg.General.Language) // antes de wails.Run: los textos de Go salen en el idioma elegido
	slog.Info("arranque", "version", appVersion(), "os", runtime.GOOS, "arch", runtime.GOARCH, "dir", dir)
	if cfgErr != nil {
		slog.Warn("no se pudieron leer los ajustes, se usan los valores por defecto", "err", cfgErr)
	}

	app, err := NewApp(cfg)
	if err != nil {
		log.Fatal(err)
	}

	// En macOS, sin el menú Editar no andan Cmd+C/V/A en los campos de texto
	// ni en la terminal.
	var appMenu *menu.Menu
	if runtime.GOOS == "darwin" {
		appMenu = menu.NewMenuFromItems(menu.AppMenu(), menu.EditMenu())
	}

	err = wails.Run(&options.App{
		Title:     "OSAT",
		Width:     1280,
		Height:    800,
		MinWidth:  900,
		MinHeight: 560,
		// Arranca maximizada; Width/Height quedan como tamaño al restaurar.
		WindowStartState: options.Maximised,
		// Sin barra del sistema: la dibuja el frontend (TitleBar.svelte).
		// En Windows se mantienen el redimensionado por bordes y Aero Snap.
		// En macOS la ventana conserva su marco (sin él no hay semáforos):
		// la barra se oculta con TitleBarHidden.
		Frameless: runtime.GOOS != "darwin",
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: windowBackground(cfg.General.Appearance), // el --bg del tema elegido
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop:     true,
			DisableWebViewDrop: true,
		},
		Menu: appMenu,
		Bind: []interface{}{
			app,
		},
		Windows: &windows.Options{
			Theme: startupWindowTheme(cfg.General.Appearance),
		},
		Mac: &mac.Options{
			TitleBar: mac.TitleBarHidden(),
		},
		// ProgramName: el nombre con el que el escritorio agrupa la ventana con
		// su .desktop (build/linux/osat.desktop). WebviewGpuPolicyNever es lo que
		// Wails usa si Linux es nil; con un Linux propio hay que pedirlo.
		Linux: &linux.Options{
			ProgramName:      "osat",
			Icon:             appIcon,
			WebviewGpuPolicy: linux.WebviewGpuPolicyNever,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}

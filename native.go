package main

import "github.com/wailsapp/wails/v2/pkg/runtime"

// Lo que App le pide a la ventana nativa (eventos, diálogos, navegador, tema). En escritorio es el
// runtime de Wails; en Android lo reemplaza native_android.go, porque ahí no hay Wails.
var (
	nativeEventsEmit                = runtime.EventsEmit
	nativeOpenFileDialog            = runtime.OpenFileDialog
	nativeOpenMultipleFilesDialog   = runtime.OpenMultipleFilesDialog
	nativeOpenDirectoryDialog       = runtime.OpenDirectoryDialog
	nativeSaveFileDialog            = runtime.SaveFileDialog
	nativeBrowserOpenURL            = runtime.BrowserOpenURL
	nativeWindowSetBackgroundColour = runtime.WindowSetBackgroundColour
	nativeWindowSetLightTheme       = runtime.WindowSetLightTheme
	nativeWindowSetDarkTheme        = runtime.WindowSetDarkTheme
	nativeQuit                      = runtime.Quit
)

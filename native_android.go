package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// En Android no hay Wails: los eventos salen por el WebSocket de main_android.go y lo que necesita
// a Android (elegir archivos, abrir un enlace) se le pide a la app por ese mismo canal.

// hub reparte los eventos entre los WebSocket abiertos (en la práctica, el del WebView).
var hub = &eventHub{clients: map[chan []byte]struct{}{}}

type eventHub struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
}

func (h *eventHub) add() chan []byte {
	c := make(chan []byte, 4096)
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	return c
}

func (h *eventHub) remove(c chan []byte) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

func (h *eventHub) emit(name string, data ...interface{}) {
	if data == nil {
		data = []interface{}{}
	}
	msg, err := json.Marshal(map[string]interface{}{"n": name, "d": data})
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c <- msg:
		default: // el WebView no da abasto: se pierde el evento antes que trabar a quien lo emite
		}
	}
}

// Pedidos de archivos a Android en curso: el id va en el evento "android:pick" y vuelve por /picked.
var (
	pickSeq     atomic.Int64
	pickMu      sync.Mutex
	pickWaiting = map[int64]chan []string{}
)

func pickFiles(multiple bool) []string {
	id := pickSeq.Add(1)
	c := make(chan []string, 1)
	pickMu.Lock()
	pickWaiting[id] = c
	pickMu.Unlock()
	hub.emit("android:pick", id, multiple)
	return <-c
}

func pickDone(id int64, paths []string) {
	pickMu.Lock()
	c, ok := pickWaiting[id]
	delete(pickWaiting, id)
	pickMu.Unlock()
	if ok {
		c <- paths
	}
}

// downloadsDir es la carpeta que Android le da a la app para descargas (OSAT_DOWNLOADS): en Android
// no hay un selector de carpetas que devuelva rutas, así que las descargas y exportaciones van ahí.
func downloadsDir() (string, error) {
	dir := os.Getenv("OSAT_DOWNLOADS")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, "Downloads")
	}
	return dir, os.MkdirAll(dir, 0o700)
}

func init() {
	nativeEventsEmit = func(_ context.Context, name string, data ...interface{}) { hub.emit(name, data...) }
	nativeOpenFileDialog = func(context.Context, runtime.OpenDialogOptions) (string, error) {
		if p := pickFiles(false); len(p) > 0 {
			return p[0], nil
		}
		return "", nil
	}
	nativeOpenMultipleFilesDialog = func(context.Context, runtime.OpenDialogOptions) ([]string, error) {
		return pickFiles(true), nil
	}
	nativeOpenDirectoryDialog = func(context.Context, runtime.OpenDialogOptions) (string, error) {
		return downloadsDir()
	}
	nativeSaveFileDialog = func(_ context.Context, o runtime.SaveDialogOptions) (string, error) {
		dir, err := downloadsDir()
		if err != nil {
			return "", err
		}
		name := o.DefaultFilename
		if name == "" {
			name = "osat"
		}
		return filepath.Join(dir, filepath.Base(name)), nil
	}
	nativeBrowserOpenURL = func(_ context.Context, url string) { hub.emit("android:open", url) }
	nativeWindowSetBackgroundColour = func(context.Context, uint8, uint8, uint8, uint8) {}
	nativeWindowSetLightTheme = func(context.Context) {}
	nativeWindowSetDarkTheme = func(context.Context) {}
	nativeQuit = func(context.Context) {} // en Android osat no se actualiza solo
}

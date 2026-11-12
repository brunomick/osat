//go:build linux && !android && cgo && webkit2_41

package browser

// Motor de Linux: WebKitGTK 4.1 con un perfil propio (engine_linux.c tiene el lado de GTK).
//
// Cada pestaña es una WebKitWebView hija de un GtkOverlay. La primera vez que se abre una, el
// motor busca la ventana de osat (la de nivel superior que tiene la vista de Wails), la saca
// y la vuelve a poner adentro de un GtkOverlay; las vistas nuestras van como hijos del overlay,
// por encima de todo, ubicadas con la señal get-child-position (así no dependen de márgenes ni
// del sentido del idioma). Se hace al primer uso y no al arrancar porque New corre antes de que
// exista la ventana.
//
// Todo lo de GTK corre en el hilo de GTK (el del bucle principal de Wails). Las llamadas del
// resto de osat dejan una función en el bucle con g_idle_add y esperan el resultado; los
// eventos de WebKit llegan a ese mismo hilo. La única excepción es CloseAll en el cierre de
// osat: Wails llama a OnShutdown cuando el bucle ya terminó, así que ahí se usa GTK directo.
//
// La unidad de GTK es el píxel lógico, igual que el píxel CSS: scale no hace falta.

/*
#cgo pkg-config: gtk+-3.0 webkit2gtk-4.1
#include <stdlib.h>
#include "engine_linux.h"
*/
import "C"

import (
	"errors"
	"log/slog"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// Supported dice si este sistema tiene motor de navegador nativo.
func Supported() bool { return true }

// New crea el motor de WebKitGTK. El perfil (cookies, login) queda en profileDir. No toca GTK:
// la ventana todavía no existe, la vista y el perfil se crean al abrir la primera pestaña.
func New(profileDir string, emit func(Event)) (Engine, error) {
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return nil, err
	}
	if emit == nil {
		emit = func(Event) {}
	}
	e := &linuxEngine{profileDir: profileDir, emit: emit, tabs: map[string]unsafe.Pointer{}}
	active.Store(e)
	return e, nil
}

// Tiempos máximos de espera de quien llama (el hilo de GTK nunca espera a nadie).
const (
	callTimeout     = 10 * time.Second
	closeAllTimeout = 3 * time.Second // en el cierre de osat no hay que colgarse
)

var (
	errTimeout = errors.New("browser: el motor no respondió a tiempo")
	errNoTab   = errors.New("browser: pestaña inexistente")
)

type linuxEngine struct {
	profileDir string
	emit       func(Event)
	tabs       map[string]unsafe.Pointer // id → GtkWidget*; solo lo toca el hilo de GTK
}

var _ Engine = (*linuxEngine)(nil)

// active es el motor que recibe los eventos de C (las señales de GTK no saben de instancias).
var active atomic.Pointer[linuxEngine]

// ---------- Hilo de GTK ----------

var (
	jobsMu  sync.Mutex
	jobs    = map[uintptr]func(){}
	nextJob uintptr
)

// post deja fn en el bucle de GTK, que la corre en su hilo.
func post(fn func()) {
	jobsMu.Lock()
	nextJob++
	id := nextJob
	jobs[id] = fn
	jobsMu.Unlock()
	C.osat_post(C.uintptr_t(id))
}

//export osatRunIdle
func osatRunIdle(job C.uintptr_t) {
	jobsMu.Lock()
	fn := jobs[uintptr(job)]
	delete(jobs, uintptr(job))
	jobsMu.Unlock()
	if fn != nil {
		guard(fn)
	}
}

// guard evita que un pánico cruce a C.
func guard(fn func()) {
	defer func() {
		if v := recover(); v != nil {
			slog.Error("pánico en el motor del navegador", "panic", v)
		}
	}()
	fn()
}

// call corre fn en el hilo de GTK y espera su resultado.
func call(timeout time.Duration, fn func() error) error {
	ch := make(chan error, 1)
	post(func() { ch <- fn() })
	select {
	case err := <-ch:
		return err
	case <-time.After(timeout):
		return errTimeout
	}
}

// callTab corre fn sobre la pestaña id, en el hilo de GTK.
func (e *linuxEngine) callTab(id string, fn func(v *C.GtkWidget)) error {
	return call(callTimeout, func() error {
		v := e.tabs[id]
		if v == nil {
			return errNoTab
		}
		fn((*C.GtkWidget)(v))
		return nil
	})
}

// ---------- Eventos de C ----------

//export osatEvent
func osatEvent(id, kind, url, title *C.char, canBack, canFwd C.int) {
	e := active.Load()
	if e == nil {
		return
	}
	guard(func() {
		e.emit(Event{
			ID: C.GoString(id), Kind: C.GoString(kind), URL: C.GoString(url), Title: C.GoString(title),
			CanBack: canBack != 0, CanFwd: canFwd != 0,
		})
	})
}

//export osatViewDestroyed
func osatViewDestroyed(id *C.char, view unsafe.Pointer) {
	e := active.Load()
	if e == nil {
		return
	}
	if key := C.GoString(id); e.tabs[key] == view { // la ventana puede destruirse con las vistas adentro
		delete(e.tabs, key)
	}
}

// ---------- Engine ----------

func (e *linuxEngine) Open(id, url string, b Bounds, scale float64) error {
	return call(callTimeout, func() error {
		if v := e.tabs[id]; v != nil { // ya abierta: solo se acomoda
			C.osat_view_bounds((*C.GtkWidget)(v), C.int(b.X), C.int(b.Y), C.int(b.W), C.int(b.H))
			return nil
		}
		cdir, cid, curl := C.CString(e.profileDir), C.CString(id), C.CString(url)
		defer C.free(unsafe.Pointer(cdir))
		defer C.free(unsafe.Pointer(cid))
		defer C.free(unsafe.Pointer(curl))
		var cerr *C.char
		v := C.osat_view_new(cdir, cid, curl, C.int(b.X), C.int(b.Y), C.int(b.W), C.int(b.H), &cerr)
		if v == nil {
			defer C.g_free(C.gpointer(cerr))
			return errors.New(C.GoString(cerr))
		}
		e.tabs[id] = unsafe.Pointer(v)
		return nil
	})
}

func (e *linuxEngine) Navigate(id, url string) error {
	return e.callTab(id, func(v *C.GtkWidget) {
		curl := C.CString(url)
		defer C.free(unsafe.Pointer(curl))
		C.osat_view_navigate(v, curl)
	})
}

func (e *linuxEngine) Back(id string) error {
	return e.callTab(id, func(v *C.GtkWidget) { C.osat_view_back(v) })
}

func (e *linuxEngine) Forward(id string) error {
	return e.callTab(id, func(v *C.GtkWidget) { C.osat_view_forward(v) })
}

func (e *linuxEngine) Reload(id string) error {
	return e.callTab(id, func(v *C.GtkWidget) { C.osat_view_reload(v) })
}

func (e *linuxEngine) SetBounds(id string, b Bounds, scale float64) error {
	post(func() {
		if v := e.tabs[id]; v != nil {
			C.osat_view_bounds((*C.GtkWidget)(v), C.int(b.X), C.int(b.Y), C.int(b.W), C.int(b.H))
		}
	})
	return nil
}

func (e *linuxEngine) SetVisible(id string, visible bool) error {
	post(func() {
		if v := e.tabs[id]; v != nil {
			show := C.int(0)
			if visible {
				show = 1
			}
			C.osat_view_visible((*C.GtkWidget)(v), show)
		}
	})
	return nil
}

func (e *linuxEngine) Close(id string) {
	post(func() {
		if v := e.tabs[id]; v != nil {
			C.osat_view_close((*C.GtkWidget)(v)) // la señal destroy la saca de e.tabs
		}
	})
}

// closeAll cierra todas las vistas y suelta el perfil, así WebKit puede terminar su proceso de
// red y guardar las cookies.
func (e *linuxEngine) closeAll() {
	for _, v := range e.tabs {
		C.osat_view_close((*C.GtkWidget)(v))
	}
	C.osat_release_context()
}

// CloseAll cierra todas las vistas. Espera un rato corto: si el hilo de GTK no contesta, el
// cierre sigue igual.
func (e *linuxEngine) CloseAll() {
	// Si el bucle de GTK ya terminó (el cierre de osat), el contexto está libre y se usa GTK acá.
	// El hilo no puede cambiar entre tomarlo y soltarlo.
	runtime.LockOSThread()
	if C.osat_try_acquire() != 0 {
		e.closeAll()
		C.osat_release()
		runtime.UnlockOSThread()
		return
	}
	runtime.UnlockOSThread()
	if err := call(closeAllTimeout, func() error { e.closeAll(); return nil }); err != nil {
		slog.Warn("el navegador nativo no terminó de cerrarse", "err", err)
	}
}

//go:build windows

package browser

// Prueba de humo del motor real: abre una ventana, navega, intercepta window.open y
// comprueba que la cookie sobrevive a cerrar y reabrir el motor (el login se mantiene).
// Solo corre a mano: OSAT_SMOKE=1 go.exe test -run TestSmokeEngine -v ./internal/browser/

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	smUser32        = windows.NewLazySystemDLL("user32.dll")
	pCreateWindowEx = smUser32.NewProc("CreateWindowExW")
	pPeekMessage    = smUser32.NewProc("PeekMessageW")
	pTranslate      = smUser32.NewProc("TranslateMessage")
	pDispatch       = smUser32.NewProc("DispatchMessageW")
	pDestroyWindow  = smUser32.NewProc("DestroyWindow")
)

type smMsg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      struct{ x, y int32 }
}

func TestSmokeEngine(t *testing.T) {
	if os.Getenv("OSAT_SMOKE") == "" {
		t.Skip("solo a mano")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/set":
			fmt.Fprint(w, `<html><head><title>set</title></head><body><script>
document.cookie="login=ok; max-age=3600; path=/";
document.title="cookie-set";
window.open("`+"http://example.invalid/nueva"+`");
</script></body></html>`)
		case "/get":
			fmt.Fprint(w, `<html><head><title>x</title></head><body><script>document.title="got:"+document.cookie;</script></body></html>`)
		}
	}))
	defer srv.Close()

	// Ventana de nivel superior visible, con su bucle de mensajes en un hilo propio.
	stop := make(chan struct{})
	ready := make(chan uintptr)
	go func() {
		runtime.LockOSThread()
		cls, _ := windows.UTF16PtrFromString("STATIC")
		title, _ := windows.UTF16PtrFromString("osat-smoke")
		const wsOverlappedWindowVisible = 0x00CF0000 | 0x10000000
		h, _, _ := pCreateWindowEx.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(title)),
			wsOverlappedWindowVisible, 100, 100, 900, 700, 0, 0, 0, 0)
		ready <- h
		var m smMsg
		for {
			select {
			case <-stop:
				pDestroyWindow.Call(h)
				return
			default:
			}
			for {
				r, _, _ := pPeekMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, 1)
				if r == 0 {
					break
				}
				pTranslate.Call(uintptr(unsafe.Pointer(&m)))
				pDispatch.Call(uintptr(unsafe.Pointer(&m)))
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	if h := <-ready; h == 0 {
		t.Fatal("no se creó la ventana")
	}
	defer close(stop)

	profile := t.TempDir()
	run := func(path string, want func(Event) bool) []Event {
		evs := make(chan Event, 64)
		eng, err := New(profile, func(e Event) { evs <- e })
		if err != nil {
			t.Fatal("New:", err)
		}
		defer eng.CloseAll()
		if err := eng.Open("t1", srv.URL+path, Bounds{X: 10, Y: 10, W: 600, H: 400}, 1); err != nil {
			t.Fatal("Open:", err)
		}
		var got []Event
		deadline := time.After(30 * time.Second)
		for {
			select {
			case e := <-evs:
				got = append(got, e)
				t.Logf("evento %+v", e)
				if want(e) {
					return got
				}
			case <-deadline:
				t.Fatalf("no llegó el evento esperado; llegaron %+v", got)
			}
		}
	}

	sawNew, sawTitle := false, false
	run("/set", func(e Event) bool {
		if e.Kind == "newWindow" && e.URL == "http://example.invalid/nueva" {
			sawNew = true
		}
		if e.Title == "cookie-set" {
			sawTitle = true
		}
		return sawNew && sawTitle
	})
	run("/get", func(e Event) bool { return e.Title == "got:login=ok" })
}

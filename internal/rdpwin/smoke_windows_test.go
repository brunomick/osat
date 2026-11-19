//go:build windows

package rdpwin

// Prueba de humo del control real: lo aloja en una ventana propia, intenta conectar a un
// puerto cerrado y espera que avise la desconexión con el texto de Windows.
// Solo corre a mano: OSAT_SMOKE=1 go test -run TestSmoke -v ./internal/rdpwin/

import (
	"log/slog"
	"net"
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"osat/internal/browser"
)

func TestSmokeConnectFailsWithWindowsText(t *testing.T) {
	if os.Getenv("OSAT_SMOKE") == "" {
		t.Skip("solo a mano")
	}
	slog.SetLogLoggerLevel(slog.LevelDebug) // muestra las propiedades que el control no aceptó
	// Un puerto que seguro está cerrado.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	parent := testWindow(t)
	events := make(chan Event, 16)
	eng, err := New(func() uintptr { return parent }, func(ev Event) { events <- ev })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.CloseAll)

	b := browser.Bounds{X: 0, Y: 0, W: 800, H: 600}
	if err := eng.Open("t1", Config{Server: "127.0.0.1", Port: port, User: "nadie", Password: "x"}, b, 1); err != nil {
		t.Fatal(err)
	}
	eng.SetVisible("t1", true)
	eng.SetBounds("t1", browser.Bounds{X: 0, Y: 0, W: 900, H: 640}, 1.5)

	deadline := time.After(60 * time.Second)
	for {
		select {
		case ev := <-events:
			t.Logf("evento: %+v", ev)
			if ev.Kind == "disconnected" {
				if ev.Reason == 0 || ev.Text == "" {
					t.Fatalf("desconexión sin motivo: %+v", ev)
				}
				return
			}
		case <-deadline:
			t.Fatal("el control no avisó la desconexión")
		}
	}
}

var (
	tUser32         = windows.NewLazySystemDLL("user32.dll")
	tCreateWindowEx = tUser32.NewProc("CreateWindowExW")
	tPeekMessage    = tUser32.NewProc("PeekMessageW")
	tTranslate      = tUser32.NewProc("TranslateMessage")
	tDispatch       = tUser32.NewProc("DispatchMessageW")
	tDestroyWindow  = tUser32.NewProc("DestroyWindow")
)

// testWindow abre una ventana de nivel superior visible con su bucle de mensajes en un hilo propio.
func testWindow(t *testing.T) uintptr {
	stop := make(chan struct{})
	ready := make(chan uintptr)
	go func() {
		runtime.LockOSThread()
		class, _ := windows.UTF16PtrFromString("STATIC")
		title, _ := windows.UTF16PtrFromString("osat rdpwin smoke")
		const wsOverlappedWindow, wsVisible, wsClipChildren = 0x00CF0000, 0x10000000, 0x02000000
		h, _, _ := tCreateWindowEx.Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)),
			wsOverlappedWindow|wsVisible|wsClipChildren, 0, 0, uintptr(envInt("OSAT_SMOKE_W", 982)+20), uintptr(envInt("OSAT_SMOKE_H", 680)+40), 0, 0, 0, 0)
		ready <- h
		var m msg
		for {
			select {
			case <-stop:
				tDestroyWindow.Call(h)
				return
			default:
			}
			for {
				r, _, _ := tPeekMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, 1)
				if r == 0 {
					break
				}
				tTranslate.Call(uintptr(unsafe.Pointer(&m)))
				tDispatch.Call(uintptr(unsafe.Pointer(&m)))
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	h := <-ready
	if h == 0 {
		t.Fatal("no se pudo crear la ventana de prueba")
	}
	t.Cleanup(func() { close(stop) })
	return h
}

// Contra un servidor de verdad (sin NLA, como xrdp): espera "connected" y deja la ventana
// abierta un rato para mirarla. OSAT_SMOKE_RDP=127.0.0.1:3390 go test -run TestSmokeRealServer -v ./internal/rdpwin/
func TestSmokeRealServer(t *testing.T) {
	addr := os.Getenv("OSAT_SMOKE_RDP")
	if addr == "" {
		t.Skip("solo a mano, con OSAT_SMOKE_RDP=host:puerto")
	}
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := net.LookupPort("tcp", portText)
	parent := testWindow(t)
	events := make(chan Event, 16)
	eng, err := New(func() uintptr { return parent }, func(ev Event) { events <- ev })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.CloseAll)
	if err := eng.Open("t1", Config{Server: host, Port: port, User: os.Getenv("OSAT_SMOKE_USER"), Password: os.Getenv("OSAT_SMOKE_PASS")}, browser.Bounds{W: envInt("OSAT_SMOKE_W", 982), H: envInt("OSAT_SMOKE_H", 680)}, 1); err != nil { // 982: par pero no múltiplo de 4
		t.Fatal(err)
	}
	lateShow := os.Getenv("OSAT_SMOKE_LATE_SHOW") != "" // como la pestaña: oculta hasta "connected"
	if !lateShow {
		eng.SetVisible("t1", true)
	}
	deadline := time.After(time.Duration(30+envInt("OSAT_SMOKE_HOLD", 15)) * time.Second)
	for {
		select {
		case ev := <-events:
			t.Logf("evento: %+v", ev)
			if ev.Kind == "disconnected" {
				t.Fatalf("se desconectó: %+v", ev)
			}
			if ev.Kind == "connected" {
				if lateShow {
					eng.SetVisible("t1", true)
				}
				if msg := os.Getenv("OSAT_SMOKE_NOTICE"); msg != "" {
					time.Sleep(3 * time.Second)
					eng.Notice("t1", msg)
				}
				time.Sleep(time.Duration(envInt("OSAT_SMOKE_HOLD", 15)) * time.Second) // para mirar (o probar) la sesión
				return
			}
		case <-deadline:
			t.Fatal("no conectó")
		}
	}
}

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil {
		return v
	}
	return def
}

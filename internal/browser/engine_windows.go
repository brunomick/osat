//go:build windows

package browser

// Motor de Windows: WebView2 (go-webview2) con un perfil propio.
//
// Todo corre en un hilo propio, con su bucle de mensajes, para no depender del hilo de la
// interfaz de Wails (que no es nuestro). Las llamadas del resto de osat no tocan COM: dejan
// una función en una cola y despiertan al hilo con un mensaje; el hilo es el único que usa
// WebView2 y las ventanas.
//
// Cada pestaña es una ventana hija (WS_CHILD) de la ventana principal de osat, y dentro de
// ella va un WebView2. La ventana hija se crea con WS_EX_NOPARENTNOTIFY: sin eso, crearla o
// destruirla le manda un mensaje síncrono al hilo de la ventana principal, y si ese hilo está
// esperando a este (por ejemplo, en el cierre) quedaría colgado.
//
// El paquete pkg/edge de go-webview2 es el de Wails: cierra el proceso ante cualquier error
// de COM, hace eco de los mensajes de la página y no tiene NewWindowRequested, ni el
// historial, ni el título. Por eso acá solo se usan sus tipos de punteros COM; las llamadas
// que le faltan se hacen por el número de método de la vtable (comCall), y los eventos entran
// por objetos COM mínimos (comHandler). La vista no registra ningún objeto del host ni
// escucha mensajes de la página.

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/wailsapp/go-webview2/pkg/edge"
	"github.com/wailsapp/go-webview2/webviewloader"
	"golang.org/x/sys/windows"

	"osat/internal/winutil"
)

// Supported dice si este sistema tiene motor de navegador nativo.
func Supported() bool { return true }

// New crea el motor de WebView2. El perfil (cookies, login) queda en profileDir.
// Falla si no está instalado el runtime de WebView2.
func New(profileDir string, emit func(Event)) (Engine, error) {
	if _, err := webviewloader.GetAvailableCoreWebView2BrowserVersionString(""); err != nil {
		return nil, fmt.Errorf("browser: no se encontró el runtime de WebView2: %w", err)
	}
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return nil, err
	}
	if emit == nil {
		emit = func(Event) {}
	}
	e := &winEngine{profileDir: profileDir, emit: emit, tabs: map[string]*tab{}}
	ready := make(chan error, 1)
	go e.run(ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	return e, nil
}

// Tiempos máximos de espera de quien llama (el hilo del motor nunca espera a nadie).
const (
	callTimeout     = 10 * time.Second // abrir una vista puede tardar: arranca el proceso de WebView2
	closeAllTimeout = 3 * time.Second  // en el cierre de osat no hay que colgarse
)

var (
	errTimeout  = errors.New("browser: el motor no respondió a tiempo")
	errNoTab    = errors.New("browser: pestaña inexistente")
	errNotReady = errors.New("browser: la vista todavía se está creando")
	errClosed   = errors.New("browser: la vista se cerró antes de crearse")
)

// ---------- Win32 ----------

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassEx   = user32.NewProc("RegisterClassExW")
	procCreateWindowEx    = user32.NewProc("CreateWindowExW")
	procDestroyWindow     = user32.NewProc("DestroyWindow")
	procSetWindowPos      = user32.NewProc("SetWindowPos")
	procDefWindowProc     = user32.NewProc("DefWindowProcW")
	procGetMessage        = user32.NewProc("GetMessageW")
	procPeekMessage       = user32.NewProc("PeekMessageW")
	procTranslateMessage  = user32.NewProc("TranslateMessage")
	procDispatchMessage   = user32.NewProc("DispatchMessageW")
	procPostThreadMessage = user32.NewProc("PostThreadMessageW")
	procGetModuleHandle   = kernel32.NewProc("GetModuleHandleW")
)

const (
	wmApp = 0x8000 // WM_APP: despierta al hilo para que vacíe la cola

	wsChild            = 0x40000000
	wsClipChildren     = 0x02000000
	wsClipSiblings     = 0x04000000
	wsExNoParentNotify = 0x00000004

	swpNoActivate = 0x0010
	swpShowWindow = 0x0040
	swpHideWindow = 0x0080

	errClassAlreadyExists = 1410
)

const viewClass = "OsatSiteView"

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	ptX     int32
	ptY     int32
}

type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   uintptr
	icon       uintptr
	cursor     uintptr
	background uintptr
	menuName   *uint16
	className  *uint16
	iconSm     uintptr
}

// registerViewClass registra la clase de las ventanas hijas (no hacen nada propio: DefWindowProc).
func registerViewClass() error {
	inst, _, _ := procGetModuleHandle.Call(0)
	name, err := windows.UTF16PtrFromString(viewClass)
	if err != nil {
		return err
	}
	if err := procDefWindowProc.Find(); err != nil {
		return err
	}
	wc := wndClassEx{wndProc: procDefWindowProc.Addr(), instance: inst, className: name}
	wc.size = uint32(unsafe.Sizeof(wc))
	r, _, err := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
	if r == 0 && err != syscall.Errno(errClassAlreadyExists) {
		return err
	}
	return nil
}

func createViewWindow(parent uintptr) (uintptr, error) {
	inst, _, _ := procGetModuleHandle.Call(0)
	name, err := windows.UTF16PtrFromString(viewClass)
	if err != nil {
		return 0, err
	}
	h, _, err := procCreateWindowEx.Call(
		wsExNoParentNotify,
		uintptr(unsafe.Pointer(name)), 0,
		wsChild|wsClipChildren|wsClipSiblings,
		0, 0, 1, 1,
		parent, 0, inst, 0)
	if h == 0 {
		return 0, fmt.Errorf("browser: no se pudo crear la ventana de la vista: %w", err)
	}
	return h, nil
}

// ---------- COM ----------

// Posición de cada método en la vtable (los tres primeros son IUnknown). Es el orden de
// WebView2.idl; el mismo que tienen las vtables de pkg/edge.
const (
	slotRelease = 2

	slotEnvCreateController = 3 // ICoreWebView2Environment.CreateCoreWebView2Controller

	// ICoreWebView2Controller (PutBounds y PutIsVisible los resuelve edge.ICoreWebView2Controller)
	slotCtrlNotifyMoved = 23 // NotifyParentWindowPositionChanged
	slotCtrlClose       = 24

	slotWvGetSource           = 4 // ICoreWebView2
	slotWvNavigate            = 5
	slotWvAddSourceChanged    = 11
	slotWvAddHistoryChanged   = 13
	slotWvAddNavCompleted     = 15
	slotWvReload              = 31
	slotWvGetCanGoBack        = 38
	slotWvGetCanGoForward     = 39
	slotWvGoBack              = 40
	slotWvGoForward           = 41
	slotWvAddNewWindow        = 44
	slotWvAddTitleChanged     = 46
	slotWvGetDocumentTitle    = 48
	slotSettingsPutWebMessage = 6  // ICoreWebView2Settings.put_IsWebMessageEnabled
	slotSettingsPutHostObject = 16 // put_AreHostObjectsAllowed

	slotNewWinGetURI     = 3 // ICoreWebView2NewWindowRequestedEventArgs
	slotNewWinPutHandled = 6

	slotNavDoneIsSuccess = 3 // ICoreWebView2NavigationCompletedEventArgs
	slotNavDoneErrStatus = 4

	webErrorOperationCanceled = 14 // COREWEBVIEW2_WEB_ERROR_STATUS_OPERATION_CANCELED
)

// ptr convierte un puntero COM que llegó como uintptr (memoria que no es de Go) en unsafe.Pointer.
func ptr(u uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&u)) }

// comCall llama al método número slot del objeto COM obj y devuelve el HRESULT.
func comCall(obj unsafe.Pointer, slot int, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(obj)
	fn := *(*uintptr)(unsafe.Add(vtbl, uintptr(slot)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{uintptr(obj)}, args...)...)
	return r
}

func hrError(what string, hr uintptr) error {
	if int32(hr) >= 0 {
		return nil
	}
	return fmt.Errorf("browser: %s falló (0x%08x)", what, uint32(hr))
}

// comString lee un LPWSTR de salida de un método COM y lo libera.
func comString(obj unsafe.Pointer, slot int) string {
	var p *uint16
	if int32(comCall(obj, slot, uintptr(unsafe.Pointer(&p)))) < 0 || p == nil {
		return ""
	}
	s := windows.UTF16PtrToString(p)
	windows.CoTaskMemFree(unsafe.Pointer(p))
	return s
}

func comBool(obj unsafe.Pointer, slot int) bool {
	var v int32
	return int32(comCall(obj, slot, uintptr(unsafe.Pointer(&v)))) >= 0 && v != 0
}

// comHandler es un objeto COM mínimo con un solo método, Invoke(a, b): sirve para todos los
// handlers de WebView2 que usamos (los de eventos y los de "terminó de crearse"), que tienen
// esa misma forma. Se cuenta como que nunca se libera: lo mantiene vivo el motor (engine.keep).
type comHandler struct {
	vtbl *handlerVtbl
	fn   func(a, b uintptr) uintptr
}

type handlerVtbl struct{ queryInterface, addRef, release, invoke uintptr }

var (
	handlerOnce sync.Once
	handlerVT   *handlerVtbl // los callbacks de Windows no se pueden liberar: se crean una sola vez
)

func newHandler(fn func(a, b uintptr) uintptr) *comHandler {
	handlerOnce.Do(func() {
		ref := windows.NewCallback(handlerRef)
		handlerVT = &handlerVtbl{
			queryInterface: windows.NewCallback(handlerQueryInterface),
			addRef:         ref,
			release:        ref,
			invoke:         windows.NewCallback(handlerInvoke),
		}
	})
	return &comHandler{vtbl: handlerVT, fn: fn}
}

var iidIUnknown = windows.GUID{Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}

func handlerRef(this *comHandler) uintptr { return 1 }

func handlerQueryInterface(this *comHandler, riid *windows.GUID, out *unsafe.Pointer) uintptr {
	if *riid == iidIUnknown {
		*out = unsafe.Pointer(this)
		return 0
	}
	*out = nil
	return uintptr(windows.E_NOINTERFACE)
}

func handlerInvoke(this *comHandler, a, b uintptr) (r uintptr) {
	defer func() {
		if v := recover(); v != nil { // un pánico no puede cruzar a Windows
			slog.Error("pánico en un evento de WebView2", "panic", v)
			r = 0
		}
	}()
	return this.fn(a, b)
}

// ---------- Motor ----------

type tab struct {
	id      string
	hwnd    uintptr
	ctrl    *edge.ICoreWebView2Controller // nil hasta que WebView2 termina de crearse
	wv      *edge.ICoreWebView2
	b       Bounds
	scale   float64
	visible bool
}

func (t *tab) wvPtr() unsafe.Pointer   { return unsafe.Pointer(t.wv) }
func (t *tab) ctrlPtr() unsafe.Pointer { return unsafe.Pointer(t.ctrl) }

type winEngine struct {
	profileDir string
	emit       func(Event)

	threadID uint32
	qMu      sync.Mutex
	queue    []func()

	// Desde acá, solo lo toca el hilo del motor.
	tabs       map[string]*tab
	env        unsafe.Pointer // ICoreWebView2Environment, con una referencia nuestra
	envPending bool
	envWaiters []func(env unsafe.Pointer, err error)
	keep       []*comHandler // WebView2 puede llamar a un handler hasta que se destruye la vista
}

var _ Engine = (*winEngine)(nil)

// run es el hilo del motor: crea la cola de mensajes, avisa por ready y atiende mensajes.
func (e *winEngine) run(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// WebView2 necesita un hilo STA. S_FALSE (1) significa que ya estaba inicializado.
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err != nil && err != syscall.Errno(1) {
		ready <- fmt.Errorf("browser: CoInitializeEx: %w", err)
		return
	}
	defer windows.CoUninitialize()
	if err := registerViewClass(); err != nil {
		ready <- fmt.Errorf("browser: no se pudo registrar la clase de ventana: %w", err)
		return
	}

	var m msg
	procPeekMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, 0) // crea la cola de mensajes del hilo
	e.threadID = windows.GetCurrentThreadId()
	ready <- nil

	for {
		r, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		if m.message == wmApp {
			e.drain()
			continue
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// post deja fn en la cola del hilo del motor y lo despierta.
func (e *winEngine) post(fn func()) {
	e.qMu.Lock()
	e.queue = append(e.queue, fn)
	e.qMu.Unlock()
	procPostThreadMessage.Call(uintptr(e.threadID), wmApp, 0, 0)
}

func (e *winEngine) drain() {
	for {
		e.qMu.Lock()
		if len(e.queue) == 0 {
			e.qMu.Unlock()
			return
		}
		fn := e.queue[0]
		e.queue = e.queue[1:]
		e.qMu.Unlock()
		func() {
			defer func() {
				if v := recover(); v != nil {
					slog.Error("pánico en el motor del navegador", "panic", v)
				}
			}()
			fn()
		}()
	}
}

// call corre fn en el hilo del motor y espera su resultado. fn avisa con done, que puede
// llamarse más tarde (Open espera a que WebView2 termine de crearse).
func (e *winEngine) call(timeout time.Duration, fn func(done func(error))) error {
	ch := make(chan error, 1)
	e.post(func() { fn(func(err error) { ch <- err }) })
	select {
	case err := <-ch:
		return err
	case <-time.After(timeout):
		return errTimeout
	}
}

// callTab corre fn sobre la pestaña id ya creada, en el hilo del motor.
func (e *winEngine) callTab(id string, fn func(t *tab) error) error {
	return e.call(callTimeout, func(done func(error)) {
		t := e.tabs[id]
		switch {
		case t == nil:
			done(errNoTab)
		case t.ctrl == nil:
			done(errNotReady)
		default:
			done(fn(t))
		}
	})
}

func (e *winEngine) Open(id, url string, b Bounds, scale float64) error {
	return e.call(callTimeout, func(done func(error)) { e.open(id, url, b, scale, done) })
}

func (e *winEngine) open(id, url string, b Bounds, scale float64, done func(error)) {
	if t := e.tabs[id]; t != nil { // ya abierta: solo se acomoda
		t.b, t.scale = b, scale
		e.layout(t)
		done(nil)
		return
	}
	parent := winutil.MainWindow()
	if parent == 0 {
		done(errors.New("browser: no hay ventana principal"))
		return
	}
	hwnd, err := createViewWindow(parent)
	if err != nil {
		done(err)
		return
	}
	t := &tab{id: id, hwnd: hwnd, b: b, scale: scale, visible: true}
	e.tabs[id] = t
	fail := func(err error) {
		if e.tabs[id] == t {
			e.closeTab(t)
		}
		done(err)
	}
	e.withEnv(func(env unsafe.Pointer, err error) {
		if err != nil {
			fail(err)
			return
		}
		if e.tabs[id] != t { // la cerraron mientras arrancaba el entorno
			done(errClosed)
			return
		}
		h := newHandler(func(hr, ctrl uintptr) uintptr {
			switch {
			case e.tabs[id] != t: // la cerraron mientras se creaba
				if ctrl != 0 {
					comCall(ptr(ctrl), slotCtrlClose)
				}
				done(errClosed)
			case int32(hr) < 0 || ctrl == 0:
				fail(hrError("crear la vista", hr))
			default:
				if err := e.attach(t, ctrl, url); err != nil {
					fail(err)
				} else {
					done(nil)
				}
			}
			return 0
		})
		e.keep = append(e.keep, h)
		if err := hrError("CreateCoreWebView2Controller", comCall(env, slotEnvCreateController, hwnd, uintptr(unsafe.Pointer(h)))); err != nil {
			fail(err)
		}
	})
}

// withEnv entrega el entorno de WebView2 (con el perfil propio), creándolo la primera vez.
func (e *winEngine) withEnv(cb func(env unsafe.Pointer, err error)) {
	if e.env != nil {
		cb(e.env, nil)
		return
	}
	e.envWaiters = append(e.envWaiters, cb)
	if e.envPending {
		return
	}
	e.envPending = true
	err := webviewloader.CreateCoreWebView2EnvironmentWithOptions(envHandler{e}, webviewloader.WithUserDataFolder(e.profileDir))
	if err != nil && e.envPending { // si falló antes de avisar, avisamos nosotros
		e.envDone(nil, fmt.Errorf("browser: no se pudo crear el entorno de WebView2: %w", err))
	}
}

func (e *winEngine) envDone(env unsafe.Pointer, err error) {
	e.envPending = false
	if env != nil {
		comCall(env, 1) // AddRef: el que nos pasan solo vale durante el aviso
		e.env = env
	}
	waiters := e.envWaiters
	e.envWaiters = nil
	for _, w := range waiters {
		w(e.env, err)
	}
}

// envHandler recibe el entorno recién creado (en el hilo del motor, dentro del bucle de mensajes).
type envHandler struct{ e *winEngine }

func (h envHandler) EnvironmentCompleted(code webviewloader.HRESULT, env *webviewloader.ICoreWebView2Environment) webviewloader.HRESULT {
	if code < 0 || env == nil {
		h.e.envDone(nil, hrError("crear el entorno de WebView2", uintptr(uint32(code))))
	} else {
		h.e.envDone(unsafe.Pointer(env), nil)
	}
	return 0
}

// attach engancha eventos y ajustes al WebView2 recién creado y empieza a navegar.
func (e *winEngine) attach(t *tab, ctrlRaw uintptr, url string) error {
	ctrl := (*edge.ICoreWebView2Controller)(ptr(ctrlRaw))
	ctrl.AddRef() // el que nos pasan solo vale durante el aviso
	wv, err := ctrl.GetCoreWebView2()
	if err != nil {
		comCall(unsafe.Pointer(ctrl), slotCtrlClose)
		ctrl.Release()
		return fmt.Errorf("browser: GetCoreWebView2: %w", err)
	}
	t.ctrl, t.wv = ctrl, wv

	// La página no puede hablarle al host: sin mensajes ni objetos del host (no registramos ninguno).
	if s, err := wv.GetSettings(); err == nil {
		comCall(unsafe.Pointer(s), slotSettingsPutWebMessage, 0)
		comCall(unsafe.Pointer(s), slotSettingsPutHostObject, 0)
		s.Release()
	}

	snapshot := func(kind string) Event {
		return Event{
			ID: t.id, Kind: kind,
			URL:     comString(t.wvPtr(), slotWvGetSource),
			Title:   comString(t.wvPtr(), slotWvGetDocumentTitle),
			CanBack: comBool(t.wvPtr(), slotWvGetCanGoBack),
			CanFwd:  comBool(t.wvPtr(), slotWvGetCanGoForward),
		}
	}
	on := func(slot int, fn func(sender, args uintptr)) {
		h := newHandler(func(sender, args uintptr) uintptr {
			if e.tabs[t.id] == t { // ya cerrada: un evento atrasado no se manda
				fn(sender, args)
			}
			return 0
		})
		e.keep = append(e.keep, h)
		var token int64
		comCall(t.wvPtr(), slot, uintptr(unsafe.Pointer(h)), uintptr(unsafe.Pointer(&token)))
	}
	on(slotWvAddSourceChanged, func(_, _ uintptr) { e.emit(snapshot("nav")) })
	on(slotWvAddHistoryChanged, func(_, _ uintptr) { e.emit(snapshot("nav")) })
	on(slotWvAddTitleChanged, func(_, _ uintptr) { e.emit(snapshot("title")) })
	on(slotWvAddNavCompleted, func(_, args uintptr) {
		var status int32
		if !comBool(ptr(args), slotNavDoneIsSuccess) &&
			comCall(ptr(args), slotNavDoneErrStatus, uintptr(unsafe.Pointer(&status))) == 0 &&
			status != webErrorOperationCanceled {
			e.emit(snapshot("error"))
		}
	})
	on(slotWvAddNewWindow, func(_, args uintptr) {
		comCall(ptr(args), slotNewWinPutHandled, 1) // no se abre nada: lo resuelve la pestaña
		ev := snapshot("newWindow")
		ev.URL = comString(ptr(args), slotNewWinGetURI)
		e.emit(ev)
	})

	e.layout(t)
	return hrError("Navigate", comCallString(t.wvPtr(), slotWvNavigate, url))
}

// comCallString llama a un método COM que recibe un solo LPCWSTR.
func comCallString(obj unsafe.Pointer, slot int, s string) uintptr {
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return uintptr(windows.E_INVALIDARG)
	}
	return comCall(obj, slot, uintptr(unsafe.Pointer(p)))
}

// layout acomoda la ventana hija y la vista según t.b, t.scale y t.visible.
func (e *winEngine) layout(t *tab) {
	p := t.b.Physical(t.scale)
	flags := uintptr(swpNoActivate | swpHideWindow)
	if t.visible {
		flags = swpNoActivate | swpShowWindow
	}
	// HWND_TOP: tiene que quedar por encima de la vista de Wails.
	procSetWindowPos.Call(t.hwnd, 0, uintptr(p.X), uintptr(p.Y), uintptr(p.W), uintptr(p.H), flags)
	if t.ctrl == nil {
		return
	}
	t.ctrl.PutBounds(edge.Rect{Left: 0, Top: 0, Right: int32(p.W), Bottom: int32(p.H)})
	t.ctrl.PutIsVisible(t.visible)
	comCall(t.ctrlPtr(), slotCtrlNotifyMoved)
}

func (e *winEngine) closeTab(t *tab) {
	delete(e.tabs, t.id)
	if t.ctrl != nil {
		comCall(t.ctrlPtr(), slotCtrlClose)
		t.wv.Release()
		t.ctrl.Release()
		t.ctrl, t.wv = nil, nil
	}
	procDestroyWindow.Call(t.hwnd) // si osat ya se estaba cerrando, Windows ya la destruyó: falla sin problema
}

func (e *winEngine) Navigate(id, url string) error {
	return e.callTab(id, func(t *tab) error {
		return hrError("Navigate", comCallString(t.wvPtr(), slotWvNavigate, url))
	})
}

func (e *winEngine) Back(id string) error {
	return e.callTab(id, func(t *tab) error { return hrError("GoBack", comCall(t.wvPtr(), slotWvGoBack)) })
}

func (e *winEngine) Forward(id string) error {
	return e.callTab(id, func(t *tab) error { return hrError("GoForward", comCall(t.wvPtr(), slotWvGoForward)) })
}

func (e *winEngine) Reload(id string) error {
	return e.callTab(id, func(t *tab) error { return hrError("Reload", comCall(t.wvPtr(), slotWvReload)) })
}

func (e *winEngine) SetBounds(id string, b Bounds, scale float64) error {
	e.post(func() {
		if t := e.tabs[id]; t != nil {
			t.b, t.scale = b, scale
			e.layout(t)
		}
	})
	return nil
}

func (e *winEngine) SetVisible(id string, visible bool) error {
	e.post(func() {
		if t := e.tabs[id]; t != nil {
			t.visible = visible
			e.layout(t)
		}
	})
	return nil
}

func (e *winEngine) Close(id string) {
	e.post(func() {
		if t := e.tabs[id]; t != nil {
			e.closeTab(t)
		}
	})
}

// CloseAll cierra todas las vistas y suelta el entorno, así WebView2 puede terminar su proceso
// y guardar las cookies. Espera un rato corto: si el hilo no contesta, el cierre sigue igual.
func (e *winEngine) CloseAll() {
	err := e.call(closeAllTimeout, func(done func(error)) {
		for _, t := range e.tabs {
			e.closeTab(t)
		}
		if e.env != nil {
			comCall(e.env, slotRelease)
			e.env = nil
		}
		done(nil)
	})
	if err != nil {
		slog.Warn("el navegador nativo no terminó de cerrarse", "err", err)
	}
}

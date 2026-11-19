//go:build windows

package rdpwin

// El control de Escritorio remoto (MsRdpClient, de mstscax.dll) es un ActiveX. Lo aloja la
// clase de ventana AtlAxWin de atl.dll (viene con Windows): una ventana hija de la ventana
// principal de osat, que crea el control y le hace de contenedor. Se maneja por IDispatch
// (los nombres de las propiedades son los de la documentación de MsRdpClient) y sus eventos
// entran por un IDispatch propio (sink) conectado a IMsTscAxEvents.
//
// Como en internal/browser, todo corre en un hilo propio (STA, con OleInitialize: el
// portapapeles de mstsc usa el de OLE) con su bucle de mensajes. El resto de osat deja
// funciones en una cola y despierta al hilo.

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"

	ole "github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows"

	"osat/internal/browser"
	"osat/internal/winutil"
)

// Supported dice si este sistema tiene el cliente de Escritorio remoto de Windows.
func Supported() bool { return true }

// New arranca el hilo del motor. parent devuelve la ventana dentro de la que van los
// escritorios (nil = la principal de osat; en las pruebas, una propia). Falla si el control
// de Escritorio remoto no está registrado.
func New(parent func() uintptr, emit func(Event)) (Engine, error) {
	if parent == nil {
		parent = winutil.MainWindow
	}
	if emit == nil {
		emit = func(Event) {}
	}
	e := &winEngine{parent: parent, emit: emit, tabs: map[string]*tab{}}
	ready := make(chan error, 1)
	go e.run(ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	return e, nil
}

const (
	callTimeout     = 10 * time.Second
	closeAllTimeout = 3 * time.Second
	resizeDelay     = 400 * time.Millisecond // espera al dejar de cambiar el tamaño antes de pedírselo al servidor
)

var errTimeout = errors.New("rdpwin: el motor no respondió a tiempo")

// ---------- Win32 ----------

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	ole32    = windows.NewLazySystemDLL("ole32.dll")
	atl      = windows.NewLazySystemDLL("atl.dll")

	procCreateWindowEx    = user32.NewProc("CreateWindowExW")
	procDestroyWindow     = user32.NewProc("DestroyWindow")
	procSetWindowPos      = user32.NewProc("SetWindowPos")
	procSetFocus          = user32.NewProc("SetFocus")
	procGetWindow         = user32.NewProc("GetWindow")
	procGetMessage        = user32.NewProc("GetMessageW")
	procPeekMessage       = user32.NewProc("PeekMessageW")
	procTranslateMessage  = user32.NewProc("TranslateMessage")
	procDispatchMessage   = user32.NewProc("DispatchMessageW")
	procPostThreadMessage = user32.NewProc("PostThreadMessageW")
	procGetModuleHandle   = kernel32.NewProc("GetModuleHandleW")
	procOleInitialize     = ole32.NewProc("OleInitialize")
	procOleUninitialize   = ole32.NewProc("OleUninitialize")
	procAtlAxWinInit      = atl.NewProc("AtlAxWinInit")
	procAtlAxGetControl   = atl.NewProc("AtlAxGetControl")
)

const (
	wmApp = 0x8000

	wsChild        = 0x40000000
	wsClipChildren = 0x02000000
	wsClipSiblings = 0x04000000
	wsTabStop      = 0x00010000

	swpNoActivate = 0x0010
	swpShowWindow = 0x0040
	swpHideWindow = 0x0080

	gwChild = 5
)

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	ptX     int32
	ptY     int32
}

// ---------- COM ----------

var (
	// Eventos del control (dispinterface IMsTscAxEvents).
	diidTscAxEvents = ole.NewGUID("{336D5562-EFA8-482E-8CB3-C5C0FC7A7DB6}")
	// IMsTscAx: la interfaz base del cliente (para reconocerlo entre los MsTscAx.MsTscAx.N).
	iidTscAx = ole.NewGUID("{8C11EFAE-92C3-11D1-BC1E-00C04FA31489}")
	// IMsRdpExtendedSettings: propiedades por nombre (la escala del escritorio).
	iidExtendedSettings = ole.NewGUID("{302D8188-0052-4807-806A-362B628F9AC5}")
)

// Números de los eventos de IMsTscAxEvents.
const (
	dispOnConnected     = 2
	dispOnLoginComplete = 3
	dispOnDisconnected  = 4
)

// findCLSID busca el control más nuevo registrado (MsTscAx.MsTscAx.N: cada versión de
// Windows agrega una con más propiedades). No todas son el cliente completo (en Windows 11,
// la 13 es otro objeto): se crea cada una en una ventana oculta y vale la que tiene IMsTscAx.
// Corre en el hilo del motor.
func findCLSID() (string, error) {
	inst, _, _ := procGetModuleHandle.Call(0)
	class, _ := windows.UTF16PtrFromString("AtlAxWin")
	for n := 20; n >= 1; n-- {
		g, err := ole.CLSIDFromProgID("MsTscAx.MsTscAx." + strconv.Itoa(n))
		if err != nil {
			continue
		}
		name, _ := windows.UTF16PtrFromString(g.String())
		hwnd, _, _ := procCreateWindowEx.Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(name)), 0, 0, 0, 1, 1, 0, 0, inst, 0)
		if hwnd == 0 {
			continue
		}
		ok := false
		var unk *ole.IUnknown
		if r, _, _ := procAtlAxGetControl.Call(hwnd, uintptr(unsafe.Pointer(&unk))); int32(r) >= 0 && unk != nil {
			if tsc, err := unk.QueryInterface(iidTscAx); err == nil {
				tsc.Release()
				ok = true
			}
			unk.Release()
		}
		procDestroyWindow.Call(hwnd)
		if ok {
			return g.String(), nil
		}
	}
	return "", errors.New("rdpwin: no está registrado el control de Escritorio remoto (mstscax.dll)")
}

// sink recibe los eventos de un control: un IDispatch mínimo que solo atiende Invoke.
// Lo mantiene vivo su pestaña (los callbacks de Windows no se pueden liberar: la vtable
// se crea una sola vez).
type sink struct {
	vtbl *sinkVtbl
	fn   func(dispid int32, args []ole.VARIANT)
}

type sinkVtbl struct {
	queryInterface, addRef, release              uintptr
	getTypeInfoCount, getTypeInfo, getIDsOfNames uintptr
	invoke                                       uintptr
}

// dispParams es DISPPARAMS: los argumentos llegan al revés (el último primero).
type dispParams struct {
	args      *ole.VARIANT
	named     *int32
	nArgs     uint32
	nNamedArg uint32
}

var (
	sinkOnce sync.Once
	sinkVT   *sinkVtbl
)

func newSink(fn func(dispid int32, args []ole.VARIANT)) *sink {
	sinkOnce.Do(func() {
		ref := windows.NewCallback(func(this *sink) uintptr { return 1 })
		notImpl := windows.NewCallback(func(this *sink, a, b, c, d, e uintptr) uintptr { return ole.E_NOTIMPL })
		sinkVT = &sinkVtbl{
			queryInterface: windows.NewCallback(sinkQueryInterface),
			addRef:         ref,
			release:        ref,
			getTypeInfoCount: windows.NewCallback(func(this *sink, n *uint32) uintptr {
				*n = 0
				return 0
			}),
			getTypeInfo:   notImpl,
			getIDsOfNames: notImpl,
			invoke:        windows.NewCallback(sinkInvoke),
		}
	})
	return &sink{vtbl: sinkVT, fn: fn}
}

func sinkQueryInterface(this *sink, riid *ole.GUID, out *unsafe.Pointer) uintptr {
	if ole.IsEqualGUID(riid, ole.IID_IUnknown) || ole.IsEqualGUID(riid, ole.IID_IDispatch) || ole.IsEqualGUID(riid, diidTscAxEvents) {
		*out = unsafe.Pointer(this)
		return 0
	}
	*out = nil
	return ole.E_NOINTERFACE
}

func sinkInvoke(this *sink, dispid int32, riid, lcid, flags uintptr, params *dispParams, result, excep, argErr uintptr) (r uintptr) {
	defer func() {
		if v := recover(); v != nil { // un pánico no puede cruzar a Windows
			slog.Error("pánico en un evento del escritorio remoto", "panic", v)
			r = 0
		}
	}()
	var args []ole.VARIANT
	if params != nil && params.nArgs > 0 && params.args != nil {
		args = unsafe.Slice(params.args, params.nArgs)
	}
	this.fn(dispid, args)
	return 0
}

// put pone una propiedad y devuelve el error (para registrarlo; casi ninguno es grave).
func put(d *ole.IDispatch, name string, v interface{}) error {
	r, err := oleutil.PutProperty(d, name, v)
	if r != nil {
		r.Clear()
	}
	return err
}

// sub devuelve el primer objeto de configuración que exista entre names (AdvancedSettings9,
// AdvancedSettings8…: depende de la versión del control). El que llama lo libera.
func sub(d *ole.IDispatch, names ...string) *ole.IDispatch {
	for _, n := range names {
		if v, err := oleutil.GetProperty(d, n); err == nil {
			if o := v.ToIDispatch(); o != nil {
				return o
			}
		}
	}
	return nil
}

// ---------- Motor ----------

type tab struct {
	id        string
	hwnd      uintptr
	ctl       *ole.IDispatch
	cp        *ole.IConnectionPoint
	cookie    uint32
	sink      *sink
	drop      *dropTarget // para soltar archivos (drop_windows.go)
	tip       uintptr     // tooltip de los avisos (notice_windows.go)
	tipTimer  *time.Timer
	b         browser.Bounds
	scale     float64
	visible   bool
	connected bool
	sent      [2]int // último tamaño del escritorio pedido al servidor
	resize    *time.Timer
}

type winEngine struct {
	parent func() uintptr
	emit   func(Event)
	clsid  string

	threadID uint32
	qMu      sync.Mutex
	queue    []func()

	tabs map[string]*tab // solo lo toca el hilo del motor
}

var _ Engine = (*winEngine)(nil)

func (e *winEngine) run(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// OleInitialize (no solo CoInitialize): el control usa el portapapeles y el arrastre de OLE.
	if r, _, _ := procOleInitialize.Call(0); int32(r) < 0 {
		ready <- fmt.Errorf("rdpwin: OleInitialize: 0x%08x", uint32(r))
		return
	}
	defer procOleUninitialize.Call()
	if err := procAtlAxWinInit.Find(); err != nil {
		ready <- fmt.Errorf("rdpwin: no se encontró atl.dll: %w", err)
		return
	}
	if r, _, _ := procAtlAxWinInit.Call(); r == 0 {
		ready <- errors.New("rdpwin: AtlAxWinInit falló")
		return
	}
	initTooltips()
	clsid, err := findCLSID()
	if err != nil {
		ready <- err
		return
	}
	e.clsid = clsid

	var m msg
	procPeekMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, 0) // crea la cola de mensajes del hilo
	e.threadID = windows.GetCurrentThreadId()
	ready <- nil

	for {
		r, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		if m.message == wmApp && m.hwnd == 0 {
			e.drain()
			continue
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}

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
					slog.Error("pánico en el motor del escritorio remoto", "panic", v)
				}
			}()
			fn()
		}()
	}
}

func (e *winEngine) call(timeout time.Duration, fn func() error) error {
	ch := make(chan error, 1)
	e.post(func() { ch <- fn() })
	select {
	case err := <-ch:
		return err
	case <-time.After(timeout):
		return errTimeout
	}
}

func (e *winEngine) Open(id string, cfg Config, b browser.Bounds, scale float64) error {
	return e.call(callTimeout, func() error { return e.open(id, cfg, b, scale) })
}

func (e *winEngine) open(id string, cfg Config, b browser.Bounds, scale float64) error {
	if old := e.tabs[id]; old != nil { // reconectar: uno nuevo en su lugar
		e.closeTab(old)
	}
	parent := e.parent()
	if parent == 0 {
		return errors.New("rdpwin: no hay ventana principal")
	}
	inst, _, _ := procGetModuleHandle.Call(0)
	class, _ := windows.UTF16PtrFromString("AtlAxWin")
	name, _ := windows.UTF16PtrFromString(e.clsid)
	p := b.Physical(scale)
	hwnd, _, err := procCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(name)),
		wsChild|wsClipChildren|wsClipSiblings|wsTabStop,
		uintptr(p.X), uintptr(p.Y), uintptr(max(p.W, 1)), uintptr(max(p.H, 1)),
		parent, 0, inst, 0)
	if hwnd == 0 {
		return fmt.Errorf("rdpwin: no se pudo crear el control: %w", err)
	}
	var unk *ole.IUnknown
	if r, _, _ := procAtlAxGetControl.Call(hwnd, uintptr(unsafe.Pointer(&unk))); int32(r) < 0 || unk == nil {
		procDestroyWindow.Call(hwnd)
		return fmt.Errorf("rdpwin: el control no se creó (0x%08x)", uint32(r))
	}
	ctl, err := unk.QueryInterface(ole.IID_IDispatch)
	unk.Release()
	if err != nil {
		procDestroyWindow.Call(hwnd)
		return fmt.Errorf("rdpwin: el control no tiene IDispatch: %w", err)
	}
	t := &tab{id: id, hwnd: hwnd, ctl: ctl, b: b, scale: scale}
	e.tabs[id] = t
	e.advise(t)
	e.registerDrop(t)
	if err := e.configure(t, cfg); err != nil {
		e.closeTab(t)
		return err
	}
	e.layout(t)
	if _, err := oleutil.CallMethod(ctl, "Connect"); err != nil {
		e.closeTab(t)
		return fmt.Errorf("rdpwin: Connect: %w", err)
	}
	return nil
}

// advise conecta el sink de eventos al control.
func (e *winEngine) advise(t *tab) {
	id := t.id
	t.sink = newSink(func(dispid int32, args []ole.VARIANT) { e.onEvent(id, dispid, args) })
	cpcUnk, err := t.ctl.QueryInterface(ole.IID_IConnectionPointContainer)
	if err != nil {
		slog.Warn("escritorio remoto: sin eventos del control", "err", err)
		return
	}
	cpc := (*ole.IConnectionPointContainer)(unsafe.Pointer(cpcUnk))
	defer cpc.Release()
	var cp *ole.IConnectionPoint
	if err := cpc.FindConnectionPoint(diidTscAxEvents, &cp); err != nil {
		slog.Warn("escritorio remoto: sin eventos del control", "err", err)
		return
	}
	cookie, err := cp.Advise((*ole.IUnknown)(unsafe.Pointer(t.sink)))
	if err != nil {
		cp.Release()
		slog.Warn("escritorio remoto: sin eventos del control", "err", err)
		return
	}
	t.cp, t.cookie = cp, cookie
}

func (e *winEngine) onEvent(id string, dispid int32, args []ole.VARIANT) {
	t := e.tabs[id]
	if t == nil {
		return
	}
	switch dispid {
	case dispOnConnected:
		t.connected = true
		e.emit(Event{ID: id, Kind: "connected"})
	case dispOnLoginComplete:
		e.emit(Event{ID: id, Kind: "loginComplete"})
		e.fitDesktop(t) // la pestaña pudo cambiar de tamaño mientras se conectaba
	case dispOnDisconnected:
		t.connected = false
		reason := 0
		if len(args) > 0 {
			reason = int(int32(args[0].Val))
		}
		// Fuera del evento: el control no espera que se lo llame de vuelta mientras avisa.
		e.post(func() {
			if e.tabs[id] != t {
				return
			}
			e.emit(Event{ID: id, Kind: "disconnected", Reason: reason, Text: e.describe(t, reason)})
		})
	}
}

// describe pide al control el texto de un motivo de desconexión (en el idioma de Windows).
func (e *winEngine) describe(t *tab, reason int) string {
	ext := int64(0)
	if v, err := oleutil.GetProperty(t.ctl, "ExtendedDisconnectReason"); err == nil {
		ext = v.Val
	}
	v, err := oleutil.CallMethod(t.ctl, "GetErrorDescription", uint32(reason), uint32(ext))
	if err != nil {
		return ""
	}
	defer v.Clear()
	s, _ := v.Value().(string)
	return s
}

// configure carga todo lo de la conexión antes de Connect.
func (e *winEngine) configure(t *tab, c Config) error {
	if err := put(t.ctl, "Server", c.Server); err != nil {
		return fmt.Errorf("rdpwin: Server: %w", err)
	}
	logPut := func(name string, err error) {
		if err != nil {
			slog.Debug("escritorio remoto: propiedad no soportada", "prop", name, "err", err)
		}
	}
	logPut("UserName", put(t.ctl, "UserName", c.User))
	logPut("Domain", put(t.ctl, "Domain", c.Domain))
	w, h := desktopSize(t.b.Physical(t.scale))
	logPut("DesktopWidth", put(t.ctl, "DesktopWidth", int32(w)))
	logPut("DesktopHeight", put(t.ctl, "DesktopHeight", int32(h)))
	logPut("ColorDepth", put(t.ctl, "ColorDepth", int32(32)))
	t.sent = [2]int{w, h}

	adv := sub(t.ctl, "AdvancedSettings9", "AdvancedSettings8", "AdvancedSettings7", "AdvancedSettings6", "AdvancedSettings5", "AdvancedSettings4", "AdvancedSettings3", "AdvancedSettings2")
	if adv == nil {
		return errors.New("rdpwin: el control no tiene AdvancedSettings")
	}
	defer adv.Release()
	if c.Port > 0 {
		logPut("RDPPort", put(adv, "RDPPort", int32(c.Port)))
	}
	if err := put(adv, "ClearTextPassword", c.Password); err != nil {
		return fmt.Errorf("rdpwin: no se pudo pasar la contraseña: %w", err)
	}
	logPut("EnableCredSspSupport", put(adv, "EnableCredSspSupport", true))   // NLA
	logPut("AuthenticationLevel", put(adv, "AuthenticationLevel", int32(2))) // avisar si el certificado no es de confianza
	logPut("RedirectClipboard", put(adv, "RedirectClipboard", true))         // texto, imágenes y archivos
	logPut("SmartSizing", put(adv, "SmartSizing", true))                     // si el servidor no acepta el tamaño nuevo, se escala
	logPut("EnableAutoReconnect", put(adv, "EnableAutoReconnect", true))

	// Con el foco en el escritorio, las combinaciones (Alt+Tab, Win…) van al servidor, como en IronRDP.
	if sec := sub(t.ctl, "SecuredSettings3", "SecuredSettings2"); sec != nil {
		logPut("KeyboardHookMode", put(sec, "KeyboardHookMode", int32(1)))
		sec.Release()
	}

	if c.Gateway != "" {
		ts := sub(t.ctl, "TransportSettings4", "TransportSettings3", "TransportSettings2")
		if ts == nil {
			return errors.New("rdpwin: el control no soporta RD Gateway")
		}
		defer ts.Release()
		logPut("GatewayHostname", put(ts, "GatewayHostname", c.Gateway))
		logPut("GatewayUsageMethod", put(ts, "GatewayUsageMethod", int32(1)))               // usar siempre el gateway
		logPut("GatewayProfileUsageMethod", put(ts, "GatewayProfileUsageMethod", int32(1))) // con esta configuración
		logPut("GatewayCredsSource", put(ts, "GatewayCredsSource", int32(0)))               // usuario y contraseña
		logPut("GatewayCredSharing", put(ts, "GatewayCredSharing", int32(1)))               // las mismas que el equipo
		logPut("GatewayUsername", put(ts, "GatewayUsername", c.User))
		logPut("GatewayDomain", put(ts, "GatewayDomain", c.Domain))
		logPut("GatewayPassword", put(ts, "GatewayPassword", c.Password))
	}

	ds, dev := scaleFactors(t.scale)
	e.extended(t, "DesktopScaleFactor", ds)
	e.extended(t, "DeviceScaleFactor", dev)
	return nil
}

// extended pone una propiedad de IMsRdpExtendedSettings (no está en IDispatch: va por la vtable).
func (e *winEngine) extended(t *tab, name string, v uint32) {
	unk, err := t.ctl.QueryInterface(iidExtendedSettings)
	if err != nil {
		return
	}
	defer unk.Release()
	bstr := ole.SysAllocString(name)
	defer ole.SysFreeString(bstr)
	val := ole.NewVariant(ole.VT_UI4, int64(v))
	vtbl := *(*unsafe.Pointer)(unsafe.Pointer(unk))
	putProperty := *(*uintptr)(unsafe.Add(vtbl, 3*unsafe.Sizeof(uintptr(0))))
	if r, _, _ := syscall.SyscallN(putProperty, uintptr(unsafe.Pointer(unk)), uintptr(unsafe.Pointer(bstr)), uintptr(unsafe.Pointer(&val))); int32(r) < 0 {
		slog.Debug("escritorio remoto: propiedad no soportada", "prop", name, "hr", fmt.Sprintf("0x%08x", uint32(r)))
	}
}

// desktopSize: el tamaño del escritorio remoto para una pestaña de p píxeles físicos.
// Windows pide entre 200 y 8192 y ancho par; el ancho va múltiplo de 4 porque con otros
// servidores (xrdp) un ancho que no lo es dibuja la imagen torcida (cada fila corrida).
// Lo que sobra de la pestaña (hasta 3 píxeles) lo cubre SmartSizing.
func desktopSize(p browser.Bounds) (w, h int) {
	clamp := func(v int) int { return min(8192, max(200, v)) }
	return clamp(p.W) &^ 3, clamp(p.H)
}

// scaleFactors: la escala del escritorio remoto (100 a 500 %) y la del dispositivo (100,
// 140 o 180, los únicos que acepta) para el devicePixelRatio de osat.
func scaleFactors(scale float64) (desktop, device uint32) {
	if scale <= 0 {
		scale = 1
	}
	desktop = uint32(min(500, max(100, math.Round(scale*100))))
	switch {
	case scale < 1.25:
		device = 100
	case scale < 1.75:
		device = 140
	default:
		device = 180
	}
	return desktop, device
}

// layout acomoda la ventana del control según t.b, t.scale y t.visible.
func (e *winEngine) layout(t *tab) {
	p := t.b.Physical(t.scale)
	flags := uintptr(swpNoActivate | swpHideWindow)
	if t.visible && p.W > 0 && p.H > 0 {
		flags = swpNoActivate | swpShowWindow
	}
	// HWND_TOP (0): por encima de la vista de Wails.
	procSetWindowPos.Call(t.hwnd, 0, uintptr(p.X), uintptr(p.Y), uintptr(max(p.W, 1)), uintptr(max(p.H, 1)), flags)
}

// fitDesktop le pide al servidor que el escritorio tenga el tamaño de la pestaña (Windows 8.1
// o más nuevo; si no lo acepta, SmartSizing lo escala).
func (e *winEngine) fitDesktop(t *tab) {
	if !t.connected {
		return
	}
	p := t.b.Physical(t.scale)
	if p.W <= 0 || p.H <= 0 {
		return // pestaña oculta
	}
	w, h := desktopSize(p)
	if [2]int{w, h} == t.sent {
		return
	}
	ds, dev := scaleFactors(t.scale)
	if _, err := oleutil.CallMethod(t.ctl, "UpdateSessionDisplaySettings", uint32(w), uint32(h), uint32(0), uint32(0), uint32(0), ds, dev); err != nil {
		slog.Debug("escritorio remoto: el servidor no cambió el tamaño", "err", err)
		return
	}
	t.sent = [2]int{w, h}
}

func (e *winEngine) closeTab(t *tab) {
	delete(e.tabs, t.id)
	if t.resize != nil {
		t.resize.Stop()
	}
	if t.connected {
		oleutil.CallMethod(t.ctl, "Disconnect")
	}
	if t.cp != nil {
		t.cp.Unadvise(t.cookie)
		t.cp.Release()
	}
	e.revokeDrop(t)
	e.closeNotice(t)
	t.ctl.Release()
	procDestroyWindow.Call(t.hwnd) // destruye el control (AtlAxWin suelta su referencia)
}

func (e *winEngine) SetBounds(id string, b browser.Bounds, scale float64) {
	e.post(func() {
		t := e.tabs[id]
		if t == nil {
			return
		}
		t.b, t.scale = b, scale
		e.layout(t)
		if t.resize != nil {
			t.resize.Stop()
		}
		t.resize = time.AfterFunc(resizeDelay, func() {
			e.post(func() {
				if e.tabs[id] == t {
					e.fitDesktop(t)
				}
			})
		})
	})
}

func (e *winEngine) SetVisible(id string, visible bool) {
	e.post(func() {
		if t := e.tabs[id]; t != nil {
			t.visible = visible
			e.layout(t)
			if !visible {
				e.hideNotice(t)
			}
		}
	})
}

// Focus pasa el teclado a la ventana de entrada del control (la hija más profunda).
func (e *winEngine) Focus(id string) {
	e.post(func() {
		t := e.tabs[id]
		if t == nil {
			return
		}
		h := t.hwnd
		for {
			c, _, _ := procGetWindow.Call(h, gwChild)
			if c == 0 {
				break
			}
			h = c
		}
		procSetFocus.Call(h)
	})
}

func (e *winEngine) Close(id string) {
	e.post(func() {
		if t := e.tabs[id]; t != nil {
			e.closeTab(t)
		}
	})
}

func (e *winEngine) CloseAll() {
	err := e.call(closeAllTimeout, func() error {
		for _, t := range e.tabs {
			e.closeTab(t)
		}
		return nil
	})
	if err != nil {
		slog.Warn("el escritorio remoto no terminó de cerrarse", "err", err)
	}
}

//go:build windows

package applock

import (
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"golang.org/x/sys/windows"

	"osat/internal/winutil"
)

// Windows Hello con WinRT: Windows.Security.Credentials.UI.UserConsentVerifier. Las interfaces
// se llaman por la vtable a mano (go-ole solo trae la activación de WinRT) y las operaciones
// asíncronas se esperan consultando IAsyncInfo, sin armar un delegado de finalización.

const (
	helloClass = "Windows.Security.Credentials.UI.UserConsentVerifier"

	roInitMultithreaded = 1
	sFalse              = 1 // RoInitialize: el hilo ya estaba inicializado igual

	// Estados de IAsyncInfo.
	asyncStarted   = 0
	asyncCompleted = 1

	// Valores de los enums de WinRT que nos importan.
	availabilityAvailable = 0 // UserConsentVerifierAvailability.Available
	resultVerified        = 0 // UserConsentVerificationResult.Verified

	// Posición de cada método en su vtable (después de los 6 de IUnknown + IInspectable).
	checkAvailabilityIdx = 6 // IUserConsentVerifierStatics.CheckAvailabilityAsync
	requestForWindowIdx  = 6 // IUserConsentVerifierInterop.RequestVerificationForWindowAsync
	asyncStatusIdx       = 7 // IAsyncInfo.Status (Id, Status, ErrorCode, Cancel, Close)
	asyncCancelIdx       = 9 // IAsyncInfo.Cancel
	getResultsIdx        = 8 // IAsyncOperation<T>.GetResults (put_Completed, get_Completed, GetResults)

	helloPollEvery   = 50 * time.Millisecond
	availabilityWait = 5 * time.Second
	verificationWait = 2 * time.Minute
)

var (
	iidInterop   = ole.NewGUID("{39E050C3-4E74-441A-8DC0-B81104DF949C}") // IUserConsentVerifierInterop
	iidStatics   = ole.NewGUID("{AF4F3F91-564C-4DDC-B8B5-973447627C65}") // IUserConsentVerifierStatics
	iidAsyncInfo = ole.NewGUID("{00000036-0000-0000-C000-000000000046}") // IAsyncInfo

	// IAsyncOperation<UserConsentVerificationResult>.
	iidAsyncResult = pinterfaceIID("9fc2b0bb-e446-44e2-aa61-9cab8f636af2",
		"enum(Windows.Security.Credentials.UI.UserConsentVerificationResult;i4)")

	procRoUninitialize = windows.NewLazySystemDLL("combase.dll").NewProc("RoUninitialize")

	user32               = windows.NewLazySystemDLL("user32.dll")
	procRegisterClassEx  = user32.NewProc("RegisterClassExW")
	procCreateWindowEx   = user32.NewProc("CreateWindowExW")
	procDestroyWindow    = user32.NewProc("DestroyWindow")
	procDefWindowProc    = user32.NewProc("DefWindowProcW")
	procGetWindowRect    = user32.NewProc("GetWindowRect")
	procSetLayeredAttrs  = user32.NewProc("SetLayeredWindowAttributes")
	procShowWindow       = user32.NewProc("ShowWindow")
	procSetForeground    = user32.NewProc("SetForegroundWindow")
	procAllowForeground  = user32.NewProc("AllowSetForegroundWindow")
	procAttachInput      = user32.NewProc("AttachThreadInput")
	procGetForeground    = user32.NewProc("GetForegroundWindow")
	procWindowPID        = user32.NewProc("GetWindowThreadProcessId")
	procPeekMessage      = user32.NewProc("PeekMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessage  = user32.NewProc("DispatchMessageW")
	procGetModuleHandle  = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW")

	ownerClassOnce sync.Once
	ownerClassErr  error
)

// Ventana dueña del diálogo de Hello. Mientras el diálogo está abierto, Windows deshabilita a su
// dueña; si fuera la ventana de osat, Wails le pide el foco a WebView2 estando deshabilitada,
// MoveFocus falla con E_INVALIDARG y go-webview2 cierra osat (os.Exit). Por eso la dueña es una
// ventana propia, invisible y centrada sobre la de osat.
const (
	ownerClass = "osatHelloOwner"

	wsPopup         = 0x80000000
	wsExToolWindow  = 0x00000080
	wsExLayered     = 0x00080000
	lwaAlpha        = 0x2
	swShow          = 5
	pmRemove        = 1
	msgSize         = 48 // sizeof(MSG) en 64 bits
	ownerPumpEvery  = 10 * time.Millisecond
	ownerHalfExtent = 1 // la ventana mide 2×2 píxeles
)

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

func registerOwnerClass() error {
	ownerClassOnce.Do(func() {
		name, _ := windows.UTF16PtrFromString(ownerClass)
		inst, _, _ := procGetModuleHandle.Call(0)
		wc := wndClassEx{wndProc: procDefWindowProc.Addr(), instance: inst, className: name}
		wc.size = uint32(unsafe.Sizeof(wc))
		if r, _, err := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
			ownerClassErr = err
		}
	})
	return ownerClassErr
}

// ownerWindow crea en el hilo actual la ventana dueña del diálogo, centrada sobre main.
// Hay que destruirla en el mismo hilo y bombear sus mensajes mientras exista (pumpFor).
func ownerWindow(main uintptr) (uintptr, error) {
	if err := registerOwnerClass(); err != nil {
		return 0, err
	}
	var r struct{ left, top, right, bottom int32 }
	procGetWindowRect.Call(main, uintptr(unsafe.Pointer(&r)))
	cx, cy := (r.left+r.right)/2, (r.top+r.bottom)/2
	name, _ := windows.UTF16PtrFromString(ownerClass)
	inst, _, _ := procGetModuleHandle.Call(0)
	h, _, err := procCreateWindowEx.Call(wsExToolWindow|wsExLayered, uintptr(unsafe.Pointer(name)), 0, wsPopup,
		uintptr(cx-ownerHalfExtent), uintptr(cy-ownerHalfExtent), 2*ownerHalfExtent, 2*ownerHalfExtent, 0, 0, inst, 0)
	if h == 0 {
		return 0, err
	}
	procSetLayeredAttrs.Call(h, 0, 0, lwaAlpha) // totalmente transparente
	procShowWindow.Call(h, swShow)
	// Este hilo no es el de la interfaz de osat, que es el que tiene la entrada del usuario: sin compartirla,
	// Windows no le da el primer plano (la ventana solo titila en la barra de tareas) y el diálogo de Hello
	// queda detrás. Es lo mismo que hace el arrastre de archivos (internal/osdrag).
	ui, me := uintptr(winutil.WindowThread(main)), uintptr(windows.GetCurrentThreadId())
	if ui != 0 && ui != me {
		procAttachInput.Call(me, ui, 1)
		defer procAttachInput.Call(me, ui, 0)
	}
	ok, _, _ := procSetForeground.Call(h)
	slog.Info("windows hello: ventana dueña", "primer_plano", ok != 0)
	return h, nil
}

// pumpFor atiende los mensajes de las ventanas de este hilo durante d.
func pumpFor(d time.Duration) {
	var m [msgSize]byte
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(ownerPumpEvery) {
		for {
			if r, _, _ := procPeekMessage.Call(uintptr(unsafe.Pointer(&m[0])), 0, 0, 0, pmRemove); r == 0 {
				break
			}
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&m[0])))
			procDispatchMessage.Call(uintptr(unsafe.Pointer(&m[0])))
		}
	}
}

// pinterfaceIID calcula el IID de una interfaz genérica de WinRT con un parámetro de tipo:
// SHA-1 de (namespace de WinRT + firma), con los bits de versión 5 y de variante de un UUID.
func pinterfaceIID(generic, arg string) *ole.GUID {
	ns := ole.NewGUID("{11f47ad5-7b73-42c0-abae-878b1e16adee}")
	b := binary.BigEndian.AppendUint32(nil, ns.Data1)
	b = binary.BigEndian.AppendUint16(b, ns.Data2)
	b = binary.BigEndian.AppendUint16(b, ns.Data3)
	b = append(b, ns.Data4[:]...)
	b = append(b, "pinterface({"+generic+"};"+arg+")"...)
	h := sha1.Sum(b)
	g := &ole.GUID{
		Data1: binary.BigEndian.Uint32(h[0:4]),
		Data2: binary.BigEndian.Uint16(h[4:6]),
		Data3: binary.BigEndian.Uint16(h[6:8])&0x0fff | 0x5000,
	}
	copy(g.Data4[:], h[8:16])
	g.Data4[0] = g.Data4[0]&0x3f | 0x80
	return g
}

// method devuelve el puntero a la función número idx de la vtable de obj.
func method(obj *ole.IUnknown, idx int) uintptr {
	vt := *(**[16]uintptr)(unsafe.Pointer(obj))
	return vt[idx]
}

// inWinRT corre f en un hilo del sistema propio con WinRT inicializado (multihilo).
func inWinRT(f func() (int32, error)) (int32, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	err := ole.RoInitialize(roInitMultithreaded)
	var oe *ole.OleError
	if err == nil || (errors.As(err, &oe) && oe.Code() == sFalse) {
		defer procRoUninitialize.Call()
	}
	// Si el hilo ya era STA (RPC_E_CHANGED_MODE) se sigue igual, sin desinicializar.
	return f()
}

// awaitEnum espera una IAsyncOperation<enum> hasta wait y devuelve su resultado. Si vence la
// cancela.
func awaitEnum(op *ole.IInspectable, wait time.Duration) (int32, error) {
	info, err := op.QueryInterface(iidAsyncInfo)
	if err != nil {
		return 0, err
	}
	defer info.Release()
	deadline := time.Now().Add(wait)
	for {
		var status int32
		hr, _, _ := syscall.SyscallN(method(&info.IUnknown, asyncStatusIdx),
			uintptr(unsafe.Pointer(info)), uintptr(unsafe.Pointer(&status)))
		if hr != 0 {
			return 0, ole.NewError(hr)
		}
		if status == asyncCompleted {
			break
		}
		if status != asyncStarted {
			return 0, fmt.Errorf("operación de WinRT terminó con estado %d", status)
		}
		if time.Now().After(deadline) {
			syscall.SyscallN(method(&info.IUnknown, asyncCancelIdx), uintptr(unsafe.Pointer(info)))
			return 0, errors.New("la operación de WinRT tardó demasiado")
		}
		pumpFor(helloPollEvery)
	}
	var res int32
	hr, _, _ := syscall.SyscallN(method(&op.IUnknown, getResultsIdx),
		uintptr(unsafe.Pointer(op)), uintptr(unsafe.Pointer(&res)))
	if hr != 0 {
		return 0, ole.NewError(hr)
	}
	return res, nil
}

// HelloAvailable dice si la PC tiene Windows Hello (PIN, huella o cara) configurado.
// Cualquier falla cuenta como "no disponible".
func HelloAvailable() bool {
	res, err := inWinRT(func() (int32, error) {
		factory, err := ole.RoGetActivationFactory(helloClass, iidStatics)
		if err != nil {
			return 0, err
		}
		defer factory.Release()
		var op *ole.IInspectable
		hr, _, _ := syscall.SyscallN(method(&factory.IUnknown, checkAvailabilityIdx),
			uintptr(unsafe.Pointer(factory)), uintptr(unsafe.Pointer(&op)))
		if hr != 0 {
			return 0, ole.NewError(hr)
		}
		defer op.Release()
		return awaitEnum(op, availabilityWait)
	})
	return err == nil && res == availabilityAvailable
}

// HelloVerify muestra el diálogo de Windows Hello centrado sobre la ventana hwnd (0 = la ventana
// de osat) con el texto message. Devuelve true solo si la persona se verificó. Que cancele o que
// Hello no esté disponible da (false, nil): no es un error para mostrar, se vuelve a la contraseña.
// El error es para fallas inesperadas del sistema.
func HelloVerify(hwnd uintptr, message string) (bool, error) {
	if hwnd == 0 {
		hwnd = winutil.MainWindow()
	}
	if hwnd == 0 {
		return false, errors.New("no se encontró la ventana de osat")
	}
	// Con osat minimizado o detrás de otra ventana, el diálogo quedaría fuera de la vista (centrado sobre
	// una ventana minimizada, en -32000) y sin el primer plano: no se muestra.
	if !winutil.InFront(hwnd) {
		return false, nil
	}
	res, err := inWinRT(func() (int32, error) {
		owner, err := ownerWindow(hwnd)
		if err != nil {
			return 0, err
		}
		defer procDestroyWindow.Call(owner)
		// El diálogo lo muestra otro proceso (CredentialUIBroker): osat, que está al frente, lo deja ponerse delante.
		procAllowForeground.Call(asfwAny)
		factory, err := ole.RoGetActivationFactory(helloClass, iidInterop)
		if err != nil {
			return 0, err
		}
		defer factory.Release()
		msg, err := ole.NewHString(message)
		if err != nil {
			return 0, err
		}
		defer ole.DeleteHString(msg)
		var op *ole.IInspectable
		hr, _, _ := syscall.SyscallN(method(&factory.IUnknown, requestForWindowIdx),
			uintptr(unsafe.Pointer(factory)), owner, uintptr(msg),
			uintptr(unsafe.Pointer(iidAsyncResult)), uintptr(unsafe.Pointer(&op)))
		if hr != 0 {
			return 0, ole.NewError(hr)
		}
		go logForeground()
		defer op.Release()
		return awaitEnum(op, verificationWait)
	})
	if err != nil {
		return false, err
	}
	return res == resultVerified, nil
}

// WindowReady dice si la ventana de osat está al frente y no minimizada: recién ahí tiene sentido
// mostrar el diálogo de Windows Hello.
func WindowReady() bool { return winutil.InFront(winutil.MainWindow()) }

// asfwAny es ASFW_ANY: cualquier proceso puede ponerse al frente.
const asfwAny = ^uintptr(0)

// logForeground anota en el log qué programa está al frente un momento después de pedir Hello: si el
// diálogo vuelve a quedar detrás, ahí se ve quién tiene el primer plano.
func logForeground() {
	time.Sleep(time.Second)
	h, _, _ := procGetForeground.Call()
	var pid uint32
	procWindowPID.Call(h, uintptr(unsafe.Pointer(&pid)))
	exe := "?"
	if p, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid); err == nil {
		buf := make([]uint16, windows.MAX_PATH)
		n := uint32(len(buf))
		if windows.QueryFullProcessImageName(p, 0, &buf[0], &n) == nil {
			exe = filepath.Base(windows.UTF16ToString(buf[:n]))
		}
		windows.CloseHandle(p)
	}
	slog.Info("windows hello: al frente tras pedirlo", "programa", exe)
}

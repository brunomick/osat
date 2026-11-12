//go:build windows

// Package osdrag inicia un arrastre nativo del sistema (drag & drop OLE) con
// archivos locales, para soltarlos en el Explorador, el escritorio, etc.
// WebView2 no permite arrastrar archivos hacia afuera, así que se hace desde Go
// con las APIs del shell (las mismas que usa el Explorador).
package osdrag

import (
	"fmt"
	"path/filepath"
	"runtime"

	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"osat/internal/i18n"
	"osat/internal/winutil"
)

var (
	ole32   = windows.NewLazySystemDLL("ole32.dll")
	shell32 = windows.NewLazySystemDLL("shell32.dll")
	user32  = windows.NewLazySystemDLL("user32.dll")

	procOleInitialize      = ole32.NewProc("OleInitialize")
	procOleUninitialize    = ole32.NewProc("OleUninitialize")
	procILCreateFromPathW  = shell32.NewProc("ILCreateFromPathW")
	procILFree             = shell32.NewProc("ILFree")
	procILFindLastID       = shell32.NewProc("ILFindLastID")
	procSHCreateDataObject = shell32.NewProc("SHCreateDataObject")
	procSHDoDragDrop       = shell32.NewProc("SHDoDragDrop")
	procAttachThreadInput  = user32.NewProc("AttachThreadInput")
	procGetAsyncKeyState   = user32.NewProc("GetAsyncKeyState")
	procGetCursorPos       = user32.NewProc("GetCursorPos")
	procWindowFromPoint    = user32.NewProc("WindowFromPoint")
)

var iidIDataObject = windows.GUID{Data1: 0x0000010E, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}

const (
	dropEffectCopy  = 1
	dragdropSCancel = 0x00040101
	vkLButton       = 0x01
)

// ErrCancelled: el usuario soltó fuera de un destino válido o canceló (Esc).
var ErrCancelled = i18n.Err("app.dragCancelled")

// Supported: en Windows hay arrastre hacia el sistema (en los demás todavía no).
const Supported = true

type iunknown struct{ vtbl *[3]uintptr } // QueryInterface, AddRef, Release

// Files arrastra archivos o carpetas locales, que tienen que estar todos en la
// misma carpeta. Bloquea hasta que se suelta. Se puede llamar con el botón del
// mouse ya presionado (arrastre en curso desde la página): Windows lo continúa.
func Files(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	dir := filepath.Dir(paths[0])
	for _, p := range paths {
		if filepath.Dir(p) != dir {
			return i18n.Err("app.theFilesToDragMust")
		}
	}
	errc := make(chan error, 1)
	go func() {
		// OLE necesita un hilo fijo (STA) que procese mensajes durante el arrastre.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		errc <- doDrag(dir, paths)
	}()
	return <-errc
}

func doDrag(dir string, paths []string) error {
	if hr, _, _ := procOleInitialize.Call(0); int32(hr) < 0 {
		return fmt.Errorf("OleInitialize: 0x%08x", uint32(hr))
	}
	defer procOleUninitialize.Call()

	// Compartir el estado de entrada con el hilo de la ventana: el botón está
	// presionado ahí (empezó como un arrastre en la página).
	hwnd := winutil.MainWindow()
	if hwnd != 0 {
		ui := uintptr(winutil.WindowThread(hwnd))
		me := uintptr(windows.GetCurrentThreadId())
		if ui != me {
			procAttachThreadInput.Call(me, ui, 1)
			defer procAttachThreadInput.Call(me, ui, 0)
		}
	}

	// Si mientras se bajaban los archivos el usuario ya soltó el botón sobre
	// osat, no hay dónde soltarlos (y no queremos que se vuelvan a subir).
	if !leftButtonDown() && cursorOverOwnWindow() {
		return ErrCancelled
	}

	folder, err := pidl(dir)
	if err != nil {
		return err
	}
	defer procILFree.Call(folder)
	abs := make([]uintptr, 0, len(paths))
	defer func() {
		for _, a := range abs {
			procILFree.Call(a)
		}
	}()
	children := make([]uintptr, 0, len(paths))
	for _, p := range paths {
		a, err := pidl(p)
		if err != nil {
			return err
		}
		abs = append(abs, a)
		c, _, _ := procILFindLastID.Call(a) // apunta dentro de a: vive mientras viva a
		children = append(children, c)
	}

	var obj *iunknown
	hr, _, _ := procSHCreateDataObject.Call(folder, uintptr(len(children)), uintptr(unsafe.Pointer(&children[0])),
		0, uintptr(unsafe.Pointer(&iidIDataObject)), uintptr(unsafe.Pointer(&obj)))
	if int32(hr) < 0 || obj == nil {
		return fmt.Errorf("SHCreateDataObject: 0x%08x", uint32(hr))
	}
	defer syscall.SyscallN(obj.vtbl[2], uintptr(unsafe.Pointer(obj)))

	// pdsrc = NULL: el shell usa su fuente de arrastre por defecto (con imagen).
	var effect uint32
	hr, _, _ = procSHDoDragDrop.Call(hwnd, uintptr(unsafe.Pointer(obj)), 0, dropEffectCopy, uintptr(unsafe.Pointer(&effect)))
	if int32(hr) < 0 {
		return fmt.Errorf("SHDoDragDrop: 0x%08x", uint32(hr))
	}
	if hr == dragdropSCancel || effect == 0 {
		return ErrCancelled
	}
	return nil
}

func pidl(path string) (uintptr, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	r, _, _ := procILCreateFromPathW.Call(uintptr(unsafe.Pointer(p)))
	if r == 0 {
		return 0, i18n.Err("app.couldnTPrepareSFor", path)
	}
	return r, nil
}

func leftButtonDown() bool {
	r, _, _ := procGetAsyncKeyState.Call(vkLButton)
	return r&0x8000 != 0
}

func cursorOverOwnWindow() bool {
	var pt struct{ X, Y int32 }
	if r, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt))); r == 0 {
		return false
	}
	// POINT se pasa por valor: en x64 entra entero en un registro.
	h, _, _ := procWindowFromPoint.Call(uintptr(uint32(pt.X)) | uintptr(uint32(pt.Y))<<32)
	return h != 0 && winutil.OwnsWindow(h)
}

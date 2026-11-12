//go:build windows

// Package winutil tiene utilidades de Win32 compartidas (arrastre nativo).
package winutil

import (
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                 = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows        = user32.NewProc("EnumWindows")
	procGetWindowThreadPID = user32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible    = user32.NewProc("IsWindowVisible")
	procIsIconic           = user32.NewProc("IsIconic")
	procGetForeground      = user32.NewProc("GetForegroundWindow")
)

// OwnsWindow dice si la ventana h es de este proceso.
func OwnsWindow(h uintptr) bool {
	var pid uint32
	procGetWindowThreadPID.Call(h, uintptr(unsafe.Pointer(&pid)))
	return pid == windows.GetCurrentProcessId()
}

// WindowThread devuelve el id del hilo que creó la ventana h.
func WindowThread(h uintptr) uint32 {
	t, _, _ := procGetWindowThreadPID.Call(h, 0)
	return uint32(t)
}

var (
	enumOnce sync.Once
	enumCB   uintptr
	enumMu   sync.Mutex
	enumHwnd uintptr
)

// MainWindow devuelve la ventana visible de nivel superior de este proceso
// (la ventana de osat). 0 si todavía no hay ninguna.
func MainWindow() uintptr {
	enumOnce.Do(func() { // NewCallback tiene un límite de callbacks: crear uno solo
		enumCB = windows.NewCallback(func(h, _ uintptr) uintptr {
			if v, _, _ := procIsWindowVisible.Call(h); v != 0 && OwnsWindow(h) {
				enumHwnd = h
				return 0
			}
			return 1
		})
	})
	enumMu.Lock()
	defer enumMu.Unlock()
	enumHwnd = 0
	procEnumWindows.Call(enumCB, 0)
	return enumHwnd
}

// InFront dice si la ventana h está al frente (es la del primer plano) y no minimizada.
func InFront(h uintptr) bool {
	if h == 0 {
		return false
	}
	if min, _, _ := procIsIconic.Call(h); min != 0 {
		return false
	}
	fg, _, _ := procGetForeground.Call()
	return fg == h
}

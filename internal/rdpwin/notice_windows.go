//go:build windows

package rdpwin

// Aviso corto sobre el escritorio remoto (por ejemplo, "pegalo con Ctrl+V" después de soltar
// archivos). Los avisos de osat son HTML y la ventana del escritorio va encima de la página,
// así que no se verían: este es un tooltip nativo de Windows que se cierra solo.

import (
	"log/slog"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procSendMessage   = user32.NewProc("SendMessageW")
	procGetWindowRect = user32.NewProc("GetWindowRect")
	procInitCommonCtl = windows.NewLazySystemDLL("comctl32.dll").NewProc("InitCommonControlsEx")
)

// initTooltips registra la clase de los tooltips (tooltips_class32). Corre en el hilo del motor.
func initTooltips() {
	icc := struct{ size, classes uint32 }{8, 0x4} // INITCOMMONCONTROLSEX con ICC_BAR_CLASSES
	if r, _, _ := procInitCommonCtl.Call(uintptr(unsafe.Pointer(&icc))); r == 0 {
		slog.Debug("escritorio remoto: InitCommonControlsEx falló (sin avisos)")
	}
}

const (
	noticeTime = 5 * time.Second

	wsPopup         = 0x80000000
	wsExTopmost     = 0x00000008
	ttsNoPrefix     = 0x02
	ttsAlwaysTip    = 0x01
	ttfTrack        = 0x0020
	ttfAbsolute     = 0x0080
	ttmAddTool      = 0x0432 // TTM_ADDTOOLW
	ttmUpdateText   = 0x0439 // TTM_UPDATETIPTEXTW
	ttmTrackActive  = 0x0411
	ttmTrackPos     = 0x0412
	ttmSetMaxWidth  = 0x0418
	noticeMaxWidth  = 480
	noticeMarginPix = 16
)

// toolInfo es TOOLINFOW (64 bits).
type toolInfo struct {
	size     uint32
	flags    uint32
	hwnd     uintptr
	id       uintptr
	rect     [4]int32
	inst     uintptr
	text     *uint16
	lParam   uintptr
	reserved uintptr
}

// toolInfoSize: el tamaño sin lpReserved (TTTOOLINFOW_V2_SIZE), que aceptan las dos versiones de
// los controles comunes (la 5, de un proceso sin manifiesto, rechaza el tamaño completo).
const toolInfoSize = uint32(unsafe.Offsetof(toolInfo{}.reserved))

type rect struct{ left, top, right, bottom int32 }

// Notice muestra text arriba a la izquierda del escritorio de la pestaña id, unos segundos.
func (e *winEngine) Notice(id, text string) {
	e.post(func() {
		t := e.tabs[id]
		if t == nil || !t.visible {
			return
		}
		e.showNotice(t, text)
	})
}

func (e *winEngine) showNotice(t *tab, text string) {
	s, convErr := windows.UTF16PtrFromString(text)
	if convErr != nil {
		return
	}
	ti := toolInfo{flags: ttfTrack | ttfAbsolute, hwnd: t.hwnd, text: s}
	ti.size = toolInfoSize
	if t.tip == 0 {
		class, _ := windows.UTF16PtrFromString("tooltips_class32")
		inst, _, _ := procGetModuleHandle.Call(0)
		h, _, err := procCreateWindowEx.Call(wsExTopmost, uintptr(unsafe.Pointer(class)), 0,
			wsPopup|ttsNoPrefix|ttsAlwaysTip, 0, 0, 0, 0, t.hwnd, 0, inst, 0)
		if h == 0 {
			slog.Warn("escritorio remoto: no se pudo mostrar un aviso", "err", err)
			return
		}
		t.tip = h
		procSendMessage.Call(h, ttmSetMaxWidth, 0, noticeMaxWidth)
		procSendMessage.Call(h, ttmAddTool, 0, uintptr(unsafe.Pointer(&ti)))
	} else {
		procSendMessage.Call(t.tip, ttmUpdateText, 0, uintptr(unsafe.Pointer(&ti)))
	}
	var r rect
	procGetWindowRect.Call(t.hwnd, uintptr(unsafe.Pointer(&r)))
	x, y := uintptr(r.left+noticeMarginPix), uintptr(r.top+noticeMarginPix)
	procSendMessage.Call(t.tip, ttmTrackPos, 0, (y<<16)|(x&0xFFFF))
	procSendMessage.Call(t.tip, ttmTrackActive, 1, uintptr(unsafe.Pointer(&ti)))
	if t.tipTimer != nil {
		t.tipTimer.Stop()
	}
	id := t.id
	t.tipTimer = time.AfterFunc(noticeTime, func() {
		e.post(func() {
			if e.tabs[id] == t {
				e.hideNotice(t)
			}
		})
	})
}

func (e *winEngine) hideNotice(t *tab) {
	if t.tip == 0 {
		return
	}
	ti := toolInfo{hwnd: t.hwnd}
	ti.size = toolInfoSize
	procSendMessage.Call(t.tip, ttmTrackActive, 0, uintptr(unsafe.Pointer(&ti)))
}

// closeNotice destruye el tooltip de la pestaña (al cerrarla).
func (e *winEngine) closeNotice(t *tab) {
	if t.tipTimer != nil {
		t.tipTimer.Stop()
	}
	if t.tip != 0 {
		procDestroyWindow.Call(t.tip)
		t.tip = 0
	}
}

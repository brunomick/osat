//go:build windows

package rdpwin

// Soltar archivos sobre el escritorio remoto. El cliente de Escritorio remoto de Windows no
// acepta arrastrar archivos (tampoco mstsc): solo copiar y pegar. Así que el escritorio de
// cada pestaña se registra como destino de arrastre (IDropTarget de OLE; Windows lo busca
// subiendo desde la ventana bajo el puntero, así cubre las ventanas internas del control) y,
// al soltar, osat pone esos archivos en el portapapeles de Windows como lo hace el
// Explorador con Ctrl+C. El control se los ofrece al servidor y se pegan allá con Ctrl+V.

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	ole "github.com/go-ole/go-ole"
	"golang.org/x/sys/windows"
)

var (
	shell32 = windows.NewLazySystemDLL("shell32.dll")

	procRegisterDragDrop   = ole32.NewProc("RegisterDragDrop")
	procRevokeDragDrop     = ole32.NewProc("RevokeDragDrop")
	procReleaseStgMedium   = ole32.NewProc("ReleaseStgMedium")
	procDragQueryFile      = shell32.NewProc("DragQueryFileW")
	procOpenClipboard      = user32.NewProc("OpenClipboard")
	procCloseClipboard     = user32.NewProc("CloseClipboard")
	procEmptyClipboard     = user32.NewProc("EmptyClipboard")
	procSetClipboardData   = user32.NewProc("SetClipboardData")
	procRegisterClipFormat = user32.NewProc("RegisterClipboardFormatW")
	procGlobalAlloc        = kernel32.NewProc("GlobalAlloc")
	procGlobalLock         = kernel32.NewProc("GlobalLock")
	procGlobalUnlock       = kernel32.NewProc("GlobalUnlock")
	procGlobalFree         = kernel32.NewProc("GlobalFree")
)

const (
	cfHDrop          = 15
	tymedHGlobal     = 1
	dvAspectContent  = 1
	dropEffectNone   = 0
	dropEffectCopy   = 1
	gmemMoveable     = 0x0002
	dragQueryCount   = 0xFFFFFFFF
	sizeofDropFiles  = 20 // DROPFILES: pFiles, pt (x, y), fNC, fWide
	clipboardRetries = 10
)

var iidDropTarget = ole.NewGUID("{00000122-0000-0000-C000-000000000046}")

// formatEtc y stgMedium son FORMATETC y STGMEDIUM (64 bits).
type formatEtc struct {
	cfFormat uint16
	ptd      uintptr
	aspect   uint32
	lindex   int32
	tymed    uint32
}

type stgMedium struct {
	tymed   uint32
	hGlobal uintptr
	unk     uintptr
}

var hdropFormat = formatEtc{cfFormat: cfHDrop, aspect: dvAspectContent, lindex: -1, tymed: tymedHGlobal}

// dropTarget es un IDropTarget mínimo para una pestaña. Lo mantiene vivo la pestaña; Windows
// lo suelta en RevokeDragDrop (la cuenta de referencias no importa: nunca se libera solo).
type dropTarget struct {
	vtbl   *dropTargetVtbl
	accept bool // lo que se arrastra trae archivos
	onDrop func(paths []string)
}

type dropTargetVtbl struct {
	queryInterface, addRef, release      uintptr
	dragEnter, dragOver, dragLeave, drop uintptr
}

var (
	dropOnce sync.Once
	dropVT   *dropTargetVtbl
)

func newDropTarget(onDrop func(paths []string)) *dropTarget {
	dropOnce.Do(func() {
		ref := windows.NewCallback(func(this *dropTarget) uintptr { return 1 })
		dropVT = &dropTargetVtbl{
			queryInterface: windows.NewCallback(func(this *dropTarget, riid *ole.GUID, out *unsafe.Pointer) uintptr {
				if ole.IsEqualGUID(riid, ole.IID_IUnknown) || ole.IsEqualGUID(riid, iidDropTarget) {
					*out = unsafe.Pointer(this)
					return 0
				}
				*out = nil
				return ole.E_NOINTERFACE
			}),
			addRef:  ref,
			release: ref,
			dragEnter: windows.NewCallback(func(this *dropTarget, data, keys, pt uintptr, effect *uint32) uintptr {
				this.accept = hasFiles(data)
				*effect = this.effect()
				return 0
			}),
			dragOver: windows.NewCallback(func(this *dropTarget, keys, pt uintptr, effect *uint32) uintptr {
				*effect = this.effect()
				return 0
			}),
			dragLeave: windows.NewCallback(func(this *dropTarget) uintptr {
				this.accept = false
				return 0
			}),
			drop: windows.NewCallback(func(this *dropTarget, data, keys, pt uintptr, effect *uint32) (r uintptr) {
				defer func() {
					if v := recover(); v != nil { // un pánico no puede cruzar a Windows
						slog.Error("pánico al soltar archivos en el escritorio remoto", "panic", v)
						r = 0
					}
				}()
				*effect = dropEffectNone
				paths := filesOf(data)
				if len(paths) > 0 {
					*effect = dropEffectCopy
					this.onDrop(paths)
				}
				this.accept = false
				return 0
			}),
		}
	})
	return &dropTarget{vtbl: dropVT, onDrop: onDrop}
}

func (d *dropTarget) effect() uint32 {
	if d.accept {
		return dropEffectCopy
	}
	return dropEffectNone
}

// ptr convierte un puntero que llegó como uintptr (memoria de Windows, no de Go) en unsafe.Pointer.
func ptr(u uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&u)) }

// dataCall llama al método slot de un IDataObject.
func dataCall(data uintptr, slot int, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(ptr(data))
	fn := *(*uintptr)(unsafe.Add(vtbl, uintptr(slot)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{data}, args...)...)
	return r
}

const (
	slotDataGetData      = 3
	slotDataQueryGetData = 5
)

func hasFiles(data uintptr) bool {
	f := hdropFormat
	return data != 0 && dataCall(data, slotDataQueryGetData, uintptr(unsafe.Pointer(&f))) == 0
}

// filesOf saca las rutas (CF_HDROP) de lo que se soltó.
func filesOf(data uintptr) []string {
	if data == 0 {
		return nil
	}
	f := hdropFormat
	var m stgMedium
	if int32(dataCall(data, slotDataGetData, uintptr(unsafe.Pointer(&f)), uintptr(unsafe.Pointer(&m)))) < 0 {
		return nil
	}
	defer procReleaseStgMedium.Call(uintptr(unsafe.Pointer(&m)))
	if m.tymed != tymedHGlobal || m.hGlobal == 0 {
		return nil
	}
	n, _, _ := procDragQueryFile.Call(m.hGlobal, dragQueryCount, 0, 0)
	paths := make([]string, 0, n)
	for i := uintptr(0); i < n; i++ {
		size, _, _ := procDragQueryFile.Call(m.hGlobal, i, 0, 0)
		buf := make([]uint16, size+1)
		procDragQueryFile.Call(m.hGlobal, i, uintptr(unsafe.Pointer(&buf[0])), size+1)
		paths = append(paths, windows.UTF16ToString(buf))
	}
	return paths
}

// registerDrop hace de t un destino de arrastre de archivos.
func (e *winEngine) registerDrop(t *tab) {
	id := t.id
	t.drop = newDropTarget(func(paths []string) { e.onFilesDropped(id, paths) })
	if r, _, _ := procRegisterDragDrop.Call(t.hwnd, uintptr(unsafe.Pointer(t.drop))); int32(r) < 0 {
		slog.Warn("escritorio remoto: no se pueden soltar archivos", "hr", fmt.Sprintf("0x%08x", uint32(r)))
		t.drop = nil
	}
}

func (e *winEngine) revokeDrop(t *tab) {
	if t.drop != nil {
		procRevokeDragDrop.Call(t.hwnd)
		t.drop = nil
	}
}

// onFilesDropped corre en el hilo del motor (OLE llama al destino desde el hilo que lo registró).
func (e *winEngine) onFilesDropped(id string, paths []string) {
	t := e.tabs[id]
	if t == nil {
		return
	}
	if err := setClipboardFiles(t.hwnd, paths); err != nil {
		slog.Warn("escritorio remoto: no se pudieron copiar los archivos soltados", "err", err)
		e.emit(Event{ID: id, Kind: "filesError", Text: err.Error()})
		return
	}
	slog.Info("escritorio remoto: archivos soltados, copiados al portapapeles", "tab", id, "files", len(paths))
	e.emit(Event{ID: id, Kind: "filesCopied", Files: len(paths)})
	e.Focus(id) // para pegar con Ctrl+V sin tener que hacer clic
}

// dropFiles arma un DROPFILES con las rutas (UTF-16, separadas por un nulo y con dos al final).
func dropFiles(paths []string) []byte {
	var text []uint16
	for _, p := range paths {
		text = append(text, utf16.Encode([]rune(p))...)
		text = append(text, 0)
	}
	text = append(text, 0)
	b := make([]byte, sizeofDropFiles+2*len(text))
	b[0] = sizeofDropFiles // pFiles: el texto empieza después del encabezado
	b[16] = 1              // fWide: rutas en UTF-16
	for i, c := range text {
		b[sizeofDropFiles+2*i] = byte(c)
		b[sizeofDropFiles+2*i+1] = byte(c >> 8)
	}
	return b
}

// globalBytes copia b a un HGLOBAL movible (el portapapeles se queda con él).
func globalBytes(b []byte) (uintptr, error) {
	h, _, err := procGlobalAlloc.Call(gmemMoveable, uintptr(len(b)))
	if h == 0 {
		return 0, err
	}
	p, _, err := procGlobalLock.Call(h)
	if p == 0 {
		procGlobalFree.Call(h)
		return 0, err
	}
	copy(unsafe.Slice((*byte)(ptr(p)), len(b)), b)
	procGlobalUnlock.Call(h)
	return h, nil
}

// setClipboardFiles pone las rutas en el portapapeles como una copia del Explorador (CF_HDROP y
// "Preferred DropEffect" = copiar). owner es una ventana del hilo que llama.
func setClipboardFiles(owner uintptr, paths []string) error {
	if len(paths) == 0 {
		return errors.New("rdpwin: no hay archivos")
	}
	// Otro programa puede tener el portapapeles abierto un instante.
	opened := false
	for i := 0; i < clipboardRetries; i++ {
		if r, _, _ := procOpenClipboard.Call(owner); r != 0 {
			opened = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !opened {
		return errors.New("rdpwin: el portapapeles está ocupado")
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()
	h, err := globalBytes(dropFiles(paths))
	if err != nil {
		return err
	}
	if r, _, err := procSetClipboardData.Call(cfHDrop, h); r == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("rdpwin: SetClipboardData: %w", err)
	}
	name, _ := windows.UTF16PtrFromString("Preferred DropEffect")
	if cf, _, _ := procRegisterClipFormat.Call(uintptr(unsafe.Pointer(name))); cf != 0 {
		if h, err := globalBytes([]byte{dropEffectCopy, 0, 0, 0}); err == nil {
			if r, _, _ := procSetClipboardData.Call(cf, h); r == 0 {
				procGlobalFree.Call(h)
			}
		}
	}
	return nil
}

//go:build windows

package winclip

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"osat/internal/i18n"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	ole32    = windows.NewLazySystemDLL("ole32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procSeq             = user32.NewProc("GetClipboardSequenceNumber")
	procIsAvailable     = user32.NewProc("IsClipboardFormatAvailable")
	procRegisterFormat  = user32.NewProc("RegisterClipboardFormatW")
	procOpenClipboard   = user32.NewProc("OpenClipboard")
	procCloseClipboard  = user32.NewProc("CloseClipboard")
	procGetData         = user32.NewProc("GetClipboardData")
	procDragQueryFile   = shell32.NewProc("DragQueryFileW")
	procOleInitialize   = ole32.NewProc("OleInitialize")
	procOleUninitialize = ole32.NewProc("OleUninitialize")
	procOleGetClipboard = ole32.NewProc("OleGetClipboard")
	procReleaseStg      = ole32.NewProc("ReleaseStgMedium")
	procGlobalLock      = kernel32.NewProc("GlobalLock")
	procGlobalUnlock    = kernel32.NewProc("GlobalUnlock")
	procGlobalSize      = kernel32.NewProc("GlobalSize")
)

const (
	cfHDrop         = 15
	tymedHGlobal    = 1
	tymedIStream    = 4
	dvAspectContent = 1
	dragQueryCount  = 0xFFFFFFFF

	fdAttributes = 0x04
	fdWritesTime = 0x20
	fdFileSize   = 0x40
	attrDir      = 0x10

	fileDescriptorSize = 592 // FILEDESCRIPTORW
	nameOffset         = 72  // cFileName dentro de FILEDESCRIPTORW
	maxPath            = 260

	slotRelease = 2
	slotGetData = 3 // IDataObject::GetData
	slotRead    = 3 // ISequentialStream::Read
)

func registerFormat(name string) uint16 {
	p, _ := windows.UTF16PtrFromString(name)
	r, _, _ := procRegisterFormat.Call(uintptr(unsafe.Pointer(p)))
	return uint16(r)
}

var (
	cfDescriptor = registerFormat("FileGroupDescriptorW")
	cfContents   = registerFormat("FileContents")
)

func available(cf uint16) bool {
	r, _, _ := procIsAvailable.Call(uintptr(cf))
	return r != 0
}

// Current dice qué archivos hay en el portapapeles, sin abrirlo.
func Current() Info {
	seq, _, _ := procSeq.Call()
	info := Info{Seq: uint32(seq)}
	switch {
	case available(cfHDrop):
		info.Kind = KindPaths
	case cfDescriptor != 0 && cfContents != 0 && available(cfDescriptor):
		info.Kind = KindVirtual
	}
	return info
}

// Paths devuelve las rutas que copió el Explorador (CF_HDROP).
func Paths() ([]string, error) {
	if !available(cfHDrop) {
		return nil, ErrNothing
	}
	opened := false
	for i := 0; i < 10; i++ { // otro programa puede tenerlo abierto un instante
		if r, _, _ := procOpenClipboard.Call(0); r != 0 {
			opened = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !opened {
		return nil, i18n.Err("winclip.busy")
	}
	defer procCloseClipboard.Call()
	h, _, _ := procGetData.Call(cfHDrop)
	if h == 0 {
		return nil, ErrNothing
	}
	n, _, _ := procDragQueryFile.Call(h, dragQueryCount, 0, 0)
	paths := make([]string, 0, n)
	for i := uintptr(0); i < n; i++ {
		size, _, _ := procDragQueryFile.Call(h, i, 0, 0)
		buf := make([]uint16, size+1)
		procDragQueryFile.Call(h, i, uintptr(unsafe.Pointer(&buf[0])), size+1)
		paths = append(paths, windows.UTF16ToString(buf))
	}
	return paths, nil
}

// SaveVirtual escribe en dir los archivos virtuales del portapapeles (ver save).
// El contenido se pide al programa que los copió (el cliente de Escritorio remoto
// los trae del servidor), así que un archivo grande tarda lo que tarde en bajar.
func SaveVirtual(dir string) ([]string, error) {
	type result struct {
		made []string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		// OLE pide un hilo fijo con su propio apartamento.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if r, _, _ := procOleInitialize.Call(0); int32(r) < 0 {
			ch <- result{err: fmt.Errorf("winclip: OleInitialize: 0x%08x", uint32(r))}
			return
		}
		defer procOleUninitialize.Call()
		made, err := saveVirtual(dir)
		ch <- result{made, err}
	}()
	r := <-ch
	return r.made, r.err
}

func saveVirtual(dir string) ([]string, error) {
	var data uintptr
	if r, _, _ := procOleGetClipboard.Call(uintptr(unsafe.Pointer(&data))); int32(r) < 0 || data == 0 {
		return nil, ErrNothing
	}
	defer comCall(data, slotRelease)
	entries, err := descriptors(data)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, ErrNothing
	}
	return save(dir, entries, func(i int) (io.ReadCloser, error) { return contents(data, i, entries[i]) })
}

// formatEtc y stgMedium son FORMATETC y STGMEDIUM.
type formatEtc struct {
	cfFormat uint16
	ptd      uintptr
	aspect   uint32
	lindex   int32
	tymed    uint32
}

type stgMedium struct {
	tymed uint32
	h     uintptr // hGlobal o pstm, según tymed
	unk   uintptr
}

// mem convierte un puntero que llegó como uintptr (memoria de Windows, no de Go) en unsafe.Pointer.
func mem(u uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&u)) }

// comCall llama al método slot de un objeto COM.
func comCall(obj uintptr, slot int, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(mem(obj))
	fn := *(*uintptr)(unsafe.Add(vtbl, uintptr(slot)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{obj}, args...)...)
	return r
}

func getData(data uintptr, f formatEtc) (stgMedium, error) {
	var m stgMedium
	if r := comCall(data, slotGetData, uintptr(unsafe.Pointer(&f)), uintptr(unsafe.Pointer(&m))); int32(r) < 0 {
		return m, fmt.Errorf("winclip: GetData: 0x%08x", uint32(r))
	}
	return m, nil
}

// globalBytes copia el contenido de un HGLOBAL (hasta max bytes si max >= 0).
func globalBytes(h uintptr, max int64) []byte {
	size, _, _ := procGlobalSize.Call(h)
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return nil
	}
	defer procGlobalUnlock.Call(h)
	n := int64(size)
	if max >= 0 && max < n {
		n = max
	}
	return bytes.Clone(unsafe.Slice((*byte)(mem(p)), n))
}

// descriptors lee el FILEGROUPDESCRIPTORW: los nombres, si son carpetas y las fechas.
func descriptors(data uintptr) ([]Entry, error) {
	m, err := getData(data, formatEtc{cfFormat: cfDescriptor, aspect: dvAspectContent, lindex: -1, tymed: tymedHGlobal})
	if err != nil {
		return nil, ErrNothing
	}
	defer procReleaseStg.Call(uintptr(unsafe.Pointer(&m)))
	if m.tymed != tymedHGlobal || m.h == 0 {
		return nil, ErrNothing
	}
	return parseDescriptors(globalBytes(m.h, -1))
}

func parseDescriptors(b []byte) ([]Entry, error) {
	if len(b) < 4 {
		return nil, ErrNothing
	}
	n := int(le32(b))
	if n < 0 || 4+n*fileDescriptorSize > len(b) {
		return nil, errors.New("winclip: FILEGROUPDESCRIPTORW incompleto")
	}
	out := make([]Entry, 0, n)
	for i := 0; i < n; i++ {
		d := b[4+i*fileDescriptorSize : 4+(i+1)*fileDescriptorSize]
		flags := le32(d)
		name := make([]uint16, maxPath)
		for j := range name {
			name[j] = uint16(d[nameOffset+2*j]) | uint16(d[nameOffset+2*j+1])<<8
		}
		e := Entry{Name: windows.UTF16ToString(name)}
		if flags&fdAttributes != 0 {
			e.Dir = le32(d[36:])&attrDir != 0
		}
		if flags&fdWritesTime != 0 {
			ft := windows.Filetime{LowDateTime: le32(d[56:]), HighDateTime: le32(d[60:])}
			if ns := ft.Nanoseconds(); ns > 0 {
				e.ModTime = time.Unix(0, ns)
			}
		}
		if flags&fdFileSize != 0 {
			e.size = int64(le32(d[64:]))<<32 | int64(le32(d[68:]))
		} else {
			e.size = -1
		}
		out = append(out, e)
	}
	return out, nil
}

func le32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

// contents pide el contenido del archivo i (FileContents con lindex = i).
func contents(data uintptr, i int, e Entry) (io.ReadCloser, error) {
	m, err := getData(data, formatEtc{cfFormat: cfContents, aspect: dvAspectContent, lindex: int32(i), tymed: tymedIStream | tymedHGlobal})
	if err != nil {
		return nil, i18n.Err("winclip.readFailed", e.Name, err)
	}
	switch {
	case m.tymed == tymedIStream && m.h != 0:
		return &stream{m: m}, nil
	case m.tymed == tymedHGlobal && m.h != 0:
		b := globalBytes(m.h, e.size)
		procReleaseStg.Call(uintptr(unsafe.Pointer(&m)))
		return io.NopCloser(bytes.NewReader(b)), nil
	}
	procReleaseStg.Call(uintptr(unsafe.Pointer(&m)))
	return nil, i18n.Err("winclip.readFailed", e.Name, errors.New("unsupported medium"))
}

// stream lee un IStream de COM. Se usa en el mismo hilo que lo pidió.
type stream struct {
	m    stgMedium
	done bool
}

func (s *stream) Read(p []byte) (int, error) {
	if s.done {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	if len(p) > 1<<20 {
		p = p[:1<<20]
	}
	var n uint32
	r := comCall(s.m.h, slotRead, uintptr(unsafe.Pointer(&p[0])), uintptr(len(p)), uintptr(unsafe.Pointer(&n)))
	if int32(r) < 0 {
		return int(n), fmt.Errorf("winclip: IStream::Read: 0x%08x", uint32(r))
	}
	if n == 0 {
		s.done = true
		return 0, io.EOF
	}
	return int(n), nil
}

func (s *stream) Close() error {
	procReleaseStg.Call(uintptr(unsafe.Pointer(&s.m)))
	return nil
}

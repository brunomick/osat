//go:build windows

package rdpwin

import (
	"encoding/binary"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"

	ole "github.com/go-ole/go-ole"
	"golang.org/x/sys/windows"

	"osat/internal/browser"
)

func TestDropFilesLayout(t *testing.T) {
	b := dropFiles([]string{`C:\a.txt`, `D:\ñ`})
	if got := binary.LittleEndian.Uint32(b[0:]); got != sizeofDropFiles {
		t.Fatalf("pFiles = %d", got)
	}
	if got := binary.LittleEndian.Uint32(b[16:]); got != 1 {
		t.Fatalf("fWide = %d", got)
	}
	u := make([]uint16, (len(b)-sizeofDropFiles)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[sizeofDropFiles+2*i:])
	}
	if want := append(utf16.Encode([]rune("C:\\a.txt\x00D:\\ñ\x00")), 0); !reflect.DeepEqual(u, want) {
		t.Fatalf("texto = %v, want %v", u, want)
	}
}

// Prueba de humo: le pasa al destino de arrastre de una pestaña un IDataObject real del Shell
// (lo mismo que entrega el Explorador al arrastrar) y espera los archivos en el portapapeles.
// Pisa el portapapeles. Solo a mano: OSAT_SMOKE=1 go test -run TestSmokeDropFiles -v ./internal/rdpwin/
func TestSmokeDropFiles(t *testing.T) {
	if os.Getenv("OSAT_SMOKE") == "" {
		t.Skip("solo a mano")
	}
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "uno.txt"), filepath.Join(dir, "dos ñ.txt")}
	for _, p := range paths {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	parent := testWindow(t)
	events := make(chan Event, 16)
	eng, err := New(func() uintptr { return parent }, func(ev Event) { events <- ev })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.CloseAll)
	if err := eng.Open("t1", Config{Server: "127.0.0.1", Port: port, User: "x", Password: "x"}, browser.Bounds{W: 400, H: 300}, 1); err != nil {
		t.Fatal(err)
	}
	we := eng.(*winEngine)
	err = we.call(callTimeout, func() error {
		tb := we.tabs["t1"]
		if tb == nil || tb.drop == nil {
			t.Error("la pestaña no quedó registrada para soltar archivos")
			return nil
		}
		data := shellDataObject(t, paths)
		defer dataCall(data, 2) // Release
		var effect uint32 = dropEffectCopy
		dt := uintptr(unsafe.Pointer(tb.drop))
		comMethod(dt, 3, data, 0, 0, uintptr(unsafe.Pointer(&effect))) // DragEnter
		if effect != dropEffectCopy {
			t.Errorf("DragEnter no acepta archivos: effect=%d", effect)
		}
		comMethod(dt, 6, data, 0, 0, uintptr(unsafe.Pointer(&effect))) // Drop
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev.Kind != "filesCopied" {
				continue
			}
			if ev.Files != 2 {
				t.Fatalf("Files = %d", ev.Files)
			}
			if got := clipboardFiles(t); !reflect.DeepEqual(got, paths) {
				t.Fatalf("portapapeles = %q, want %q", got, paths)
			}
			return
		case <-deadline:
			t.Fatal("no llegó filesCopied")
		}
	}
}

func comMethod(obj uintptr, slot int, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(ptr(obj))
	fn := *(*uintptr)(unsafe.Add(vtbl, uintptr(slot)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{obj}, args...)...)
	return r
}

// shellDataObject arma el IDataObject del Shell para esos archivos (de la misma carpeta).
func shellDataObject(t *testing.T, paths []string) uintptr {
	pSHParse := shell32.NewProc("SHParseDisplayName")
	pSHCreateDataObject := shell32.NewProc("SHCreateDataObject")
	pILFindLastID := shell32.NewProc("ILFindLastID")
	parse := func(p string) uintptr {
		var pidl uintptr
		name, _ := windows.UTF16PtrFromString(p)
		if r, _, _ := pSHParse.Call(uintptr(unsafe.Pointer(name)), 0, uintptr(unsafe.Pointer(&pidl)), 0, 0); int32(r) < 0 {
			t.Fatalf("SHParseDisplayName(%s): 0x%08x", p, uint32(r))
		}
		return pidl
	}
	// Como el Explorador: la carpeta y los nombres relativos a ella.
	folder := parse(filepath.Dir(paths[0]))
	children := make([]uintptr, len(paths))
	for i, p := range paths {
		children[i], _, _ = pILFindLastID.Call(parse(p))
	}
	iidDataObject := ole.NewGUID("{0000010E-0000-0000-C000-000000000046}")
	var data uintptr
	if r, _, _ := pSHCreateDataObject.Call(folder, uintptr(len(children)), uintptr(unsafe.Pointer(&children[0])), 0, uintptr(unsafe.Pointer(iidDataObject)), uintptr(unsafe.Pointer(&data))); int32(r) < 0 {
		t.Fatalf("SHCreateDataObject: 0x%08x", uint32(r))
	}
	return data
}

// clipboardFiles lee las rutas de CF_HDROP del portapapeles.
func clipboardFiles(t *testing.T) []string {
	pGetClipboardData := user32.NewProc("GetClipboardData")
	if r, _, _ := procOpenClipboard.Call(0); r == 0 {
		t.Fatal("no se pudo abrir el portapapeles")
	}
	defer procCloseClipboard.Call()
	h, _, _ := pGetClipboardData.Call(cfHDrop)
	if h == 0 {
		t.Fatal("el portapapeles no tiene archivos")
	}
	n, _, _ := procDragQueryFile.Call(h, dragQueryCount, 0, 0)
	var out []string
	for i := uintptr(0); i < n; i++ {
		buf := make([]uint16, 520)
		procDragQueryFile.Call(h, i, uintptr(unsafe.Pointer(&buf[0])), 520)
		out = append(out, windows.UTF16ToString(buf))
	}
	return out
}

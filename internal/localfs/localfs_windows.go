//go:build windows

package localfs

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"osat/internal/i18n"
	"osat/internal/remote"
)

const (
	hasDrives       = true
	caseInsensitive = true
)

var (
	shell32              = windows.NewLazySystemDLL("shell32.dll")
	procSHFileOperationW = shell32.NewProc("SHFileOperationW")
	procShellExecuteW    = shell32.NewProc("ShellExecuteW")
)

func rootDir() string { return `C:\` }

func listDrives() Listing {
	out := Listing{Path: Drives, Crumbs: []Crumb{{Name: i18n.T("native.thisComputer"), Path: Drives}}, Entries: []remote.FileEntry{}}
	mask, _ := windows.GetLogicalDrives()
	for i := 0; i < 26; i++ {
		if mask&(1<<i) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		e := remote.FileEntry{Name: root[:2], Path: root, IsDir: true, Mode: i18n.T("native.drive")}
		p, _ := windows.UTF16PtrFromString(root)
		var label [261]uint16
		if windows.GetVolumeInformation(p, &label[0], uint32(len(label)), nil, nil, nil, nil, 0) == nil {
			if l := windows.UTF16ToString(label[:]); l != "" {
				e.Name = fmt.Sprintf("%s (%s)", l, root[:2])
			}
		}
		var free, total uint64
		if windows.GetDiskFreeSpaceEx(p, nil, &total, &free) == nil {
			e.Size = int64(total)
		}
		out.Entries = append(out.Entries, e)
	}
	return out
}

// SHFILEOPSTRUCTW (alineación natural en 64 bits).
type shFileOp struct {
	hwnd     uintptr
	wFunc    uint32
	pFrom    *uint16
	pTo      *uint16
	fFlags   uint16
	aborted  int32
	mappings uintptr
	title    *uint16
}

const (
	foDelete     = 3
	fofSilent    = 0x0004
	fofNoConfirm = 0x0010
	fofAllowUndo = 0x0040
	fofNoErrorUI = 0x0400
)

// Remove manda los elementos a la Papelera de reciclaje (como Supr en el
// Explorador). Si la unidad no tiene papelera, Windows los borra.
func Remove(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	// pFrom: rutas separadas por \0 y terminadas en \0\0.
	var buf []uint16
	for _, p := range paths {
		u, err := syscall.UTF16FromString(p)
		if err != nil {
			return err
		}
		buf = append(buf, u...) // incluye el \0 final
	}
	buf = append(buf, 0)
	op := shFileOp{wFunc: foDelete, pFrom: &buf[0], fFlags: fofAllowUndo | fofNoConfirm | fofSilent | fofNoErrorUI}
	r, _, _ := procSHFileOperationW.Call(uintptr(unsafe.Pointer(&op)))
	if r != 0 {
		return i18n.Err("app.couldnTDeleteCodeD", r)
	}
	if op.aborted != 0 {
		return i18n.Err("app.deletionCancelled")
	}
	return nil
}

// Open abre el archivo con su programa predeterminado (o la carpeta en el Explorador).
func Open(p string) error {
	verb, _ := windows.UTF16PtrFromString("open")
	file, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return err
	}
	r, _, _ := procShellExecuteW.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)), 0, 0, 1)
	if r <= 32 {
		return i18n.Err("app.windowsCouldnTOpenThe", r)
	}
	return nil
}

// Reveal abre el Explorador con el elemento seleccionado.
func Reveal(p string) error {
	if _, err := os.Stat(p); err != nil {
		return friendly(err)
	}
	cmd := exec.Command("explorer.exe")
	// explorer no entiende el escapado de argumentos de Go: línea armada a mano.
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `explorer.exe /select,"` + strings.ReplaceAll(p, `"`, ``) + `"`}
	return cmd.Start()
}

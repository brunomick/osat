//go:build windows

package localterm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/UserExistsError/conpty"
	"golang.org/x/sys/windows"

	"osat/internal/i18n"
)

// detect busca las shells instaladas: PowerShell (7 si está), cmd, WSL y Git Bash.
func detect() []Shell {
	sys := filepath.Join(os.Getenv("SystemRoot"), "System32")
	var out []Shell

	if p, err := exec.LookPath("pwsh.exe"); err == nil {
		out = append(out, Shell{ID: "pwsh", Name: "PowerShell 7", path: p, args: []string{"-NoLogo"}})
	}
	if p := filepath.Join(sys, "WindowsPowerShell", "v1.0", "powershell.exe"); exists(p) {
		name := "PowerShell"
		if len(out) > 0 {
			name = "Windows PowerShell" // ya hay un "PowerShell 7"
		}
		// Primera en la lista: es la predeterminada (DefaultID).
		out = append([]Shell{{ID: "powershell", Name: name, path: p, args: []string{"-NoLogo"}}}, out...)
	}
	if p := os.Getenv("ComSpec"); exists(p) {
		out = append(out, Shell{ID: "cmd", Name: "cmd", path: p})
	} else if p := filepath.Join(sys, "cmd.exe"); exists(p) {
		out = append(out, Shell{ID: "cmd", Name: "cmd", path: p})
	}
	if p := filepath.Join(sys, "wsl.exe"); exists(p) {
		out = append(out, Shell{ID: "wsl", Name: "WSL", path: p})
	}
	if p := gitBash(); p != "" {
		// CHERE_INVOKING: que bash se quede en la carpeta en la que se abrió.
		out = append(out, Shell{ID: "gitbash", Name: "Git Bash", path: p, args: []string{"--login", "-i"}, env: []string{"CHERE_INVOKING=1"}})
	}
	return out
}

func exists(p string) bool {
	fi, err := os.Stat(p)
	return p != "" && err == nil && !fi.IsDir()
}

// gitBash busca bash.exe de Git for Windows en sus lugares habituales.
func gitBash() string {
	for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), filepath.Join(os.Getenv("LocalAppData"), "Programs")} {
		if base == "" {
			continue
		}
		if p := filepath.Join(base, "Git", "bin", "bash.exe"); exists(p) {
			return p
		}
	}
	return ""
}

type proc struct {
	cp   *conpty.ConPty
	once sync.Once
	err  error
}

func start(sh Shell, dir string, cols, rows int) (Proc, error) {
	cmdline := windows.ComposeCommandLine(append([]string{sh.path}, sh.args...))
	env := append(os.Environ(), "TERM=xterm-256color")
	env = append(env, sh.env...)
	cp, err := conpty.Start(cmdline, conpty.ConPtyDimensions(cols, rows), conpty.ConPtyWorkDir(dir), conpty.ConPtyEnv(env))
	if err != nil {
		return nil, i18n.Err("remote.couldnTOpenSV", sh.Name, err)
	}
	return &proc{cp: cp}, nil
}

func (p *proc) Read(b []byte) (int, error)  { return p.cp.Read(b) }
func (p *proc) Write(b []byte) (int, error) { return p.cp.Write(b) }
func (p *proc) Resize(cols, rows int) error { return p.cp.Resize(cols, rows) }
func (p *proc) Wait()                       { p.cp.Wait(context.Background()) }

// Close termina la shell y suelta los recursos. ConPty.Close cierra handles de
// Windows, y cerrarlos dos veces podría cerrar uno ajeno: se hace una sola vez.
func (p *proc) Close() error {
	p.once.Do(func() { p.err = p.cp.Close() })
	return p.err
}

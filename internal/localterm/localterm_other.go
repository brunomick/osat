//go:build !windows

package localterm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/creack/pty"

	"osat/internal/i18n"
)

// detect ofrece primero la shell del usuario ($SHELL) y después las conocidas
// de /etc/shells (bash, zsh, fish, sh) que estén instaladas, sin repetir.
func detect() []Shell {
	var out []Shell
	seen := map[string]bool{}
	add := func(p string) {
		name := filepath.Base(p)
		if !filepath.IsAbs(p) || seen[name] || !executable(p) {
			return
		}
		seen[name] = true
		// Como shell de inicio de sesión (-l), igual que la Terminal de macOS: lee ~/.profile.
		out = append(out, Shell{ID: name, Name: name, path: p, args: []string{"-l"}})
	}
	add(os.Getenv("SHELL"))
	known := map[string]bool{"bash": true, "zsh": true, "fish": true, "sh": true}
	if b, err := os.ReadFile("/etc/shells"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if p := strings.TrimSpace(line); known[filepath.Base(p)] {
				add(p)
			}
		}
	}
	add("/bin/sh") // siempre está, aunque falte /etc/shells
	return out
}

func executable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

type proc struct {
	cmd  *exec.Cmd
	pty  *os.File
	done chan struct{} // se cierra cuando la shell termina
	once sync.Once
	err  error
}

func start(sh Shell, dir string, cols, rows int) (Proc, error) {
	cmd := exec.Command(sh.path, sh.args...)
	cmd.Dir = dir
	// PWD: exec solo lo agrega si no se le pasa Env.
	cmd.Env = append(append(os.Environ(), "TERM=xterm-256color", "PWD="+dir), sh.env...)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, i18n.Err("remote.couldnTOpenSV", sh.Name, err)
	}
	p := &proc{cmd: cmd, pty: f, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(p.done)
	}()
	return p, nil
}

func (p *proc) Read(b []byte) (int, error)  { return p.pty.Read(b) }
func (p *proc) Write(b []byte) (int, error) { return p.pty.Write(b) }
func (p *proc) Wait()                       { <-p.done }

func (p *proc) Resize(cols, rows int) error {
	return pty.Setsize(p.pty, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

// Close termina la shell (y lo que haya lanzado en su grupo, como al cerrar una
// ventana de terminal) y cierra la consola. Se hace una sola vez.
func (p *proc) Close() error {
	p.once.Do(func() {
		select {
		case <-p.done: // ya terminó: su pid podría ser de otro proceso
		default:
			// pty.Start la deja como líder de su sesión: su grupo tiene su pid.
			_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGHUP)
		}
		p.err = p.pty.Close()
	})
	return p.err
}

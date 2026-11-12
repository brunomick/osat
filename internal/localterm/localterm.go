// Package localterm abre terminales de este equipo (PowerShell, cmd, WSL, Git
// Bash...; en Linux y macOS bash, zsh...) para mostrarlas en una pestaña, igual
// que las de SSH. En Windows usa la consola pseudo-terminal (ConPTY); en los
// demás, una pty (creack/pty).
package localterm

import (
	"io"
	"os"

	"osat/internal/i18n"
)

// DefaultID es la shell que se usa si no se elige otra. Fuera de Windows no
// existe y se usa la primera de la lista (la del usuario, $SHELL).
const DefaultID = "powershell"

// Shell es una terminal que se puede abrir en este equipo.
type Shell struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	path string
	args []string
	env  []string // variables extra para esta shell
}

// Proc es una shell en marcha. Lo que escribe el usuario va por Write y lo que
// imprime sale por Read; Close la termina (y es seguro llamarlo más de una vez).
type Proc interface {
	io.ReadWriter
	Resize(cols, rows int) error
	// Wait bloquea hasta que la shell termina.
	Wait()
	Close() error
}

// Shells devuelve las terminales disponibles en este equipo, en el orden en
// que se ofrecen.
func Shells() []Shell { return detect() }

// find busca una shell por id ("" = la predeterminada, o la primera que haya).
func find(shells []Shell, id string) (Shell, error) {
	if id == "" {
		id = DefaultID
	}
	for _, s := range shells {
		if s.ID == id {
			return s, nil
		}
	}
	if len(shells) > 0 && id == DefaultID {
		return shells[0], nil
	}
	return Shell{}, i18n.Err("app.thatTerminalIsNotAvailable")
}

// workDir devuelve dir si existe como carpeta; si no, la carpeta personal.
func workDir(dir string) string {
	if fi, err := os.Stat(dir); dir != "" && err == nil && fi.IsDir() {
		return dir
	}
	home, _ := os.UserHomeDir()
	return home
}

// Start abre la shell con ese id (vacío = la predeterminada) en dir, con la
// pantalla del tamaño indicado. Devuelve también la shell elegida.
func Start(id, dir string, cols, rows int) (Proc, Shell, error) {
	sh, err := find(detect(), id)
	if err != nil {
		return nil, Shell{}, err
	}
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 32
	}
	p, err := start(sh, workDir(dir), cols, rows)
	return p, sh, err
}

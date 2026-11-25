package remote

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"

	"osat/internal/applog"
	"osat/internal/i18n"
	"osat/internal/store"
	"osat/internal/vt"
)

// TmuxTarget es la shell persistente a la que se conecta una terminal (ver tmux.go).
type TmuxTarget struct {
	ID       string `json:"id"`                 // $N
	Name     string `json:"name,omitempty"`     // para el título de la pestaña
	ReadOnly bool   `json:"readOnly,omitempty"` // solo mirar
	Plain    bool   `json:"plain,omitempty"`    // varias ventanas o paneles: tmux attach común
	// Create: crearla (con Name) antes de conectarse; el id queda en SessionInfo.TmuxID.
	Create bool `json:"create,omitempty"`
}

// tmuxHistory es cuántas líneas del historial se cargan al conectarse.
const tmuxHistory = 2000

// tmuxCtl habla con un cliente de tmux en modo control conectado a una sesión
// de un solo panel. Al empezar pide el cursor y el contenido del panel; con eso
// arma la pantalla y recién después pasa la salida en vivo (%output). Lo que
// llega antes de la captura ya está en ella y se descarta.
type tmuxCtl struct {
	mu       sync.Mutex
	in       io.Writer
	target   string // '$N', entre comillas para el parser de comandos de tmux
	id       string
	readOnly bool
	send     func([]byte)
	rename   func(string)

	parser  controlParser
	pending []func(controlReply) // una por comando mandado, en orden (nil = no importa la respuesta)

	pane   string // %N, de la respuesta del display-message
	cursor [2]int // x, y
	height int
	alt    bool
	live   bool // ya se mostró la captura: la salida pasa
	exited bool
}

func newTmuxCtl(in io.Writer, id string, readOnly bool, send func([]byte), rename func(string)) *tmuxCtl {
	c := &tmuxCtl{in: in, id: id, target: "'" + id + "'", readOnly: readOnly, send: send, rename: rename}
	c.parser.onReply = c.reply
	c.parser.onNote = c.note
	return c
}

// command manda un comando y anota quién atiende su respuesta.
func (c *tmuxCtl) command(cmd string, onReply func(controlReply)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending = append(c.pending, onReply)
	_, _ = io.WriteString(c.in, cmd+"\n")
}

func (c *tmuxCtl) start(cols, rows int) {
	if !c.readOnly {
		c.command(fmt.Sprintf("refresh-client -C %d,%d", cols, rows), nil)
	}
	c.command("display-message -p -t "+c.target+" '#{pane_id} #{cursor_x} #{cursor_y} #{pane_height} #{alternate_on}'", c.gotCursor)
	c.command(fmt.Sprintf("capture-pane -p -e -S -%d -t %s", tmuxHistory, c.target), c.gotCapture)
}

func (c *tmuxCtl) gotCursor(r controlReply) {
	if r.err || len(r.lines) == 0 {
		return
	}
	f := strings.Fields(r.lines[0])
	if len(f) != 5 {
		return
	}
	c.pane = f[0]
	c.cursor[0], _ = strconv.Atoi(f[1])
	c.cursor[1], _ = strconv.Atoi(f[2])
	c.height, _ = strconv.Atoi(f[3])
	c.alt = f[4] == "1"
}

// gotCapture arma la pantalla: borra lo que hubiera (al reconectar), escribe el
// historial y las líneas visibles, y pone el cursor donde está en tmux.
func (c *tmuxCtl) gotCapture(r controlReply) {
	lines := r.lines
	var b strings.Builder
	b.WriteString("\x1b[2J\x1b[3J\x1b[H")
	if c.alt {
		// Un programa de pantalla completa (vim, top…): solo su pantalla, en la alternativa.
		b.WriteString("\x1b[?1049h\x1b[H")
		if c.height > 0 && len(lines) > c.height {
			lines = lines[len(lines)-c.height:]
		}
	}
	b.WriteString(strings.Join(lines, "\r\n"))
	fmt.Fprintf(&b, "\x1b[%d;%dH", c.cursor[1]+1, c.cursor[0]+1)
	c.send([]byte(b.String()))
	c.live = true
}

func (c *tmuxCtl) reply(r controlReply) {
	c.mu.Lock()
	var h func(controlReply)
	if len(c.pending) > 0 {
		h = c.pending[0]
		c.pending = c.pending[1:]
	}
	c.mu.Unlock()
	if h != nil {
		h(r)
	}
}

func (c *tmuxCtl) note(name string, args []byte) {
	switch name {
	case "output":
		pane, data, _ := strings.Cut(string(args), " ")
		if c.live && (c.pane == "" || pane == c.pane) {
			c.send(decodeOctal([]byte(data)))
		}
	case "session-renamed":
		id, newName, _ := strings.Cut(string(args), " ")
		if id == c.id && c.rename != nil {
			c.rename(newName)
		}
	case "exit":
		c.exited = true
	}
}

func (c *tmuxCtl) line(l []byte) { c.parser.feed(l) }

func (c *tmuxCtl) write(data []byte) {
	if c.readOnly {
		return
	}
	for _, cmd := range sendKeysCommands(c.target, data) {
		c.command(cmd, nil)
	}
}

func (c *tmuxCtl) resize(cols, rows int) {
	if c.readOnly {
		return
	}
	c.command(fmt.Sprintf("refresh-client -C %d,%d", cols, rows), nil)
}

// startTmux conecta una terminal a una shell persistente. Las de varias
// ventanas o paneles (Plain) van por startShell con un tmux attach común.
func (m *Manager) startTmux(sh *sharedClient, c store.Connection, t TmuxTarget, cols, rows int) (*Session, error) {
	if t.Create {
		id, name, err := tmuxCreate(sh.client, c, t.Name)
		if err != nil {
			return nil, err
		}
		t.ID, t.Name, t.Create, t.Plain, t.ReadOnly = id, name, false, false, false
	}
	if t.ID == "" || !strings.HasPrefix(t.ID, "$") {
		return nil, i18n.Err("remote.tmuxFailed", "id")
	}
	attach := "tmux attach-session"
	if t.ReadOnly {
		attach += " -f read-only,ignore-size"
	}
	attach += " -t " + shellQuote(t.ID)
	if t.Plain {
		s, err := m.startShellCmd(sh, c, cols, rows, false, attach)
		if err == nil {
			s.tmuxID = t.ID
			if t.Name != "" {
				s.title = t.Name
			}
		}
		return s, err
	}
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 32
	}
	sess, err := sh.client.NewSession()
	if err != nil {
		return nil, err
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		sess.Close()
		return nil, err
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		sess.Close()
		return nil, err
	}
	var errOut limitedBuffer
	errOut.max = 4 << 10
	sess.Stderr = &errOut
	if err := sess.Start("tmux -C " + strings.TrimPrefix(attach, "tmux ")); err != nil {
		sess.Close()
		return nil, err
	}

	title := t.Name
	if title == "" {
		title = c.Name
	}
	s := &Session{ID: uuid.NewString(), Kind: KindTerminal, ConnID: c.ID, name: c.Name, shared: sh, ssh: sess,
		ready: make(chan struct{}), screen: vt.New(cols, rows), title: title, tmuxID: t.ID,
		host: fmt.Sprintf("%s@%s:%d", c.User, c.Host, c.Port)}
	dataEvt := "term:data:" + s.ID
	logf := openSessionLog(currentOptions().LogDir, c.Name)
	send := func(b []byte) {
		s.screen.Write(b)
		logf.Write(b)
		m.emit(dataEvt, base64.StdEncoding.EncodeToString(b))
	}
	ctl := newTmuxCtl(stdin, t.ID, t.ReadOnly, send, func(name string) { m.emit("term:tmuxname:"+s.ID, name) })
	s.tmux = ctl
	s.stdin = ctl
	m.register(s)

	go func() {
		defer applog.Recover("startTmux: lectura de tmux")
		// Esperamos a que el frontend escuche, para no perder la pantalla inicial.
		select {
		case <-s.ready:
		case <-time.After(5 * time.Second):
		}
		ctl.start(cols, rows)
		r := bufio.NewReaderSize(stdout, 64<<10)
		for {
			line, err := r.ReadBytes('\n')
			if len(line) > 0 {
				ctl.line(line[:len(line)-boolInt(line[len(line)-1] == '\n')])
			}
			if err != nil {
				break
			}
		}
		err := sess.Wait()
		logf.Close()
		// Sin texto, la sesión ya no existe (exit, o la terminó alguien): la pestaña
		// lo avisa y no reintenta. Con texto, se cortó la conexión: reintenta.
		msg := ""
		if ctl.exited {
			// tmux avisa %exit tanto si la sesión terminó como si alguien nos desconectó.
			if l, err := tmuxList(sh.client); err != nil {
				msg = err.Error()
			} else if tmuxHas(l.Sessions, t.ID) {
				msg = i18n.T("remote.tmuxDetached")
			}
		} else if e := strings.TrimSpace(errOut.String()); e != "" {
			if !tmuxGone(e) {
				msg = e
			}
		} else if err != nil {
			var ee *ssh.ExitError
			if !errors.As(err, &ee) {
				msg = err.Error()
			}
		}
		if msg != "" {
			slog.Info("shell persistente desconectada", "conn", c.Name, "id", s.ID, "tmux", t.ID, "msg", msg)
		}
		m.emit("term:exit:"+s.ID, msg)
		m.Close(s.ID)
	}()
	return s, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Write hace de stdin de la sesión: lo que se escribe va al panel con send-keys
// (lo usan las teclas, el puente con Claude y los snippets).
func (c *tmuxCtl) Write(p []byte) (int, error) {
	c.write(p)
	return len(p), nil
}

func tmuxHas(list []TmuxSession, id string) bool {
	for _, s := range list {
		if s.ID == id {
			return true
		}
	}
	return false
}

// tmuxGone dice si el error de tmux attach es porque la sesión (o el servidor
// de tmux) ya no existe.
func tmuxGone(stderr string) bool {
	low := strings.ToLower(stderr)
	return strings.Contains(low, "can't find session") || strings.Contains(low, "no server running") ||
		strings.Contains(low, "no sessions") || strings.Contains(low, "error connecting to")
}

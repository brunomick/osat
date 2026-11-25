package remote

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/user"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"osat/internal/i18n"
	"osat/internal/store"
)

// Shells persistentes: sesiones de tmux en el servidor que sobreviven a que se
// cierre la pestaña. tmux queda por debajo; osat nunca muestra su interfaz.
// Las de un solo panel se abren en modo control (tmux -C, ver tmuxterm.go): tmux
// no dibuja nada y manda tal cual lo que escribe la shell. Las que tienen varias
// ventanas o paneles (armadas a mano) se abren con un tmux attach común.
//
// Todo lo demás (listar, crear, renombrar, terminar) va por canales de
// ejecución aparte, sin escribir en ninguna shell. Las sesiones se apuntan por
// su id ($N), que no cambia aunque otro la renombre.

// TmuxSession es una sesión de tmux del servidor, como la ve la barra lateral.
type TmuxSession struct {
	ID       string `json:"id"` // $N
	Name     string `json:"name"`
	Attached int    `json:"attached"` // clientes conectados (de cualquiera)
	Activity int64  `json:"activity"` // última actividad, en segundos Unix
	Windows  int    `json:"windows"`
	Panes    int    `json:"panes"` // de la ventana activa
	Owner    string `json:"owner"` // @osat_owner; vacío si no la creó osat
}

// tmuxListFormat es el formato de list-sessions, en el orden que espera
// parseTmuxSessions. Los campos van separados por | y no por tab: sin terminal,
// tmux cambia los tabs por _. El nombre va último porque puede tener |.
const tmuxListFormat = "#{session_id}|#{session_attached}|#{session_activity}|#{session_windows}|#{window_panes}|#{@osat_owner}|#{session_name}"

// parseTmuxSessions lee la salida de list-sessions con tmuxListFormat. Las
// líneas que no tienen la forma esperada se ignoran.
func parseTmuxSessions(out string) []TmuxSession {
	var list []TmuxSession
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(strings.TrimRight(line, "\r"), "|", 7)
		if len(f) != 7 || !strings.HasPrefix(f[0], "$") {
			continue
		}
		num := func(s string) int { n, _ := strconv.Atoi(s); return n }
		act, _ := strconv.ParseInt(f[2], 10, 64)
		list = append(list, TmuxSession{ID: f[0], Name: f[6], Attached: num(f[1]), Activity: act,
			Windows: num(f[3]), Panes: num(f[4]), Owner: f[5]})
	}
	return list
}

// parseTmuxVersion lee la salida de "tmux -V" ("tmux 3.0a", "tmux next-3.5").
// ok es false si no trae un número (p. ej. "tmux master").
func parseTmuxVersion(out string) (major, minor int, ok bool) {
	f := strings.Fields(out)
	if len(f) < 2 || f[0] != "tmux" {
		return 0, 0, false
	}
	v := f[1]
	if i := strings.LastIndex(v, "-"); i >= 0 {
		v = v[i+1:]
	}
	ma, rest, found := strings.Cut(v, ".")
	if !found {
		return 0, 0, false
	}
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	a, err1 := strconv.Atoi(ma)
	b, err2 := strconv.Atoi(rest[:end])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return a, b, true
}

// sendKeysChunk es cuántos bytes van en cada send-keys: tmux limita el largo
// de una línea de comando, y un pegado grande se parte en varios.
const sendKeysChunk = 512

// sendKeysCommands arma los send-keys -H (bytes en hexadecimal) que escriben
// data en el panel, tal cual, sin que tmux interprete nada.
func sendKeysCommands(pane string, data []byte) []string {
	var cmds []string
	for len(data) > 0 {
		n := min(len(data), sendKeysChunk)
		var b strings.Builder
		b.WriteString("send-keys -t " + pane + " -H")
		for _, c := range data[:n] {
			fmt.Fprintf(&b, " %02x", c)
		}
		cmds = append(cmds, b.String())
		data = data[n:]
	}
	return cmds
}

// decodeOctal deshace los escapes \ooo con que el modo control manda los bytes
// de %output (los de control y la barra). Trabaja por bytes: un carácter UTF-8
// partido entre dos %output pasa en dos pedazos y la terminal lo une.
func decodeOctal(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] == '\\' && i+3 < len(b) && isOctal(b[i+1]) && isOctal(b[i+2]) && isOctal(b[i+3]) {
			out = append(out, (b[i+1]-'0')<<6|(b[i+2]-'0')<<3|(b[i+3]-'0'))
			i += 3
			continue
		}
		out = append(out, b[i])
	}
	return out
}

func isOctal(c byte) bool { return c >= '0' && c <= '7' }

// controlReply es la respuesta a un comando mandado en modo control: las líneas
// entre %begin y %end (o %error, con err).
type controlReply struct {
	lines []string
	err   bool
}

// controlParser separa lo que manda tmux en modo control, línea por línea:
// respuestas a nuestros comandos (en orden) y notificaciones (%output, %exit…).
// Los bloques con flags 0 no son respuestas a comandos nuestros (p. ej. el del
// propio attach) y se descartan.
type controlParser struct {
	onReply func(controlReply)
	onNote  func(name string, args []byte)

	inBlock bool
	ours    bool
	lines   []string
}

func (p *controlParser) feed(line []byte) {
	line = bytes.TrimSuffix(line, []byte("\r"))
	if p.inBlock {
		if bytes.HasPrefix(line, []byte("%end ")) || bytes.HasPrefix(line, []byte("%error ")) {
			p.inBlock = false
			if p.ours && p.onReply != nil {
				p.onReply(controlReply{lines: p.lines, err: bytes.HasPrefix(line, []byte("%error "))})
			}
			p.lines = nil
			return
		}
		p.lines = append(p.lines, string(line))
		return
	}
	if bytes.HasPrefix(line, []byte("%begin ")) {
		f := bytes.Fields(line)
		p.inBlock = true
		p.ours = len(f) >= 4 && !bytes.Equal(f[3], []byte("0"))
		return
	}
	if len(line) < 2 || line[0] != '%' {
		return
	}
	name, args, _ := bytes.Cut(line[1:], []byte(" "))
	if p.onNote != nil {
		p.onNote(string(name), args)
	}
}

// TmuxList es lo que hay de tmux en un servidor.
type TmuxList struct {
	Version  string        `json:"version"`  // "3.4"
	ReadOnly bool          `json:"readOnly"` // se puede entrar a solo mirar (tmux 3.1 o más nuevo)
	Me       string        `json:"me"`       // el dueño que pone este osat (ver tmuxOwner): las sesiones "mías"
	Sessions []TmuxSession `json:"sessions"`
}

const tmuxTimeout = 10 * time.Second

var errTmuxMissing = i18n.Err("remote.tmuxMissing")

// tmuxAtLeast dice si la versión de tmux es major.minor o más nueva. Una que no
// trae número ("master") se toma como nueva.
func tmuxAtLeast(version string, major, minor int) bool {
	ma, mi, ok := parseTmuxVersion("tmux " + version)
	return !ok || ma > major || (ma == major && mi >= minor)
}

// tmuxListScript imprime la versión y después las sesiones. Sin servidor de
// tmux corriendo (ninguna sesión) list-sessions falla: es una lista vacía.
func tmuxListScript() string {
	return "command -v tmux >/dev/null 2>&1 || exit 127; tmux -V; tmux list-sessions -F " + shellQuote(tmuxListFormat) + " 2>/dev/null; exit 0"
}

// tmuxCreateScript crea una sesión sin conectarse a ella e imprime su id y el
// nombre que quedó, separados por un espacio. Sin nombre, tmux elige uno. Las variables de la conexión van
// con -e solo si tmux lo admite (3.2 o más nuevo).
func tmuxCreateScript(name string, env []string, withEnv bool) string {
	s := "tmux new-session -d -P -F '#{session_id} #{session_name}'"
	if name != "" {
		s += " -s " + shellQuote(name)
	}
	if withEnv {
		for _, e := range env {
			s += " -e " + shellQuote(e)
		}
	}
	return s
}

// tmuxRenameScript renombra e imprime el nombre que quedó (tmux cambia . y :).
func tmuxRenameScript(id, name string) string {
	return "tmux rename-session -t " + shellQuote(id) + " -- " + shellQuote(name) +
		" && tmux display-message -p -t " + shellQuote(id) + " '#{session_name}'"
}

func tmuxKillScript(id string) string { return "tmux kill-session -t " + shellQuote(id) }

// tmuxOwnerFrom arma el dueño que se guarda en @osat_owner: usuario@equipo.
func tmuxOwnerFrom(username, host string) string {
	if i := strings.LastIndexAny(username, `\/`); i >= 0 {
		username = username[i+1:] // en Windows viene como DOMINIO\usuario
	}
	// | separa los campos de la lista (ver tmuxListFormat).
	username, host = strings.ReplaceAll(username, "|", ""), strings.ReplaceAll(host, "|", "")
	switch {
	case username != "" && host != "":
		return username + "@" + host
	case username != "":
		return username
	case host != "":
		return host
	}
	return "osat"
}

func tmuxOwner() string {
	name := ""
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	host, _ := os.Hostname()
	return tmuxOwnerFrom(name, host)
}

// parseTmuxList lee la salida de tmuxListScript.
func parseTmuxList(out string) (TmuxList, error) {
	first, rest, _ := strings.Cut(out, "\n")
	version := strings.TrimPrefix(strings.TrimSpace(first), "tmux ")
	if !tmuxAtLeast(version, 3, 0) {
		return TmuxList{}, i18n.Err("remote.tmuxOld", version)
	}
	return TmuxList{Version: version, ReadOnly: tmuxAtLeast(version, 3, 1), Me: tmuxOwner(), Sessions: parseTmuxSessions(rest)}, nil
}

// tmuxExec corre un script de sh en un canal aparte y devuelve lo que imprimió.
// Si tmux no está instalado, errTmuxMissing; si falla, la primera línea de su error.
func tmuxExec(c *ssh.Client, script string) (string, error) {
	sess, err := c.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	var out, errOut limitedBuffer
	out.max, errOut.max = statsMaxOutput, 4<<10
	sess.Stdout, sess.Stderr = &out, &errOut
	if err := sess.Start("sh -c " + shellQuote(script)); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- sess.Wait() }()
	timer := time.NewTimer(tmuxTimeout)
	defer timer.Stop()
	select {
	case err = <-done:
	case <-timer.C:
		_ = sess.Close()
		return "", i18n.Err("remote.tmuxFailed", "timeout")
	}
	if err != nil {
		var ee *ssh.ExitError
		if errors.As(err, &ee) {
			if ee.ExitStatus() == 127 {
				return "", errTmuxMissing
			}
			msg, _, _ := strings.Cut(strings.TrimSpace(errOut.String()), "\n")
			if msg == "" {
				msg = err.Error()
			}
			return "", i18n.Err("remote.tmuxFailed", msg)
		}
		return "", err
	}
	return out.String(), nil
}

func tmuxList(c *ssh.Client) (TmuxList, error) {
	out, err := tmuxExec(c, tmuxListScript())
	if err != nil {
		return TmuxList{}, err
	}
	return parseTmuxList(out)
}

// TmuxSessions lista las sesiones de tmux por la conexión de una pestaña abierta.
func (m *Manager) TmuxSessions(sessionID string) (TmuxList, error) {
	var l TmuxList
	err := m.withClient(sessionID, func(c *ssh.Client) (err error) {
		l, err = tmuxList(c)
		return err
	})
	return l, err
}

// TmuxSessionsDial conecta, lista y cierra: el botón Actualizar de la barra
// lateral cuando no hay ninguna pestaña abierta con ese servidor.
func (m *Manager) TmuxSessionsDial(req ConnectRequest) (TmuxList, error) {
	client, _, err := m.dial(req)
	if err != nil {
		return TmuxList{}, err
	}
	defer client.Close()
	return tmuxList(client)
}

// tmuxCreate crea una shell persistente (sin conectarse a ella) con el dueño y
// el comando inicial de la conexión, y devuelve su id ($N) y el nombre que quedó.
func tmuxCreate(c *ssh.Client, conn store.Connection, name string) (id, got string, err error) {
	l, err := tmuxList(c)
	if err != nil {
		return "", "", err
	}
	out, err := tmuxExec(c, tmuxCreateScript(strings.TrimSpace(name), conn.Env, tmuxAtLeast(l.Version, 3, 2)))
	if err != nil {
		return "", "", err
	}
	id, got, _ = strings.Cut(strings.TrimSpace(out), " ")
	if !strings.HasPrefix(id, "$") {
		return "", "", i18n.Err("remote.tmuxFailed", strings.TrimSpace(out))
	}
	q := shellQuote(id)
	if _, err := tmuxExec(c, "tmux set-option -t "+q+" @osat_owner "+shellQuote(tmuxOwner())); err != nil {
		return "", "", err
	}
	if conn.InitialCommand != "" {
		if _, err := tmuxExec(c, "tmux send-keys -t "+q+" -l -- "+shellQuote(conn.InitialCommand)+" && tmux send-keys -t "+q+" Enter"); err != nil {
			return "", "", err
		}
	}
	return id, got, nil
}

// TmuxRename renombra una sesión y devuelve el nombre que quedó.
func (m *Manager) TmuxRename(sessionID, id, name string) (string, error) {
	var got string
	err := m.withClient(sessionID, func(c *ssh.Client) error {
		out, err := tmuxExec(c, tmuxRenameScript(id, name))
		got = strings.TrimSpace(out)
		return err
	})
	return got, err
}

// TmuxKill termina una sesión (y todo lo que corre en ella).
func (m *Manager) TmuxKill(sessionID, id string) error {
	return m.withClient(sessionID, func(c *ssh.Client) error {
		_, err := tmuxExec(c, tmuxKillScript(id))
		return err
	})
}

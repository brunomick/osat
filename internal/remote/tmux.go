package remote

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
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
	ID       string `json:"id"`       // $N
	Name     string `json:"name"`
	Attached int    `json:"attached"` // clientes conectados (de cualquiera)
	Activity int64  `json:"activity"` // última actividad, en segundos Unix
	Windows  int    `json:"windows"`
	Panes    int    `json:"panes"` // de la ventana activa
	Owner    string `json:"owner"` // @osat_owner; vacío si no la creó osat
}

// tmuxListFormat es el formato de list-sessions y new-session -P: un campo por
// tab, en el orden que espera parseTmuxSessions.
const tmuxListFormat = "#{session_id}\t#{session_name}\t#{session_attached}\t#{session_activity}\t#{session_windows}\t#{window_panes}\t#{@osat_owner}"

// parseTmuxSessions lee la salida de list-sessions con tmuxListFormat. Las
// líneas que no tienen la forma esperada se ignoran.
func parseTmuxSessions(out string) []TmuxSession {
	var list []TmuxSession
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(f) != 7 || !strings.HasPrefix(f[0], "$") {
			continue
		}
		num := func(s string) int { n, _ := strconv.Atoi(s); return n }
		act, _ := strconv.ParseInt(f[3], 10, 64)
		list = append(list, TmuxSession{ID: f[0], Name: f[1], Attached: num(f[2]), Activity: act,
			Windows: num(f[4]), Panes: num(f[5]), Owner: f[6]})
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

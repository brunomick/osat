// Package vt es un emulador de pantalla de terminal mínimo: recibe los bytes
// que imprime una shell (con sus secuencias de escape) y mantiene lo que se
// vería en pantalla más el historial que se fue de ella. Sirve para leer una
// terminal como texto, no para dibujarla: ignora colores y estilos.
package vt

import (
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const maxHistory = 5000

const (
	stGround = iota
	stEsc
	stCSI
	stOSC    // OSC y otras cadenas: hasta BEL o ESC \
	stOSCEsc // vimos ESC dentro de una cadena
	stSkip1  // ESC ( ) * +: un byte más
)

// Screen es la pantalla de una terminal. Es seguro usarla desde varias goroutines.
type Screen struct {
	mu         sync.Mutex
	cols, rows int
	grid       [][]rune
	cx, cy     int
	wrap       bool // el cursor está pasado el último casillero: el próximo carácter salta de línea
	top, bot   int  // región de scroll (inclusive)
	savedX     int
	savedY     int

	hist     []string
	histBase int // número de línea absoluto de hist[0]

	alt      bool
	mainGrid [][]rune
	mainX    int
	mainY    int

	state  int
	params []int
	cur    int
	hasCur bool
	priv   bool
	utf    []byte

	writes    uint64
	lastWrite time.Time
}

// New crea una pantalla de cols×rows.
func New(cols, rows int) *Screen {
	if cols < 1 {
		cols = 80
	}
	if rows < 1 {
		rows = 24
	}
	s := &Screen{cols: cols, rows: rows}
	s.grid = s.blank(rows)
	s.bot = rows - 1
	return s
}

func (s *Screen) blank(rows int) [][]rune {
	g := make([][]rune, rows)
	for i := range g {
		g[i] = s.blankRow()
	}
	return g
}

func (s *Screen) blankRow() []rune {
	r := make([]rune, s.cols)
	for i := range r {
		r[i] = ' '
	}
	return r
}

// Writes cuenta las veces que se escribió y LastWrite dice cuándo fue la última:
// sirven para saber si una terminal sigue imprimiendo.
func (s *Screen) Writes() (uint64, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes, s.lastWrite
}

// Write procesa lo que imprimió la shell.
func (s *Screen) Write(p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes++
	s.lastWrite = time.Now()
	for _, b := range p {
		s.feed(b)
	}
}

func (s *Screen) feed(b byte) {
	switch s.state {
	case stEsc:
		s.esc(b)
	case stCSI:
		s.csiByte(b)
	case stOSC:
		switch b {
		case 0x07:
			s.state = stGround
		case 0x1b:
			s.state = stOSCEsc
		}
	case stOSCEsc:
		if b == '\\' || b == 0x07 {
			s.state = stGround
		} else {
			s.state = stOSC
		}
	case stSkip1:
		s.state = stGround
	default:
		s.ground(b)
	}
}

func (s *Screen) ground(b byte) {
	if len(s.utf) > 0 || b >= 0x80 {
		s.utf = append(s.utf, b)
		if !utf8.FullRune(s.utf) {
			return
		}
		r, _ := utf8.DecodeRune(s.utf)
		s.utf = s.utf[:0]
		s.put(r)
		return
	}
	switch b {
	case 0x1b:
		s.state = stEsc
	case '\n', 0x0b, 0x0c:
		s.index()
	case '\r':
		s.cx, s.wrap = 0, false
	case 0x08:
		if s.cx > 0 {
			s.cx--
		}
		s.wrap = false
	case '\t':
		s.cx = min((s.cx/8+1)*8, s.cols-1)
		s.wrap = false
	default:
		if b >= 0x20 && b != 0x7f {
			s.put(rune(b))
		}
	}
}

func (s *Screen) put(r rune) {
	if s.wrap {
		s.cx, s.wrap = 0, false
		s.index()
	}
	s.grid[s.cy][s.cx] = r
	if s.cx == s.cols-1 {
		s.wrap = true
	} else {
		s.cx++
	}
}

// index baja una línea (con scroll al llegar al fondo de la región).
func (s *Screen) index() {
	s.wrap = false
	if s.cy == s.bot {
		s.scrollUp(1)
	} else if s.cy < s.rows-1 {
		s.cy++
	}
}

func (s *Screen) reverseIndex() {
	s.wrap = false
	if s.cy == s.top {
		s.scrollDown(1)
	} else if s.cy > 0 {
		s.cy--
	}
}

// scrollUp sube el contenido de la región; lo que sale por arriba pasa al
// historial si la región es toda la pantalla.
func (s *Screen) scrollUp(n int) {
	for ; n > 0; n-- {
		if s.top == 0 && !s.alt {
			s.pushHistory(s.grid[0])
		}
		copy(s.grid[s.top:s.bot], s.grid[s.top+1:s.bot+1])
		s.grid[s.bot] = s.blankRow()
	}
}

func (s *Screen) scrollDown(n int) {
	for ; n > 0; n-- {
		copy(s.grid[s.top+1:s.bot+1], s.grid[s.top:s.bot])
		s.grid[s.top] = s.blankRow()
	}
}

func (s *Screen) pushHistory(row []rune) {
	s.hist = append(s.hist, strings.TrimRight(string(row), " "))
	if len(s.hist) > maxHistory {
		drop := len(s.hist) - maxHistory
		s.hist = append([]string(nil), s.hist[drop:]...)
		s.histBase += drop
	}
}

func (s *Screen) esc(b byte) {
	s.state = stGround
	switch b {
	case '[':
		s.state, s.params, s.cur, s.hasCur, s.priv = stCSI, s.params[:0], 0, false, false
	case ']', 'P', '^', '_', 'X':
		s.state = stOSC
	case '(', ')', '*', '+':
		s.state = stSkip1
	case '7':
		s.savedX, s.savedY = s.cx, s.cy
	case '8':
		s.cx, s.cy, s.wrap = min(s.savedX, s.cols-1), min(s.savedY, s.rows-1), false
	case 'M':
		s.reverseIndex()
	case 'D':
		s.index()
	case 'E':
		s.cx = 0
		s.index()
	case 'c':
		s.reset()
	}
}

func (s *Screen) reset() {
	s.grid = s.blank(s.rows)
	s.cx, s.cy, s.wrap, s.top, s.bot = 0, 0, false, 0, s.rows-1
}

func (s *Screen) csiByte(b byte) {
	switch {
	case b >= '0' && b <= '9':
		s.cur, s.hasCur = s.cur*10+int(b-'0'), true
	case b == ';':
		s.params = append(s.params, s.cur)
		s.cur, s.hasCur = 0, false
	case b == '?' || b == '>' || b == '<' || b == '=':
		s.priv = b == '?'
	case b >= 0x20 && b <= 0x2f: // intermedios: se ignoran
	case b >= 0x40 && b <= 0x7e:
		if s.hasCur || len(s.params) > 0 {
			s.params = append(s.params, s.cur)
		}
		s.state = stGround
		s.csi(b)
	default:
		s.state = stGround
	}
}

// param devuelve el parámetro i, o def si falta o es 0.
func (s *Screen) param(i, def int) int {
	if i < len(s.params) && s.params[i] > 0 {
		return s.params[i]
	}
	return def
}

func (s *Screen) csi(final byte) {
	n := s.param(0, 1)
	s.wrap = false
	switch final {
	case 'A':
		s.cy = max(s.cy-n, 0)
	case 'B':
		s.cy = min(s.cy+n, s.rows-1)
	case 'C':
		s.cx = min(s.cx+n, s.cols-1)
	case 'D':
		s.cx = max(s.cx-n, 0)
	case 'E':
		s.cy, s.cx = min(s.cy+n, s.rows-1), 0
	case 'F':
		s.cy, s.cx = max(s.cy-n, 0), 0
	case 'G', '`':
		s.cx = min(n-1, s.cols-1)
	case 'd':
		s.cy = min(n-1, s.rows-1)
	case 'H', 'f':
		s.cy, s.cx = min(s.param(0, 1)-1, s.rows-1), min(s.param(1, 1)-1, s.cols-1)
	case 'J':
		s.eraseDisplay(s.paramZero(0))
	case 'K':
		s.eraseLine(s.paramZero(0))
	case 'L':
		s.insertLines(n)
	case 'M':
		s.deleteLines(n)
	case 'P':
		row := s.grid[s.cy]
		n = min(n, s.cols-s.cx)
		copy(row[s.cx:], row[s.cx+n:])
		for i := s.cols - n; i < s.cols; i++ {
			row[i] = ' '
		}
	case '@':
		row := s.grid[s.cy]
		n = min(n, s.cols-s.cx)
		copy(row[s.cx+n:], row[s.cx:s.cols-n])
		for i := s.cx; i < s.cx+n; i++ {
			row[i] = ' '
		}
	case 'X':
		for i := s.cx; i < min(s.cx+n, s.cols); i++ {
			s.grid[s.cy][i] = ' '
		}
	case 'S':
		s.scrollUp(n)
	case 'T':
		s.scrollDown(n)
	case 'r':
		t, b := s.param(0, 1)-1, s.param(1, s.rows)-1
		if t >= 0 && b < s.rows && t < b {
			s.top, s.bot = t, b
		} else {
			s.top, s.bot = 0, s.rows-1
		}
		s.cx, s.cy = 0, 0
	case 's':
		s.savedX, s.savedY = s.cx, s.cy
	case 'u':
		s.cx, s.cy = min(s.savedX, s.cols-1), min(s.savedY, s.rows-1)
	case 'h', 'l':
		if s.priv {
			for _, p := range s.params {
				if p == 47 || p == 1047 || p == 1049 {
					s.setAlt(final == 'h')
				}
			}
		}
	}
}

// paramZero devuelve el parámetro i tal cual (0 si falta): ED y EL usan el 0 como valor.
func (s *Screen) paramZero(i int) int {
	if i < len(s.params) {
		return s.params[i]
	}
	return 0
}

func (s *Screen) eraseDisplay(mode int) {
	switch mode {
	case 0:
		s.eraseLine(0)
		for y := s.cy + 1; y < s.rows; y++ {
			s.grid[y] = s.blankRow()
		}
	case 1:
		s.eraseLine(1)
		for y := 0; y < s.cy; y++ {
			s.grid[y] = s.blankRow()
		}
	default:
		for y := 0; y < s.rows; y++ {
			s.grid[y] = s.blankRow()
		}
		if mode == 3 {
			s.hist, s.histBase = nil, s.histBase+len(s.hist)
		}
	}
}

func (s *Screen) eraseLine(mode int) {
	row := s.grid[s.cy]
	from, to := s.cx, s.cols // 0: del cursor al final
	switch mode {
	case 1:
		from, to = 0, s.cx+1
	case 2:
		from, to = 0, s.cols
	}
	for i := from; i < min(to, s.cols); i++ {
		row[i] = ' '
	}
}

func (s *Screen) insertLines(n int) {
	if s.cy < s.top || s.cy > s.bot {
		return
	}
	save := s.top
	s.top = s.cy
	s.scrollDown(min(n, s.bot-s.cy+1))
	s.top = save
}

func (s *Screen) deleteLines(n int) {
	if s.cy < s.top || s.cy > s.bot {
		return
	}
	save, hist := s.top, s.alt
	s.top, s.alt = s.cy, true // lo que se borra no pasa al historial
	s.scrollUp(min(n, s.bot-s.cy+1))
	s.top, s.alt = save, hist
}

// setAlt entra o sale de la pantalla alternativa (vim, less, top...).
func (s *Screen) setAlt(on bool) {
	if on == s.alt {
		return
	}
	if on {
		s.mainGrid, s.mainX, s.mainY = s.grid, s.cx, s.cy
		s.grid = s.blank(s.rows)
		s.alt = true
		return
	}
	s.alt = false
	if len(s.mainGrid) == s.rows {
		s.grid, s.cx, s.cy = s.mainGrid, min(s.mainX, s.cols-1), s.mainY
	} else {
		s.grid = s.blank(s.rows)
	}
	s.mainGrid = nil
	s.top, s.bot, s.wrap = 0, s.rows-1, false
}

// Resize cambia el tamaño de la pantalla (cuando cambia la ventana de la terminal).
func (s *Screen) Resize(cols, rows int) {
	if cols < 1 || rows < 1 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cols == s.cols && rows == s.rows {
		return
	}
	// Si hay menos filas, lo de arriba pasa al historial para no perderlo.
	for len(s.grid) > rows {
		if !s.alt {
			s.pushHistory(s.grid[0])
		}
		s.grid = s.grid[1:]
		s.cy = max(s.cy-1, 0)
	}
	s.cols = cols
	for i := range s.grid {
		row := make([]rune, cols)
		for j := range row {
			row[j] = ' '
			if j < len(s.grid[i]) {
				row[j] = s.grid[i][j]
			}
		}
		s.grid[i] = row
	}
	for len(s.grid) < rows {
		s.grid = append(s.grid, s.blankRow())
	}
	s.rows = rows
	s.cx, s.cy, s.wrap = min(s.cx, cols-1), min(s.cy, rows-1), false
	s.top, s.bot = 0, rows-1
	s.mainGrid = nil
}

// Text devuelve las últimas n líneas (historial + pantalla), sin espacios a la
// derecha ni líneas vacías al final. n <= 0 devuelve todo.
func (s *Screen) Text(n int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return lastLines(s.linesFrom(s.histBase), n)
}

// Mark devuelve la posición (línea absoluta) del cursor: sirve para pedir
// después solo lo que se imprimió desde entonces con Since.
func (s *Screen) Mark() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.histBase + len(s.hist) + s.cy
}

// InAltScreen dice si hay un programa de pantalla completa (vim, less, top...)
// usando la pantalla alternativa.
func (s *Screen) InAltScreen() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.alt
}

// CursorLine devuelve el texto de la línea donde está el cursor, y Prompt lo
// que hay en ella antes del cursor (típicamente el prompt de la shell).
func (s *Screen) CursorLine() (line, before string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.grid[s.cy]
	return strings.TrimRight(string(row), " "), strings.TrimRight(string(row[:s.cx]), " ")
}

// Since devuelve el texto desde la línea marcada (incluida) hasta el final.
func (s *Screen) Since(mark int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.linesFrom(mark), "\n")
}

// linesFrom junta las líneas desde la absoluta from. Con la pantalla
// alternativa solo cuenta la pantalla.
func (s *Screen) linesFrom(from int) []string {
	var lines []string
	if !s.alt {
		lines = append(lines, s.hist...)
	}
	base := s.histBase
	if s.alt {
		base += len(s.hist)
	}
	for _, row := range s.grid {
		lines = append(lines, strings.TrimRight(string(row), " "))
	}
	if from > base {
		lines = lines[min(from-base, len(lines)):]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func lastLines(lines []string, n int) string {
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

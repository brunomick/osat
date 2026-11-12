package remote

import (
	"bytes"
	"strings"
	"sync"
	"time"
)

// Arranque limpio de las terminales. Cada servidor imprime lo suyo al entrar
// (MOTD, "Last login", novedades de actualizaciones) y tiene su propio prompt.
// Para que todas arranquen igual, apenas se abre la shell se le escribe una
// línea que fija un prompt común e imprime una marca invisible; todo lo que el
// servidor muestra antes de esa marca se descarta y la pantalla empieza en el
// prompt. Si la marca no llega (Windows, fish, un equipo de red...) se muestra
// la salida tal cual.

// cleanStartMarker es una secuencia OSC (no se dibuja). El comando la arma con
// printf y escapes octales, así que el eco de lo tipeado no la contiene.
var cleanStartMarker = []byte("\x1b]777;osat-ready\x07")

// cleanStartInput se tipea en la shell recién abierta. Empieza con un espacio
// para que no quede en el historial. Compatible con bash, zsh y sh; en
// cualquier otra shell falla y se cae al comportamiento normal.
const cleanStartInput = ` if [ -n "$BASH_VERSION" ]; then PS1='\[\e[90m\]\u@\h\[\e[0m\]:\[\e[1m\]\w\[\e[0m\]\$ '; ` +
	`elif [ -n "$ZSH_VERSION" ]; then PS1=$'%{\e[90m%}%n@%m%{\e[0m%}:%{\e[1m%}%~%{\e[0m%}%# '; fi; ` +
	`printf '\033]777;osat-ready\007'` + "\r"

// clearScreen borra la pantalla y el historial de desplazamiento.
const clearScreen = "\x1b[3J\x1b[2J\x1b[H"

// cleanStartEnabled dice si vale la pena intentar el arranque limpio con este
// servidor (por su banner de versión SSH). Windows no tiene shell POSIX.
func cleanStartEnabled(serverVersion string) bool {
	return !strings.Contains(strings.ToLower(serverVersion), "windows")
}

// cleanStart filtra la salida de la shell hasta ver la marca de arranque.
type cleanStart struct {
	emit    func([]byte)
	silence time.Duration // sin recibir nada por tanto tiempo: se muestra lo que haya
	maxWait time.Duration // tope total esperando la marca
	// onDone (opcional) se llama una sola vez, cuando el arranque se resuelve
	// (con la marca o sin ella). Lo que devuelve (los avisos en gris) se
	// muestra después del borrado de pantalla y antes del resto de la salida.
	onDone func() []byte
	// keepScreen: al ver la marca no se borra la pantalla ni el historial
	// (reconexión: queda lo que había antes de que se cortara).
	keepScreen bool

	mu    sync.Mutex
	done  bool // ya se resolvió (marca vista o tiempo agotado)
	late  bool // se resolvió sin marca: si llega después, se descarta
	buf   []byte
	timer *time.Timer
	total *time.Timer
}

func newCleanStart(emit func([]byte)) *cleanStart {
	return &cleanStart{emit: emit, silence: 3 * time.Second, maxWait: 10 * time.Second}
}

// start arranca los relojes; llamarlo cuando ya se empieza a leer la salida.
func (c *cleanStart) start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done {
		return
	}
	c.timer = time.AfterFunc(c.silence, c.giveUp)
	c.total = time.AfterFunc(c.maxWait, c.giveUp)
}

// write recibe lo que imprime la shell.
func (c *cleanStart) write(p []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done {
		if c.late {
			p = bytes.ReplaceAll(p, cleanStartMarker, nil)
		}
		if len(p) > 0 {
			c.emit(p)
		}
		return
	}
	c.buf = append(c.buf, p...)
	if i := bytes.Index(c.buf, cleanStartMarker); i >= 0 {
		rest := c.buf[i+len(cleanStartMarker):]
		notices := c.finish()
		var out []byte
		if !c.keepScreen {
			out = []byte(clearScreen)
		}
		c.emit(append(append(out, notices...), rest...))
		return
	}
	if c.timer != nil {
		c.timer.Reset(c.silence)
	}
}

// giveUp muestra lo recibido hasta ahora: la marca no va a llegar.
func (c *cleanStart) giveUp() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done {
		return
	}
	buf := c.buf
	notices := c.finish()
	c.late = true
	if out := append(notices, buf...); len(out) > 0 {
		c.emit(out)
	}
}

// finish deja de filtrar y devuelve lo que dio onDone. Con c.mu tomado.
func (c *cleanStart) finish() []byte {
	c.done = true
	c.buf = nil
	if c.timer != nil {
		c.timer.Stop()
	}
	if c.total != nil {
		c.total.Stop()
	}
	if c.onDone != nil {
		return c.onDone()
	}
	return nil
}

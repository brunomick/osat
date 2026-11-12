package remote

import (
	"sync"
	"time"
)

// Options son los ajustes de las terminales que usa el backend (ver settings.Terminal).
// Valen para las conexiones y terminales que se abren después de cambiarlos.
type Options struct {
	KeepAlive time.Duration // cada cuánto se le pregunta al servidor si sigue ahí
	TermType  string        // TERM que se pide para la PTY
	LogDir    string        // carpeta donde se guarda lo que muestra cada terminal; vacío = no se guarda
}

var (
	optMu sync.Mutex
	opts  = Options{KeepAlive: keepAliveInterval, TermType: "xterm-256color"}
)

// SetOptions cambia los ajustes de las terminales. Un valor vacío deja el de siempre.
func SetOptions(o Options) {
	if o.KeepAlive <= 0 {
		o.KeepAlive = keepAliveInterval
	}
	if o.TermType == "" {
		o.TermType = "xterm-256color"
	}
	optMu.Lock()
	opts = o
	optMu.Unlock()
}

func currentOptions() Options {
	optMu.Lock()
	defer optMu.Unlock()
	return opts
}

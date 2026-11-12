package remote

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// sessionLog guarda en un archivo de texto lo que muestra una terminal (ajuste
// Terminal.SessionLog), sin las secuencias de escape: ni colores ni movimientos
// del cursor. Un *sessionLog nil no hace nada, así quien escribe no pregunta.
type sessionLog struct {
	mu    sync.Mutex
	f     *os.File
	state int // dónde quedó una secuencia de escape cortada entre dos lecturas
}

// Estados del filtro de secuencias de escape.
const (
	logText   = iota
	logEsc    // vino ESC
	logCSI    // ESC [ … hasta un byte final (@ a ~)
	logString // ESC ] (OSC), ESC P (DCS)…: hasta BEL o ESC \
	logStrEsc // ESC dentro de una cadena: si sigue \, terminó
	logSkip1  // ESC ( y parecidos: se saltea un byte más
)

var unsafeName = regexp.MustCompile(`[^\p{L}\p{N}._-]+`)

// openSessionLog crea dir/<nombre>_<fecha>.log. Con dir vacío (ajuste apagado)
// o si no se puede crear, devuelve nil y la terminal sigue sin registro.
func openSessionLog(dir, name string) *sessionLog {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.Warn("no se pudo crear la carpeta de registros de sesión", "dir", dir, "err", err)
		return nil
	}
	base := unsafeName.ReplaceAllString(name, "_") + "_" + time.Now().Format("2006-01-02_15-04-05")
	for i := 1; i < 100; i++ {
		file := base + ".log"
		if i > 1 {
			file = fmt.Sprintf("%s_%d.log", base, i) // otra terminal del mismo servidor abierta en el mismo segundo
		}
		f, err := os.OpenFile(filepath.Join(dir, file), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			slog.Warn("no se pudo crear el registro de sesión", "dir", dir, "err", err)
			return nil
		}
		return &sessionLog{f: f}
	}
	return nil
}

func (l *sessionLog) Write(b []byte) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return
	}
	_, _ = l.f.Write(l.strip(b))
}

func (l *sessionLog) Close() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		l.f.Close()
		l.f = nil
	}
}

// strip saca las secuencias de escape y los caracteres de control (salvo el
// salto de línea y el tab). Recuerda el estado entre llamadas: una secuencia
// puede llegar cortada en dos lecturas.
func (l *sessionLog) strip(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		switch l.state {
		case logText:
			switch {
			case c == 0x1b:
				l.state = logEsc
			case c == '\n' || c == '\t' || (c >= 0x20 && c != 0x7f):
				out = append(out, c)
			}
		case logEsc:
			switch c {
			case '[':
				l.state = logCSI
			case ']', 'P', '_', '^', 'X':
				l.state = logString
			case '(', ')', '*', '+', '#', '%':
				l.state = logSkip1
			default:
				l.state = logText
			}
		case logCSI:
			if c >= 0x40 && c <= 0x7e {
				l.state = logText
			}
		case logString:
			if c == 0x07 {
				l.state = logText
			} else if c == 0x1b {
				l.state = logStrEsc
			}
		case logStrEsc:
			if c == '\\' {
				l.state = logText
			} else {
				l.state = logString
			}
		case logSkip1:
			l.state = logText
		}
	}
	return out
}

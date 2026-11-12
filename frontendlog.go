package main

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

const (
	frontendMaxPerMinute = 50   // errores del frontend que se registran por minuto
	frontendMessageMax   = 2000 // bytes del mensaje que se guardan
	frontendStackMax     = 8000 // bytes de la traza que se guardan
)

// frontendLog registra los errores de JavaScript que nadie atrapó, con un tope por
// minuto: si el frontend entra en un bucle de errores no llena el log. Los que
// pasan del tope se cuentan y se registran juntos al cambiar de minuto o al cerrar.
type frontendLog struct {
	mu      sync.Mutex
	now     func() time.Time // time.Now si es nil (los tests lo reemplazan)
	started time.Time        // comienzo del minuto en curso
	count   int
	skipped int
}

func (f *frontendLog) record(message, stack string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	if f.now != nil {
		now = f.now()
	}
	if now.Sub(f.started) >= time.Minute {
		f.flushLocked()
		f.started, f.count = now, 0
	}
	if f.count >= frontendMaxPerMinute {
		f.skipped++
		return
	}
	f.count++
	slog.Error("error del frontend", "message", clip(message, frontendMessageMax), "stack", clip(stack, frontendStackMax))
}

// flush registra los errores omitidos que todavía no se informaron.
func (f *frontendLog) flush() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.flushLocked()
}

func (f *frontendLog) flushLocked() {
	if f.skipped > 0 {
		slog.Warn(fmt.Sprintf("%d errores del frontend más omitidos", f.skipped))
		f.skipped = 0
	}
}

// clip recorta s a n bytes (sin cortar un carácter por la mitad).
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "…"
}

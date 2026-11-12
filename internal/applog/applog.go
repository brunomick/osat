// Package applog escribe el log de osat (logs/osat.log) con log/slog:
// archivo rotativo, filtro de secretos y utilidades para diagnóstico.
package applog

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
)

const logFile = "osat.log"

var (
	level slog.LevelVar // nivel actual; por defecto Info

	mu      sync.Mutex
	writer  *rotWriter
	logPath string
)

// Setup crea dir/logs, abre el archivo rotativo y deja un logger como el predeterminado de slog
// (así el paquete log estándar también termina en el archivo, ya filtrado).
func Setup(dir string, verbose bool) (*slog.Logger, error) {
	logs := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(logs, logFile)
	w, err := newRotWriter(path, maxLogSize, keepOld)
	if err != nil {
		return nil, err
	}
	SetVerbose(verbose)

	mu.Lock()
	if writer != nil {
		writer.Close()
	}
	writer, logPath = w, path
	mu.Unlock()

	h := slog.NewTextHandler(w, &slog.HandlerOptions{Level: &level})
	logger := slog.New(secretHandler{h})
	slog.SetDefault(logger)
	return logger, nil
}

// Close cierra el archivo del log (los tests lo usan para poder borrar la carpeta temporal).
func Close() {
	mu.Lock()
	defer mu.Unlock()
	if writer != nil {
		writer.Close()
	}
	writer, logPath = nil, ""
}

// SetVerbose cambia en caliente entre el nivel normal y el detallado.
func SetVerbose(verbose bool) {
	if verbose {
		level.Set(slog.LevelDebug)
	} else {
		level.Set(slog.LevelInfo)
	}
}

// Tail devuelve las últimas n líneas del log (si osat.log tiene menos, completa con osat.1.log, etc.).
func Tail(n int) []string {
	mu.Lock()
	path := logPath
	mu.Unlock()
	if path == "" || n <= 0 {
		return nil
	}
	var lines []string
	for i := 0; i <= keepOld && len(lines) < n; i++ {
		p := path
		if i > 0 {
			p = oldName(path, i)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		b = bytes.TrimRight(b, "\n")
		if len(b) == 0 {
			continue
		}
		part := bytes.Split(b, []byte("\n"))
		chunk := make([]string, len(part))
		for j, l := range part {
			chunk[j] = string(l)
		}
		lines = append(chunk, lines...)
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// Recover se usa con defer al inicio de una goroutine propia: si hay un panic lo registra
// con su traza y lo vuelve a lanzar.
func Recover(where string) {
	if r := recover(); r != nil {
		slog.Error("cierre inesperado", "where", where, "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
		panic(r)
	}
}

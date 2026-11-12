package main

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"osat/internal/applog"
)

// diagnosticLines es cuántas líneas del final del log lleva la información de diagnóstico.
const diagnosticLines = 200

// LogDir devuelve la carpeta de los logs (para abrirla desde "Acerca de").
func (a *App) LogDir() string { return filepath.Join(a.store.Dir(), "logs") }

// Diagnostics arma el texto que se copia al pedir ayuda: versión, sistema, carpeta de datos y el final del log.
func (a *App) Diagnostics() string {
	return diagnosticsText(a.store.Dir(), applog.Tail(diagnosticLines))
}

func diagnosticsText(dataDir string, logLines []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "osat %s\n", appVersion())
	fmt.Fprintf(&b, "Sistema: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&b, "Carpeta de datos: %s\n\n", dataDir)
	fmt.Fprintf(&b, "Últimas %d líneas del log:\n", diagnosticLines)
	if len(logLines) == 0 {
		b.WriteString("(el log está vacío)\n")
	}
	for _, l := range logLines {
		b.WriteString(l + "\n")
	}
	return b.String()
}

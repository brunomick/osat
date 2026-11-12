package main

import (
	"runtime"
	"strings"
	"testing"
)

func TestDiagnosticsText(t *testing.T) {
	out := diagnosticsText("/datos/osat", []string{"linea uno", "linea dos"})
	for _, want := range []string{
		"osat " + appVersion() + "\n",
		"Sistema: " + runtime.GOOS + "/" + runtime.GOARCH + "\n",
		"Carpeta de datos: /datos/osat\n",
		"Últimas 200 líneas del log:\nlinea uno\nlinea dos\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("falta %q en:\n%s", want, out)
		}
	}
}

func TestDiagnosticsTextEmptyLog(t *testing.T) {
	if out := diagnosticsText("/datos/osat", nil); !strings.Contains(out, "(el log está vacío)") {
		t.Errorf("falta el aviso de log vacío:\n%s", out)
	}
}

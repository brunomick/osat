package main

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// capture manda el log a un buffer mientras dura el test.
func capture(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// Pasados los 50 errores por minuto el resto se cuenta, y se informa al cambiar de minuto.
func TestFrontendLogLimit(t *testing.T) {
	buf := capture(t)
	now := time.Date(2026, 1, 1, 10, 0, 0, 0, time.Local)
	f := &frontendLog{now: func() time.Time { return now }}

	for i := 0; i < 60; i++ {
		f.record(fmt.Sprintf("falla %d", i), "at x")
	}
	if n := strings.Count(buf.String(), "error del frontend"); n != 50 {
		t.Fatalf("se registraron %d errores, esperaba 50", n)
	}
	if strings.Contains(buf.String(), "omitidos") {
		t.Fatal("el resumen de omitidos no debería aparecer hasta que cambie el minuto")
	}

	now = now.Add(61 * time.Second)
	f.record("falla nueva", "")
	out := buf.String()
	if !strings.Contains(out, "10 errores del frontend más omitidos") {
		t.Fatalf("falta el resumen de omitidos:\n%s", out)
	}
	if !strings.Contains(out, "falla nueva") {
		t.Fatal("el minuto nuevo debería registrar de nuevo")
	}
}

func TestFrontendLogFlush(t *testing.T) {
	buf := capture(t)
	f := &frontendLog{}
	for i := 0; i < frontendMaxPerMinute+3; i++ {
		f.record("falla", "")
	}
	f.flush()
	if !strings.Contains(buf.String(), "3 errores del frontend más omitidos") {
		t.Fatalf("falta el resumen:\n%s", buf.String())
	}
	f.flush() // ya informado: no se repite
	if strings.Count(buf.String(), "omitidos") != 1 {
		t.Fatalf("el resumen se repitió:\n%s", buf.String())
	}
}

func TestFrontendLogClipsLongText(t *testing.T) {
	buf := capture(t)
	(&frontendLog{}).record(strings.Repeat("é", 5000), strings.Repeat("s", 20000))
	if buf.Len() > frontendMessageMax+frontendStackMax+500 {
		t.Fatalf("el evento no se recortó: %d bytes", buf.Len())
	}
}

func TestAppVersion(t *testing.T) {
	v := appVersion()
	if v == "" || v == "desconocida" || !strings.Contains(v, ".") {
		t.Fatalf("appVersion() = %q", v)
	}
}

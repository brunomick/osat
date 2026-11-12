package remote

import (
	"strings"
	"testing"
	"time"
)

func newBridgeManager(t *testing.T) *Manager {
	t.Helper()
	return NewManager(nil, nil, func(string, ...interface{}) {})
}

func openCmd(t *testing.T, m *Manager) string {
	t.Helper()
	have := false
	for _, s := range m.LocalShells() {
		have = have || s.ID == "cmd"
	}
	if !have {
		t.Skip("no hay cmd en este equipo")
	}
	info, err := m.OpenLocalTerminal("cmd", "", 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close(info.ID) })
	if err := m.Attach(info.ID); err != nil {
		t.Fatal(err)
	}
	return info.ID
}

func TestBridgeOnlySeesSharedTerminals(t *testing.T) {
	m := newBridgeManager(t)
	id := openCmd(t, m)

	if got := m.SharedTerminals(); len(got) != 0 {
		t.Fatalf("sin compartir no debería verse nada: %+v", got)
	}
	if _, err := m.TerminalText(id, 10); err == nil {
		t.Fatal("leer una terminal no compartida debería fallar")
	}
	if err := m.TerminalSend(id, "echo hola\r"); err == nil {
		t.Fatal("escribir en una terminal no compartida debería fallar")
	}
	if _, _, err := m.TerminalRun(id, "echo hola", time.Second); err == nil {
		t.Fatal("ejecutar en una terminal no compartida debería fallar")
	}

	if err := m.SetShared(id, true); err != nil {
		t.Fatal(err)
	}
	got := m.SharedTerminals()
	if len(got) != 1 || got[0].ID != id || got[0].Kind != "local" || got[0].Title != "cmd" {
		t.Fatalf("SharedTerminals = %+v", got)
	}

	m.UnshareAll()
	if got := m.SharedTerminals(); len(got) != 0 {
		t.Fatalf("UnshareAll debería dejar de compartir todo: %+v", got)
	}
	if _, err := m.TerminalText(id, 10); err == nil {
		t.Fatal("después de dejar de compartir, leer debería fallar")
	}
	if err := m.SetShared("no-existe", true); err == nil {
		t.Fatal("compartir una sesión inexistente debería fallar")
	}
}

func TestBridgeRunCommandReturnsOutputSinceTheCommand(t *testing.T) {
	m := newBridgeManager(t)
	id := openCmd(t, m)
	if err := m.SetShared(id, true); err != nil {
		t.Fatal(err)
	}
	// Esperamos a que cmd muestre su prompt.
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if text, _ := m.TerminalText(id, 0); strings.Contains(text, ">") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cmd no mostró su prompt")
		}
	}

	start := time.Now()
	// %OS% se expande en la salida pero no en la línea del comando.
	out, timedOut, err := m.TerminalRun(id, "echo osat-%OS%", 20*time.Second)
	if err != nil || timedOut {
		t.Fatalf("TerminalRun: err=%v timedOut=%v out=%q", err, timedOut, out)
	}
	if !strings.Contains(out, "echo osat-%OS%") || !strings.Contains(out, "osat-Windows_NT") {
		t.Fatalf("falta el comando o su salida: %q", out)
	}
	if strings.Contains(out, "Microsoft Windows [") {
		t.Fatalf("no debería incluir el banner anterior al comando: %q", out)
	}
	if !strings.HasSuffix(out, ">") {
		t.Fatalf("debería terminar con el prompt que volvió: %q", out)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("con el prompt de vuelta tendría que volver enseguida, tardó %v", d)
	}

	// Un comando que tarda: espera a que termine y devuelve la salida completa.
	out, timedOut, err = m.TerminalRun(id, "ping -n 3 127.0.0.1 >nul & echo listo-%OS%", 30*time.Second)
	if err != nil || timedOut || !strings.Contains(out, "listo-Windows_NT") {
		t.Fatalf("comando lento: err=%v timedOut=%v out=%q", err, timedOut, out)
	}

	// Y el resto del historial se puede leer.
	if text, _ := m.TerminalText(id, 0); !strings.Contains(text, "osat-Windows_NT") || !strings.Contains(text, "Microsoft Windows [") {
		t.Fatalf("TerminalText debería incluir todo: %q", text)
	}
}

func TestBridgeRunCommandTimesOut(t *testing.T) {
	m := newBridgeManager(t)
	id := openCmd(t, m)
	if err := m.SetShared(id, true); err != nil {
		t.Fatal(err)
	}
	out, timedOut, err := m.TerminalRun(id, "ping -n 20 127.0.0.1", 2*time.Second)
	if err != nil || !timedOut {
		t.Fatalf("debería agotarse el tiempo: err=%v timedOut=%v out=%q", err, timedOut, out)
	}
	if !strings.Contains(out, "ping -n 20") {
		t.Fatalf("igual devuelve lo impreso hasta ahora: %q", out)
	}
	// Ctrl+C lo corta y el prompt vuelve.
	if err := m.TerminalSend(id, "\x03"); err != nil {
		t.Fatal(err)
	}
}

// TerminalReady: solo con la terminal esperando un comando en un prompt.
func TestBridgeTerminalReadyOnlyAtAShellPrompt(t *testing.T) {
	m := newBridgeManager(t)
	id := openCmd(t, m)
	if err := m.SetShared(id, true); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if m.TerminalReady(id) == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cmd en su prompt debería estar listo: %v", m.TerminalReady(id))
		}
	}

	// Con un comando corriendo (el cursor queda en una línea vacía) no está listo.
	if err := m.TerminalSend(id, "ping -n 30 127.0.0.1\r"); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); m.TerminalReady(id) == nil; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("con un comando corriendo no debería estar listo para recibir otro")
		}
	}
	if err := m.TerminalSend(id, "\x03"); err != nil { // Ctrl+C: vuelve el prompt
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); m.TerminalReady(id) != nil; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("después de Ctrl+C debería volver a estar listo")
		}
	}

	m.UnshareAll()
	if err := m.TerminalReady(id); err == nil {
		t.Fatal("una terminal no compartida no está disponible")
	}
}

package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"osat/internal/remote"
)

func newBridgeApp(t *testing.T, dir string) *App {
	t.Helper()
	a := &App{mgr: remote.NewManager(nil, nil, func(string, ...interface{}) {})}
	a.initBridge(dir)
	t.Cleanup(a.bridge.srv.Stop)
	return a
}

func TestBridgeIsOnTheFirstTimeAndKeepsItsTokenAndChoice(t *testing.T) {
	dir := t.TempDir()

	// Primera vez (no hay bridge.json): encendido de entrada.
	first := newBridgeApp(t, dir)
	st := first.BridgeStatus()
	if !st.Enabled || st.Token == "" || st.Port == 0 {
		t.Fatalf("la primera vez debería arrancar encendido: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(dir, "bridge.json")); err != nil {
		t.Fatalf("el token tiene que quedar guardado desde el primer arranque: %v", err)
	}
	first.bridge.srv.Stop()

	// Al reiniciar: sigue encendido y con el mismo token (si no, habría que volver a agregarlo a Claude Code).
	second := newBridgeApp(t, dir)
	if st2 := second.BridgeStatus(); !st2.Enabled || st2.Token != st.Token {
		t.Fatalf("debería conservar el token: antes %q, ahora %+v", st.Token, st2)
	}

	// Si el usuario lo apaga, queda apagado en los siguientes arranques.
	if _, err := second.BridgeSetEnabled(false); err != nil {
		t.Fatal(err)
	}
	third := newBridgeApp(t, dir)
	if st3 := third.BridgeStatus(); st3.Enabled {
		t.Fatalf("apagado por el usuario: no debería volver a encenderse solo: %+v", st3)
	}
	// Y se puede volver a encender con el mismo token.
	if st4, err := third.BridgeSetEnabled(true); err != nil || !st4.Enabled || st4.Token != st.Token {
		t.Fatalf("encender de nuevo: %+v, %v", st4, err)
	}
}

// Si el puerto de siempre está ocupado al arrancar, el puente usa otro solo por
// esa vez: el guardado no cambia, así el comando ya agregado a Claude Code
// vuelve a funcionar en el próximo arranque.
func TestBridgeKeepsItsPreferredPortWhenBusy(t *testing.T) {
	old := bridgeStartWait
	bridgeStartWait = 50 * time.Millisecond
	t.Cleanup(func() { bridgeStartWait = old })

	dir := t.TempDir()
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := busy.Addr().(*net.TCPAddr).Port
	saved, _ := json.Marshal(bridgeSettings{Enabled: true, Port: port, Token: "tok"})
	if err := os.WriteFile(filepath.Join(dir, "bridge.json"), saved, 0o600); err != nil {
		t.Fatal(err)
	}

	a := newBridgeApp(t, dir)
	st := a.BridgeStatus()
	if !st.Enabled || st.Port == port || st.PreferredPort != port {
		t.Fatalf("debería usar otro puerto sin olvidar el de siempre (%d): %+v", port, st)
	}
	var file bridgeSettings
	data, _ := os.ReadFile(filepath.Join(dir, "bridge.json"))
	if err := json.Unmarshal(data, &file); err != nil || file.Port != port {
		t.Fatalf("el puerto guardado debe seguir siendo %d: %+v (%v)", port, file, err)
	}

	// Cuando se libera, el próximo arranque vuelve al de siempre.
	a.bridge.srv.Stop()
	busy.Close()
	b := newBridgeApp(t, dir)
	if st := b.BridgeStatus(); st.Port != port || st.PreferredPort != port {
		t.Fatalf("debería volver al puerto de siempre: %+v", st)
	}
}

// Si el puerto se libera enseguida (la instancia anterior terminando de
// cerrarse), se lo espera en vez de pasarse a otro.
func TestBridgeWaitsForThePreferredPortToFree(t *testing.T) {
	dir := t.TempDir()
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := busy.Addr().(*net.TCPAddr).Port
	saved, _ := json.Marshal(bridgeSettings{Enabled: true, Port: port, Token: "tok"})
	os.WriteFile(filepath.Join(dir, "bridge.json"), saved, 0o600)
	time.AfterFunc(400*time.Millisecond, func() { busy.Close() })

	a := newBridgeApp(t, dir)
	if st := a.BridgeStatus(); st.Port != port {
		t.Fatalf("debería esperar y usar %d: %+v", port, st)
	}
}

// El modo se guarda, cambia las reglas de permisos y arranca en "ask".
func TestBridgeModeIsSavedAndChangesTheRules(t *testing.T) {
	dir := t.TempDir()
	a := newBridgeApp(t, dir)
	st := a.BridgeStatus()
	if st.Mode != "ask" || !strings.Contains(st.Permissions, `"ask"`) {
		t.Fatalf("debería arrancar con permisos: %+v", st)
	}
	st, err := a.BridgeSetMode("auto")
	if err != nil || st.Mode != "auto" {
		t.Fatalf("auto: %+v, %v", st, err)
	}
	if strings.Contains(st.Permissions, `"ask"`) || !strings.Contains(st.Permissions, "mcp__osat__run_command") ||
		!strings.Contains(st.Permissions, "mcp__osat__send_input") || !strings.Contains(st.Permissions, "mcp__osat__interrupt") {
		t.Fatalf("en modo auto las reglas tienen que permitir todo: %s", st.Permissions)
	}
	if _, err := a.BridgeSetMode("cualquiera"); err == nil {
		t.Fatal("un modo desconocido tiene que dar error")
	}
	a.bridge.srv.Stop()

	// Se conserva al reiniciar.
	b := newBridgeApp(t, dir)
	if st := b.BridgeStatus(); st.Mode != "auto" {
		t.Fatalf("debería seguir en auto: %+v", st)
	}
	if st, err := b.BridgeSetMode("ask"); err != nil || st.Mode != "ask" || !strings.Contains(st.Permissions, `"ask"`) {
		t.Fatalf("volver a ask: %+v, %v", st, err)
	}
}

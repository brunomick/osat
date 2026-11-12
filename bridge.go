package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"osat/internal/bridge"
)

// Puente con Claude Code: un servidor MCP local (ver internal/bridge) que se
// enciende desde la app y solo ve las terminales que el usuario comparte.

const (
	defaultBridgePort = 47800
	bridgePortSpan    = 100 // si el puerto está ocupado, se prueban los siguientes
)

// bridgeSettings se guarda en bridge.json (con permisos solo del usuario: el
// token da acceso a las terminales compartidas).
type bridgeSettings struct {
	Enabled bool   `json:"enabled"`
	Port    int    `json:"port"`
	Token   string `json:"token"`
	// Mode: "ask" (lo que solo lee corre sin preguntar y lo demás pide permiso) o
	// "auto" (sin permisos: Claude corre lo que quiera). Vacío = "ask".
	Mode string `json:"mode,omitempty"`
}

const (
	bridgeModeAsk  = "ask"
	bridgeModeAuto = "auto"
)

func (s bridgeSettings) auto() bool { return s.Mode == bridgeModeAuto }

// BridgeStatus es lo que ve el frontend.
type BridgeStatus struct {
	Enabled bool `json:"enabled"`
	Port    int  `json:"port"`
	// PreferredPort es el puerto de siempre; si Port es otro, estaba ocupado.
	PreferredPort int    `json:"preferredPort"`
	Token         string `json:"token"`
	// Mode es "ask" o "auto" (ver bridgeSettings); Permissions sigue al modo.
	Mode string `json:"mode"`
	URL  string `json:"url"`
	// Command agrega este puente a Claude Code.
	Command string `json:"command"`
	// Permissions son las reglas de permisos para Claude Code: lo que solo lee se
	// permite de una vez y lo que cambia cosas queda en "ask" (con su explicación).
	Permissions string `json:"permissions"`
}

// mcpName es el nombre con el que se agrega el puente a Claude Code; las reglas
// de permisos lo usan (mcp__osat__<herramienta>).
const mcpName = "osat"

// permissionRules son las reglas para settings.json de Claude Code: es Claude
// Code (no osat) quien pide permiso, así que el modo se aplica pegando estas.
// En modo "ask" solo lo que lee corre de una vez; en "auto" corre todo.
func permissionRules(auto bool) string {
	names := func(tools []string) []string {
		out := make([]string, len(tools))
		for i, t := range tools {
			out[i] = "mcp__" + mcpName + "__" + t
		}
		return out
	}
	perms := map[string]any{"allow": names(bridge.ReadOnlyTools), "ask": names(bridge.ChangeTools)}
	if auto {
		perms = map[string]any{"allow": names(append(append([]string{}, bridge.ReadOnlyTools...), bridge.ChangeTools...))}
	}
	b, _ := json.MarshalIndent(map[string]any{"permissions": perms}, "", "  ")
	return string(b)
}

type bridgeState struct {
	mu   sync.Mutex
	path string
	set  bridgeSettings
	srv  *bridge.Server
}

func (a *App) initBridge(dir string) {
	b := &bridgeState{path: filepath.Join(dir, "bridge.json"), srv: bridge.New(a.mgr)}
	data, err := os.ReadFile(b.path)
	if err == nil {
		if err := json.Unmarshal(data, &b.set); err != nil {
			slog.Warn("puente con Claude: bridge.json no se pudo interpretar", "err", err)
		}
	} else if errors.Is(err, os.ErrNotExist) {
		// Primera vez: encendido de entrada. No expone ninguna terminal hasta que
		// el usuario comparta una, y desde el panel se puede apagar para siempre.
		b.set.Enabled = true
	} else {
		slog.Warn("puente con Claude: no se pudo leer bridge.json", "err", err)
	}
	a.bridge = b
	b.srv.SetAuto(b.set.auto())
	if b.set.Enabled {
		if err := b.start(bridgeStartWait); err != nil {
			slog.Warn("puente con Claude: no se pudo encender", "err", err)
			b.set.Enabled = false // puerto ocupado o similar: queda apagado hasta que el usuario lo encienda
		} else if err := b.save(); err != nil { // el token y el puerto tienen que ser los mismos la próxima vez
			slog.Warn("puente con Claude: no se pudo guardar bridge.json", "err", err)
		}
	}
}

func (b *bridgeState) save() error {
	data, err := json.MarshalIndent(b.set, "", "  ")
	if err != nil {
		return err
	}
	tmp := b.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, b.path)
}

func newToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// bridgeStartWait es cuánto se espera al arrancar a que se libere el puerto de
// siempre (la instancia anterior de la app puede estar terminando de cerrarse).
var bridgeStartWait = 3 * time.Second

// start enciende el servidor con el token guardado (lo crea si falta). Usa el
// puerto de siempre (b.set.Port); solo si sigue ocupado después de esperar usa
// el siguiente libre, y esta vez nada más: el puerto guardado no cambia, así el
// comando que ya se agregó a Claude Code vuelve a servir apenas se libere.
func (b *bridgeState) start(wait time.Duration) error {
	if b.set.Token == "" {
		t, err := newToken()
		if err != nil {
			return err
		}
		b.set.Token = t
	}
	if b.set.Port == 0 {
		b.set.Port = defaultBridgePort
	}
	preferred := b.set.Port
	var err error
	for deadline := time.Now().Add(wait); ; time.Sleep(200 * time.Millisecond) {
		if err = b.srv.Start(preferred, b.set.Token); err == nil || !time.Now().Before(deadline) {
			break
		}
	}
	for i := 1; err != nil && i < bridgePortSpan; i++ {
		err = b.srv.Start(preferred+i, b.set.Token)
	}
	if err != nil {
		return fmt.Errorf("no se pudo abrir un puerto local para el puente: %w", err)
	}
	_, port := b.srv.Running()
	slog.Info("puente con Claude: encendido", "port", port, "preferred_port", preferred)
	return nil
}

func (b *bridgeState) status() BridgeStatus {
	mode := bridgeModeAsk
	if b.set.auto() {
		mode = bridgeModeAuto
	}
	on, port := b.srv.Running()
	if !on {
		return BridgeStatus{Port: b.set.Port, PreferredPort: b.set.Port, Mode: mode}
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
	return BridgeStatus{
		Enabled:       true,
		Port:          port,
		PreferredPort: b.set.Port,
		Token:         b.set.Token,
		Mode:          mode,
		URL:           url,
		Command:       fmt.Sprintf(`claude mcp add --transport http %s %s --header "Authorization: Bearer %s"`, mcpName, url, b.set.Token),
		Permissions:   permissionRules(b.set.auto()),
	}
}

// BridgeStatus dice si el puente con Claude Code está encendido y cómo conectarlo.
func (a *App) BridgeStatus() BridgeStatus {
	a.bridge.mu.Lock()
	defer a.bridge.mu.Unlock()
	return a.bridge.status()
}

// BridgeSetEnabled enciende o apaga el puente. Al apagarlo se deja de compartir
// toda terminal y el frontend se entera por el evento "bridge:unshared".
func (a *App) BridgeSetEnabled(on bool) (BridgeStatus, error) {
	b := a.bridge
	b.mu.Lock()
	defer b.mu.Unlock()
	if on {
		if err := b.start(0); err != nil {
			slog.Warn("puente con Claude: no se pudo encender", "err", err)
			return b.status(), err
		}
	} else {
		b.srv.Stop()
		slog.Info("puente con Claude: apagado")
		a.mgr.UnshareAll()
		if a.ctx != nil {
			nativeEventsEmit(a.ctx, "bridge:unshared")
		}
	}
	b.set.Enabled = on
	return b.status(), b.save()
}

// BridgeSetMode elige el modo: "ask" (lo que solo lee corre sin preguntar, lo
// demás pide permiso) o "auto" (sin permisos: Claude corre lo que quiera).
// Cambia las reglas de permisos que muestra el panel; hay que pegarlas en
// settings.json de Claude Code, que es quien pregunta.
func (a *App) BridgeSetMode(mode string) (BridgeStatus, error) {
	if mode != bridgeModeAsk && mode != bridgeModeAuto {
		return a.BridgeStatus(), fmt.Errorf("modo desconocido: %q", mode)
	}
	b := a.bridge
	b.mu.Lock()
	defer b.mu.Unlock()
	b.set.Mode = mode
	b.srv.SetAuto(b.set.auto())
	return b.status(), b.save()
}

// BridgeNewToken cambia el token: el comando anterior deja de funcionar.
func (a *App) BridgeNewToken() (BridgeStatus, error) {
	b := a.bridge
	b.mu.Lock()
	defer b.mu.Unlock()
	t, err := newToken()
	if err != nil {
		return b.status(), err
	}
	b.set.Token = t
	b.srv.SetToken(t)
	return b.status(), b.save()
}

// TermShare comparte una terminal con Claude Code, o deja de compartirla.
func (a *App) TermShare(id string, on bool) error {
	if on {
		if enabled, _ := a.bridge.srv.Running(); !enabled {
			return errors.New("encendé primero el puente con Claude Code")
		}
	}
	return a.mgr.SetShared(id, on)
}

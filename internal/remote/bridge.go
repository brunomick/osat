package remote

import (
	"errors"
	"io"
	"sort"
	"strings"
	"time"

	"osat/internal/bridge"
)

// Puente con Claude Code (ver el paquete bridge): el Manager le deja ver y
// manejar solo las terminales que el usuario compartió, una por una.

var errNotShared = errors.New("esa terminal no existe o no está compartida con Claude Code")

// SetShared comparte (o deja de compartir) una terminal con Claude Code.
func (m *Manager) SetShared(id string, on bool) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	if s.screen == nil {
		return errors.New("solo se pueden compartir terminales")
	}
	s.sharedWith.Store(on)
	return nil
}

// UnshareAll deja de compartir todas las terminales (al apagar el puente).
func (m *Manager) UnshareAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		s.sharedWith.Store(false)
	}
}

func (m *Manager) sharedSession(id string) (*Session, error) {
	s, err := m.get(id)
	if err != nil || s.screen == nil || !s.sharedWith.Load() {
		return nil, errNotShared
	}
	return s, nil
}

func (m *Manager) SharedTerminals() []bridge.Terminal {
	m.mu.Lock()
	var out []bridge.Terminal
	for _, s := range m.sessions {
		if s.screen == nil || !s.sharedWith.Load() {
			continue
		}
		t := bridge.Terminal{ID: s.ID, Title: s.title, Kind: "ssh", Host: s.host}
		if s.local != nil {
			t.Kind = "local"
		}
		out = append(out, t)
	}
	m.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Title+out[i].ID < out[j].Title+out[j].ID })
	return out
}

// TerminalReady dice si la terminal parece estar esperando un comando en un
// prompt de shell. Si hay un programa de pantalla completa (vim, less...) o el
// cursor está al principio de una línea vacía (algo que lee entrada sin prompt),
// lo que se tipee iría a parar ahí y no se ejecutaría como comando.
func (m *Manager) TerminalReady(id string) error {
	s, err := m.sharedSession(id)
	if err != nil {
		return err
	}
	if s.screen.InAltScreen() {
		return errors.New("the terminal is running a full-screen program (vim, less, top...): ask the user to leave it first")
	}
	if _, before := s.screen.CursorLine(); strings.TrimSpace(before) == "" {
		return errors.New("the terminal does not look like it is waiting at a shell prompt (the cursor is on an empty line): read_terminal to see what is on screen")
	}
	return nil
}

func (m *Manager) TerminalText(id string, lines int) (string, error) {
	s, err := m.sharedSession(id)
	if err != nil {
		return "", err
	}
	return s.screen.Text(lines), nil
}

func (m *Manager) TerminalSend(id, data string) error {
	s, err := m.sharedSession(id)
	if err != nil {
		return err
	}
	_, err = io.WriteString(s.stdin, data)
	return err
}

// Tiempos de TerminalRun: con el prompt de vuelta alcanza una pausa corta; si
// no se lo reconoce (prompt que cambia, programa interactivo) se espera más.
const (
	runQuiet     = 700 * time.Millisecond
	runSlowQuiet = 3 * time.Second
	runPoll      = 50 * time.Millisecond
)

// TerminalRun tipea el comando y devuelve lo que se imprimió desde entonces,
// cuando la salida se asienta o se agota el tiempo.
func (m *Manager) TerminalRun(id, command string, timeout time.Duration) (string, bool, error) {
	s, err := m.sharedSession(id)
	if err != nil {
		return "", false, err
	}
	_, prompt := s.screen.CursorLine() // lo que hay antes del cursor: el prompt
	mark := s.screen.Mark()
	w0, _ := s.screen.Writes()
	if _, err := io.WriteString(s.stdin, command+"\r"); err != nil {
		return "", false, err
	}

	deadline := time.Now().Add(timeout)
	timedOut := false
	for {
		time.Sleep(runPoll)
		if w, last := s.screen.Writes(); w > w0 {
			idle := time.Since(last)
			line, _ := s.screen.CursorLine()
			backAtPrompt := prompt != "" && s.screen.Mark() > mark && strings.HasPrefix(line, prompt)
			if (backAtPrompt && idle >= runQuiet) || idle >= runSlowQuiet {
				break
			}
		}
		if time.Now().After(deadline) {
			timedOut = true
			break
		}
	}
	return s.screen.Since(mark), timedOut, nil
}

package remote

import (
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"

	"osat/internal/i18n"
)

// Preguntas keyboard-interactive (OTP, 2FA, "Verification code:", etc.):
// se le muestran al usuario en vez de contestarlas con la contraseña.

type PromptQuestion struct {
	Text string `json:"text"`
	Echo bool   `json:"echo"`
}

// PromptRequest viaja al frontend en el evento "auth:prompt".
type PromptRequest struct {
	ID          string           `json:"id"`
	Host        string           `json:"host"`
	Name        string           `json:"name"`
	Instruction string           `json:"instruction"`
	Questions   []PromptQuestion `json:"questions"`
}

// Prompter pregunta al usuario y devuelve una respuesta por pregunta.
type Prompter func(PromptRequest) ([]string, error)

const promptTimeout = 5 * time.Minute

var errPromptCancelled = newErr(CodeCancelled, "Autenticación cancelada.", nil)

func looksLikePassword(q string) bool {
	q = strings.ToLower(q)
	for _, w := range []string{"password", "contraseña", "passwort", "senha", "mot de passe"} {
		if strings.Contains(q, w) {
			return true
		}
	}
	return false
}

// keyboardInteractive responde con secret solo la típica pregunta oculta de
// contraseña (una vez); todo lo demás se lo pregunta al usuario. pause detiene
// el deadline del handshake mientras el usuario escribe y devuelve cómo
// reanudarlo.
func keyboardInteractive(host, secret string, prompt Prompter, pause func() func()) ssh.AuthMethod {
	secretUsed := false
	return ssh.KeyboardInteractive(func(name, instruction string, qs []string, echos []bool) ([]string, error) {
		if len(qs) == 0 { // ronda solo informativa
			return []string{}, nil
		}
		if secret != "" && !secretUsed && len(qs) == 1 && !echos[0] && looksLikePassword(qs[0]) {
			secretUsed = true
			return []string{secret}, nil
		}
		if prompt == nil {
			return nil, i18n.Err("remote.theServerAskedForAdditional")
		}
		req := PromptRequest{ID: uuid.NewString(), Host: host, Name: name, Instruction: instruction}
		for i, q := range qs {
			req.Questions = append(req.Questions, PromptQuestion{Text: q, Echo: i < len(echos) && echos[i]})
		}
		resume := pause()
		defer resume()
		ans, err := prompt(req)
		if err != nil {
			return nil, err
		}
		if len(ans) != len(qs) {
			return nil, errPromptCancelled
		}
		return ans, nil
	})
}

// noPrompt es el Prompter de la reconexión automática (ConnectRequest.NoPrompt):
// no pregunta y corta con NEED_SECRET, para que el usuario reconecte a mano.
func noPrompt(PromptRequest) ([]string, error) {
	return nil, newErr(CodeNeedSecret, "El servidor pide datos adicionales para entrar.", map[string]string{"kind": "prompt"})
}

// askUser implementa Prompter emitiendo "auth:prompt" y esperando a que el
// frontend llame a AnswerPrompt.
func (m *Manager) askUser(req PromptRequest) ([]string, error) {
	ch := make(chan []string, 1)
	m.mu.Lock()
	m.prompts[req.ID] = ch
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.prompts, req.ID)
		m.mu.Unlock()
	}()

	m.emit("auth:prompt", req)
	select {
	case ans := <-ch:
		if ans == nil {
			return nil, errPromptCancelled
		}
		return ans, nil
	case <-time.After(promptTimeout):
		return nil, errPromptCancelled
	}
}

// AnswerPrompt entrega las respuestas del usuario (nil = canceló).
func (m *Manager) AnswerPrompt(id string, answers []string) {
	m.mu.Lock()
	ch, ok := m.prompts[id]
	m.mu.Unlock()
	if ok {
		select {
		case ch <- answers:
		default:
		}
	}
}

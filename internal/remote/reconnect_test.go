package remote

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"osat/internal/store"
)

// events junta los eventos que emite el Manager.
type events struct {
	mu   sync.Mutex
	list []string
}

func (e *events) emit(event string, _ ...interface{}) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.list = append(e.list, event)
}

func (e *events) get() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.list...)
}

// waitFor espera hasta 2 s a que llegue el evento.
func (e *events) waitFor(event string) bool {
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		for _, ev := range e.get() {
			if ev == event {
				return true
			}
		}
	}
	return false
}

// Si se corta la conexión, cada sesión que seguía abierta sobre ella recibe
// session:lost; la que el usuario ya había cerrado, no.
func TestSessionLostIsEmittedForOpenSessions(t *testing.T) {
	var ev events
	m := NewManager(nil, nil, ev.emit)
	client := fakeShellClient(t, "SSH-2.0-OpenSSH_9.6p1")
	sh := newSharedClient(client)
	conn := store.Connection{ID: "c"}
	a := m.startTunnel(sh, conn)
	if err := sh.acquire(); err != nil {
		t.Fatal(err)
	}
	b := m.startTunnel(sh, conn)
	if err := sh.acquire(); err != nil {
		t.Fatal(err)
	}
	closed := m.startTunnel(sh, conn)
	m.Close(closed.ID)

	client.Close() // la conexión se cae
	if !ev.waitFor("session:lost:"+a.ID) || !ev.waitFor("session:lost:"+b.ID) {
		t.Fatalf("faltan avisos: %q", ev.get())
	}
	time.Sleep(50 * time.Millisecond)
	for _, e := range ev.get() {
		if e == "session:lost:"+closed.ID {
			t.Fatal("se avisó a una sesión que ya estaba cerrada")
		}
	}
}

// Cerrar todas las sesiones cierra la conexión, pero no es una caída: no se avisa nada.
func TestSessionLostIsNotEmittedWhenTheUserCloses(t *testing.T) {
	var ev events
	m := NewManager(nil, nil, ev.emit)
	client := fakeShellClient(t, "SSH-2.0-OpenSSH_9.6p1")
	sh := newSharedClient(client)
	s := m.startTunnel(sh, store.Connection{ID: "c"})
	m.Close(s.ID)
	select {
	case <-sh.lost():
	case <-time.After(2 * time.Second):
		t.Fatal("la conexión debería haberse cerrado")
	}
	time.Sleep(50 * time.Millisecond)
	if got := ev.get(); len(got) != 0 {
		t.Fatalf("no debería haber avisos: %q", got)
	}
}

// otpServer es un servidor SSH que solo acepta keyboard-interactive con un
// código de verificación (como un segundo factor).
func otpServer(t *testing.T, code string) (string, int) {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{KeyboardInteractiveCallback: func(_ ssh.ConnMetadata, ask ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
		ans, err := ask("", "", []string{"Verification code: "}, []bool{true})
		if err != nil {
			return nil, err
		}
		if len(ans) == 1 && ans[0] == code {
			return nil, nil
		}
		return nil, errors.New("código incorrecto")
	}}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, chans, reqs, err := ssh.NewServerConn(nc, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for nch := range chans {
					nch.Reject(ssh.Prohibited, "nada")
				}
			}()
		}
	}()
	a := ln.Addr().(*net.TCPAddr)
	return a.IP.String(), a.Port
}

// Con NoPrompt, un servidor que pide un código no dispara auth:prompt: corta
// con NEED_SECRET. Sin NoPrompt se pregunta como siempre.
func TestNoPromptDoesNotAskTheUser(t *testing.T) {
	m, st := jumpRig(t)
	var ev events
	m.emit = func(event string, data ...interface{}) {
		ev.emit(event)
		if event == "auth:prompt" {
			go m.AnswerPrompt(data[0].(PromptRequest).ID, []string{"123456"})
		}
	}
	h, p := otpServer(t, "123456")
	c, err := st.Save(store.ConnectionInput{Connection: store.Connection{Name: "otp", Host: h, Port: p, User: "u", AuthType: store.AuthPassword}, Secret: "pw"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = dialTrusting(m, ConnectRequest{ConnectionID: c.ID, NoPrompt: true})
	var ke *Error
	if !errors.As(err, &ke) || ke.Code != CodeNeedSecret {
		t.Fatalf("esperaba NEED_SECRET, obtuve %v", err)
	}
	for _, e := range ev.get() {
		if e == "auth:prompt" {
			t.Fatal("con NoPrompt no se pregunta nada")
		}
	}

	client, err := dialTrusting(m, ConnectRequest{ConnectionID: c.ID})
	if err != nil {
		t.Fatalf("sin NoPrompt debería preguntar y entrar: %v", err)
	}
	client.Close()
	if !ev.waitFor("auth:prompt") {
		t.Fatal("sin NoPrompt se tendría que haber preguntado el código")
	}
}

// Una conexión borrada (o la de su salto) es CONN_GONE, con el mismo mensaje
// de siempre: la reconexión automática lo reconoce por el código y se detiene.
func TestConnGoneHasItsCode(t *testing.T) {
	m, _ := jumpRig(t)

	_, _, err := m.dial(ConnectRequest{ConnectionID: "borrada"})
	var ke *Error
	if !errors.As(err, &ke) || ke.Code != CodeConnGone || ke.Message != "conexión inexistente" || ke.Data["connId"] != "borrada" {
		t.Fatalf("conexión borrada: err = %v", err)
	}

	// El store no deja guardar ni dejar huérfano un salto, así que se arma a mano.
	c := store.Connection{ID: "db", Name: "db", JumpID: "bastion-borrado"}
	_, err = m.dialJump(c, nil, noPrompt, 0)
	if !errors.As(err, &ke) || ke.Code != CodeConnGone || ke.Message != "la conexión de salto de «db» ya no existe" ||
		ke.Data["name"] != "db" || ke.Data["jumpId"] != "bastion-borrado" {
		t.Fatalf("salto borrado: err = %v", err)
	}
}

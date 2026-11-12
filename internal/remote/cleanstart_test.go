package remote

import (
	"bytes"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// capture junta lo que emite un cleanStart.
type capture struct {
	mu  sync.Mutex
	out []byte
}

func (c *capture) emit(b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.out = append(c.out, b...)
}

func (c *capture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.out)
}

func TestCleanStartDropsEverythingBeforeTheMarker(t *testing.T) {
	var got capture
	cs := newCleanStart(got.emit)
	cs.start()
	cs.write([]byte("Welcome to Ubuntu\r\n * Documentation: https://docs.ubuntu.com\r\nLast login: Tue\r\n"))
	cs.write([]byte("ubuntu@ip:~$ PS1=... printf\r\n\x1b]777;osat-")) // la marca llega partida
	if got.String() != "" {
		t.Fatalf("no debería salir nada antes de la marca, salió %q", got.String())
	}
	cs.write([]byte("ready\x07new-prompt$ "))
	if want := clearScreen + "new-prompt$ "; got.String() != want {
		t.Fatalf("salió %q, quería %q", got.String(), want)
	}
	cs.write([]byte("ls\r\n"))
	if want := clearScreen + "new-prompt$ ls\r\n"; got.String() != want {
		t.Fatalf("después de la marca todo pasa tal cual: salió %q, quería %q", got.String(), want)
	}
}

func TestCleanStartGivesUpAndShowsTheOutput(t *testing.T) {
	var got capture
	cs := newCleanStart(got.emit)
	cs.silence = 30 * time.Millisecond
	cs.start()
	cs.write([]byte("C:\\Users\\admin>"))
	time.Sleep(150 * time.Millisecond)
	if got.String() != "C:\\Users\\admin>" {
		t.Fatalf("sin marca debería mostrarse la salida tal cual, salió %q", got.String())
	}
	// Si la marca llega tarde no se dibuja ni borra nada.
	cs.write(append([]byte("echo"), cleanStartMarker...))
	if want := "C:\\Users\\admin>echo"; got.String() != want {
		t.Fatalf("salió %q, quería %q", got.String(), want)
	}
}

func TestCleanStartFlushesWhenTheSessionEnds(t *testing.T) {
	var got capture
	cs := newCleanStart(got.emit)
	cs.start()
	cs.write([]byte("This account is currently not available.\r\n"))
	cs.giveUp() // lo que hace el lector al terminar la sesión
	if got.String() != "This account is currently not available.\r\n" {
		t.Fatalf("salió %q", got.String())
	}
}

func TestCleanStartEnabled(t *testing.T) {
	for v, want := range map[string]bool{
		"SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13": true,
		"SSH-2.0-dropbear_2022.83":               true,
		"SSH-2.0-OpenSSH_for_Windows_9.5":        false,
		"SSH-2.0-OpenSSH_for_windows_8.1":        false,
		"SSH-2.0-Cisco-1.25":                     true, // sin marca se ve normal
	} {
		if got := cleanStartEnabled(v); got != want {
			t.Errorf("cleanStartEnabled(%q) = %v, quería %v", v, got, want)
		}
	}
}

// La línea que se tipea tiene que ser válida para una shell de verdad: imprime
// la marca y deja el prompt común.
func TestCleanStartInputRunsInBash(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no hay bash en este equipo")
	}
	cmd := exec.Command(bash, "--norc", "--noprofile", "-i")
	cmd.Stdin = strings.NewReader(cleanStartInput + "printf '<%s>' \"$PS1\"\nexit\n")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("bash: %v\n%s", err, out.String())
	}
	s := out.String()
	if !strings.Contains(s, string(cleanStartMarker)) {
		t.Fatalf("falta la marca en la salida de bash: %q", s)
	}
	if !strings.Contains(s, `<\[\e[90m\]\u@\h\[\e[0m\]:\[\e[1m\]\w\[\e[0m\]\$ >`) {
		t.Fatalf("PS1 no quedó como se esperaba: %q", s)
	}
}

// Con keepScreen (reconexión) se descarta lo de antes de la marca, pero sin
// borrar la pantalla; los avisos siguen saliendo antes del prompt.
func TestCleanStartKeepScreen(t *testing.T) {
	var got capture
	cs := newCleanStart(got.emit)
	cs.keepScreen = true
	cs.onDone = func() []byte { return []byte("<aviso>") }
	cs.start()
	cs.write([]byte("Welcome to Ubuntu\r\neco"))
	cs.write(append(append([]byte{}, cleanStartMarker...), "new$ "...))
	if want := "<aviso>new$ "; got.String() != want {
		t.Fatalf("salió %q, quería %q", got.String(), want)
	}
}

// onDone se llama una vez y lo que devuelve va entre el borrado y el resto.
func TestCleanStartOnDoneWithMarker(t *testing.T) {
	var got capture
	calls := 0
	cs := newCleanStart(got.emit)
	cs.onDone = func() []byte { calls++; return []byte("<aviso>") }
	cs.start()
	cs.write([]byte("banner"))
	if calls != 0 {
		t.Fatal("onDone antes de la marca")
	}
	cs.write(append(append([]byte("eco"), cleanStartMarker...), "prompt$ "...))
	cs.giveUp() // ya resuelto: no vuelve a llamarlo
	if want := clearScreen + "<aviso>prompt$ "; got.String() != want || calls != 1 {
		t.Fatalf("salió %q (onDone %d veces), quería %q", got.String(), calls, want)
	}
}

// Sin marca, los avisos salen antes de lo retenido; aunque no haya nada retenido.
func TestCleanStartOnDoneOnGiveUp(t *testing.T) {
	for _, held := range []string{"C:\\>", ""} {
		var got capture
		calls := 0
		cs := newCleanStart(got.emit)
		cs.silence = 30 * time.Millisecond
		cs.onDone = func() []byte { calls++; return []byte("<aviso>") }
		cs.start()
		if held != "" {
			cs.write([]byte(held))
		}
		time.Sleep(150 * time.Millisecond)
		cs.write(cleanStartMarker) // tarde: no se llama de nuevo
		if want := "<aviso>" + held; got.String() != want || calls != 1 {
			t.Fatalf("salió %q (onDone %d veces), quería %q", got.String(), calls, want)
		}
	}
}

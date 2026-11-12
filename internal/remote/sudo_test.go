package remote

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

const sudoTestFile = "/etc/app.conf"

// fakeSudo simula sudo en el servidor de prueba (execServer) para un solo archivo, con la contraseña "secreta".
type fakeSudo struct {
	mode string // "nopasswd" | "passwd" | "nots" (timestamp_timeout=0) | "requiretty" | "nosudoers" | "nosudo"

	mu        sync.Mutex
	file      []byte
	validated bool // vale la validación (sudo -v) para los comandos siguientes
	attempts  int  // contraseñas incorrectas recibidas
	cmds      []string
	stdins    []string // lo que recibió cada comando por stdin
}

func (f *fakeSudo) run(cmd string, ch ssh.Channel) int {
	in, _ := io.ReadAll(ch)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cmds = append(f.cmds, cmd)
	f.stdins = append(f.stdins, string(in))
	fail := func(msg string, code int) int { io.WriteString(ch.Stderr(), msg); return code }
	badPass := func() int {
		f.attempts++
		return fail("Sorry, try again.\nsudo: no password was provided\nsudo: 1 incorrect password attempt\n", 1)
	}
	switch f.mode {
	case "nosudo":
		return fail("sh: 1: sudo: not found\n", 127)
	case "requiretty":
		return fail("sudo: sorry, you must have a tty to run sudo\n", 1)
	}
	switch {
	case strings.HasPrefix(cmd, sudoN):
		if f.mode != "nopasswd" && !f.validated {
			return fail("sudo: a password is required\n", 1)
		}
		return f.exec(strings.TrimPrefix(cmd, sudoN), in, ch)
	case cmd == sudoValidateCmd:
		if string(in) != "secreta\n" {
			return badPass()
		}
		if f.mode == "nosudoers" {
			return fail("Sorry, user u may not run sudo on srv.\n", 1)
		}
		f.validated = f.mode != "nots"
		return 0
	case strings.HasPrefix(cmd, sudoS):
		pw, rest, _ := bytes.Cut(in, []byte("\n"))
		if string(pw) != "secreta" {
			return badPass()
		}
		return f.exec(strings.TrimPrefix(cmd, sudoS), rest, ch)
	}
	return fail("comando inesperado\n", 2)
}

func (f *fakeSudo) exec(cmd string, stdin []byte, ch ssh.Channel) int {
	switch cmd {
	case "head -c 2097153 -- '" + sudoTestFile + "'":
		b := f.file
		if len(b) > maxEditSize+1 {
			b = b[:maxEditSize+1]
		}
		ch.Write(b)
		return 0
	case "tee -- '" + sudoTestFile + "' > /dev/null":
		f.file = append([]byte(nil), stdin...)
		return 0
	}
	io.WriteString(ch.Stderr(), "comando inesperado\n")
	return 2
}

func (f *fakeSudo) log() ([]string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.cmds...), append([]string(nil), f.stdins...)
}

func (f *fakeSudo) content() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return string(f.file)
}

func sudoRig(t *testing.T, f *fakeSudo) (*Manager, *sharedClient) {
	t.Helper()
	m := newBridgeManager(t)
	t.Cleanup(m.CloseAll)
	sc := newSharedClient(execServer(t, f.run))
	m.register(&Session{ID: "srv", Kind: KindSFTP, ConnID: "c", shared: sc})
	return m, sc
}

func readCmd(prefix string) string  { return prefix + "head -c 2097153 -- '" + sudoTestFile + "'" }
func writeCmd(prefix string) string { return prefix + "tee -- '" + sudoTestFile + "' > /dev/null" }

func sameList(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n---\n") != strings.Join(want, "\n---\n") {
		t.Fatalf("%s:\n%q\nquería\n%q", what, got, want)
	}
}

// Con NOPASSWD no se pide ni se manda contraseña: un solo comando para leer y otro para guardar.
func TestSudoWithoutPassword(t *testing.T) {
	f := &fakeSudo{mode: "nopasswd", file: []byte("root=1\n")}
	m, sc := sudoRig(t, f)
	got, err := m.SudoReadText("srv", sudoTestFile, "")
	if err != nil || got != "root=1\n" {
		t.Fatalf("leer: %q %v", got, err)
	}
	if err := m.SudoWriteText("srv", sudoTestFile, "root=2\n", "ignorada"); err != nil {
		t.Fatal(err)
	}
	if f.content() != "root=2\n" {
		t.Fatalf("contenido: %q", f.content())
	}
	cmds, stdins := f.log()
	sameList(t, "comandos", cmds, []string{readCmd(sudoN), writeCmd(sudoN)})
	sameList(t, "stdin", stdins, []string{"", "root=2\n"})
	if sc.sudoPassword() != "" {
		t.Fatal("sin validar no se recuerda ninguna contraseña")
	}
}

// Si sudo pide contraseña: el frontend la pregunta, se valida sola y se recuerda hasta cerrar la conexión.
func TestSudoWithPassword(t *testing.T) {
	f := &fakeSudo{mode: "passwd", file: []byte("a\n")}
	m, sc := sudoRig(t, f)
	if _, err := m.SudoReadText("srv", sudoTestFile, ""); !isCode(err, CodeSudoPassword) {
		t.Fatalf("sin contraseña: %v", err)
	}
	got, err := m.SudoReadText("srv", sudoTestFile, "secreta")
	if err != nil || got != "a\n" {
		t.Fatalf("leer: %q %v", got, err)
	}
	if sc.sudoPassword() != "secreta" {
		t.Fatal("la contraseña que funcionó se recuerda")
	}

	// Venció la validación: se vuelve a validar con la recordada, sin preguntar.
	f.mu.Lock()
	f.validated = false
	f.mu.Unlock()
	if err := m.SudoWriteText("srv", sudoTestFile, "b\n", ""); err != nil {
		t.Fatal(err)
	}
	if f.content() != "b\n" {
		t.Fatalf("contenido: %q", f.content())
	}
	cmds, stdins := f.log()
	sameList(t, "comandos", cmds, []string{
		readCmd(sudoN),
		readCmd(sudoN), sudoValidateCmd, readCmd(sudoN),
		writeCmd(sudoN), sudoValidateCmd, writeCmd(sudoN),
	})
	// La contraseña va sola, y solo a la validación; el contenido, solo a tee.
	sameList(t, "stdin", stdins, []string{"", "", "secreta\n", "", "b\n", "secreta\n", "b\n"})

	m.Close("srv")
	if sc.sudoPassword() != "" {
		t.Fatal("al cerrar la conexión se olvida la contraseña")
	}
}

// Una contraseña incorrecta es un solo intento fallido y no se recuerda.
func TestSudoBadPassword(t *testing.T) {
	f := &fakeSudo{mode: "passwd", file: []byte("a\n")}
	m, sc := sudoRig(t, f)
	if err := m.SudoWriteText("srv", sudoTestFile, "pisado\n", "mala"); !isCode(err, CodeSudoBadPassword) {
		t.Fatalf("error: %v", err)
	}
	f.mu.Lock()
	attempts := f.attempts
	f.mu.Unlock()
	if attempts != 1 || f.content() != "a\n" || sc.sudoPassword() != "" {
		t.Fatalf("intentos=%d contenido=%q recordada=%q", attempts, f.content(), sc.sudoPassword())
	}
	cmds, _ := f.log()
	sameList(t, "comandos", cmds, []string{writeCmd(sudoN), sudoValidateCmd})
}

// Con timestamp_timeout=0 se puede leer (contraseña sola por stdin a sudo -S) pero no guardar.
func TestSudoNoTimestamp(t *testing.T) {
	f := &fakeSudo{mode: "nots", file: []byte("a\n")}
	m, _ := sudoRig(t, f)
	got, err := m.SudoReadText("srv", sudoTestFile, "secreta")
	if err != nil || got != "a\n" {
		t.Fatalf("leer: %q %v", got, err)
	}
	err = m.SudoWriteText("srv", sudoTestFile, "pisado\n", "")
	if err == nil || !strings.Contains(err.Error(), "timestamp_timeout=0") {
		t.Fatalf("guardar: %v", err)
	}
	if f.content() != "a\n" {
		t.Fatalf("contenido: %q", f.content())
	}
	cmds, stdins := f.log()
	sameList(t, "comandos", cmds, []string{
		readCmd(sudoN), sudoValidateCmd, readCmd(sudoN), readCmd(sudoS),
		writeCmd(sudoN), sudoValidateCmd, writeCmd(sudoN),
	})
	for i, in := range stdins {
		if strings.Contains(in, "secreta") && in != "secreta\n" {
			t.Fatalf("la contraseña se mezcló con otra cosa en %q: %q", cmds[i], in)
		}
	}
}

// Los fallos de sudo se explican sin su texto crudo.
func TestSudoErrors(t *testing.T) {
	for mode, want := range map[string]string{
		"requiretty": "requiretty",
		"nosudoers":  "no tiene permiso para usar sudo",
		"nosudo":     "no tiene sudo",
	} {
		m, _ := sudoRig(t, &fakeSudo{mode: mode})
		_, err := m.SudoReadText("srv", sudoTestFile, "secreta")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", mode, err)
		}
	}

	// Sin exec: el error de siempre.
	m := newBridgeManager(t)
	m.register(&Session{ID: "srv", Kind: KindSFTP, ConnID: "c", shared: newSharedClient(forwardingServer(t))})
	if _, err := m.SudoReadText("srv", sudoTestFile, ""); !errors.Is(err, errNoShell) {
		t.Fatalf("sin exec: %v", err)
	}
}

// La codificación se respeta igual que en la edición normal, y el límite de 2 MB también.
func TestSudoEncodingAndLimit(t *testing.T) {
	f := &fakeSudo{mode: "nopasswd", file: []byte("ma\xf1ana\n")}
	m, _ := sudoRig(t, f)
	got, err := m.SudoReadText("srv", sudoTestFile, "")
	if err != nil || got != "mañana\n" {
		t.Fatalf("leer: %q %v", got, err)
	}
	if err := m.SudoWriteText("srv", sudoTestFile, "mañana más\n", ""); err != nil {
		t.Fatal(err)
	}
	if f.content() != "ma\xf1ana m\xe1s\n" {
		t.Fatalf("se guardó en otra codificación: %q", f.content())
	}

	f.mu.Lock()
	f.file = bytes.Repeat([]byte("a"), maxEditSize+1)
	f.mu.Unlock()
	if _, err := m.SudoReadText("srv", sudoTestFile, ""); !errors.Is(err, errTooBig) {
		t.Fatalf("más de 2 MB: %v", err)
	}
}

// Un permiso denegado por SFTP viaja con el código PERMISSION; el resto de los errores, igual que antes.
func TestPermissionErr(t *testing.T) {
	denied := &sftp.StatusError{Code: uint32(sftp.ErrSSHFxPermissionDenied)}
	if err := permissionErr(denied, "No tenés permiso", "read"); !isCode(err, CodePermission) {
		t.Fatalf("permiso: %v", err)
	}
	if err := permissionErr(os.ErrNotExist, "x", "read"); err != os.ErrNotExist {
		t.Fatalf("otro error: %v", err)
	}
	if permissionErr(nil, "x", "read") != nil {
		t.Fatal("sin error")
	}
}

package remote

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// memSFTP levanta un servidor SFTP en memoria conectado por pipes.
func memSFTP(t *testing.T) *sftp.Client {
	t.Helper()
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	srv := sftp.NewRequestServer(struct {
		io.Reader
		io.WriteCloser
	}{sr, sw}, sftp.InMemHandler())
	go srv.Serve()
	cl, err := sftp.NewClientPipe(cr, cw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Cerrar los pipes primero: si no, cl.Close espera respuestas para siempre.
		sw.Close()
		cw.Close()
		srv.Close()
		cl.Close()
	})
	return cl
}

func put(t *testing.T, cl *sftp.Client, p, content string) {
	t.Helper()
	f, err := cl.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte(content))
	f.Close()
}

func get(t *testing.T, cl *sftp.Client, p string) string {
	t.Helper()
	f, err := cl.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	return string(b)
}

func assertNoTemps(t *testing.T, cl *sftp.Client, dir string) {
	t.Helper()
	infos, _ := cl.ReadDir(dir)
	for _, fi := range infos {
		if strings.Contains(fi.Name(), ".osat-") {
			t.Errorf("quedó un temporal: %s", fi.Name())
		}
	}
}

func TestWriteRemoteReplaces(t *testing.T) {
	cl := memSFTP(t)
	cl.Mkdir("/d")
	put(t, cl, "/d/a.txt", "viejo contenido largo")

	err := writeRemote(context.Background(), cl, "/d/a.txt", strings.NewReader("nuevo"), 5, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := get(t, cl, "/d/a.txt"); got != "nuevo" {
		t.Fatalf("contenido = %q", got)
	}
	assertNoTemps(t, cl, "/d")
}

// Si la escritura se corta (cancelación, conexión caída), el original queda intacto.
func TestWriteRemoteCancelKeepsOriginal(t *testing.T) {
	cl := memSFTP(t)
	cl.Mkdir("/d")
	put(t, cl, "/d/a.txt", "original")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := writeRemote(ctx, cl, "/d/a.txt", strings.NewReader(strings.Repeat("x", 1<<20)), 1<<20, 0, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; quería context.Canceled", err)
	}
	if got := get(t, cl, "/d/a.txt"); got != "original" {
		t.Fatalf("el original cambió: %q", got)
	}
	assertNoTemps(t, cl, "/d")
}

func TestWriteRemoteNewFileCancelLeavesNothing(t *testing.T) {
	cl := memSFTP(t)
	cl.Mkdir("/d")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = writeRemote(ctx, cl, "/d/nuevo.bin", strings.NewReader("abc"), 3, 0o644, nil)
	if _, err := cl.Lstat("/d/nuevo.bin"); err == nil {
		t.Fatal("quedó un archivo parcial")
	}
	assertNoTemps(t, cl, "/d")
}

func TestWriteRemoteFollowsSymlink(t *testing.T) {
	cl := memSFTP(t)
	cl.Mkdir("/d")
	put(t, cl, "/d/real.txt", "viejo")
	if err := cl.Symlink("/d/real.txt", "/d/link.txt"); err != nil {
		t.Skip("el servidor en memoria no soporta symlinks:", err)
	}
	if err := writeRemote(context.Background(), cl, "/d/link.txt", strings.NewReader("nuevo"), 5, 0, nil); err != nil {
		t.Fatal(err)
	}
	if fi, err := cl.Lstat("/d/link.txt"); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("el symlink fue reemplazado por un archivo: %v %v", fi, err)
	}
	if got := get(t, cl, "/d/real.txt"); got != "nuevo" {
		t.Fatalf("destino del link = %q", got)
	}
}

func TestCheckConflicts(t *testing.T) {
	names := []string{"a", "b", "c"}
	exists := func(i int) bool { return i != 1 }

	_, err := checkConflicts(ConflictAsk, names, exists)
	var ke *Error
	if !errors.As(err, &ke) || ke.Code != CodeConflict || ke.Data["names"] != "a\nc" || ke.Data["count"] != "2" {
		t.Fatalf("ask: err = %v", err)
	}
	skip, err := checkConflicts(ConflictSkip, names, exists)
	if err != nil || !skip[0] || skip[1] || !skip[2] {
		t.Fatalf("skip: %v %v", skip, err)
	}
	skip, err = checkConflicts(ConflictOverwrite, names, exists)
	if err != nil || len(skip) != 0 {
		t.Fatalf("overwrite: %v %v", skip, err)
	}
	if _, err := checkConflicts(ConflictAsk, names, func(int) bool { return false }); err != nil {
		t.Fatalf("sin conflictos: %v", err)
	}
}

func TestKeyboardInteractive(t *testing.T) {
	var asked []PromptRequest
	prompt := func(r PromptRequest) ([]string, error) {
		asked = append(asked, r)
		return []string{"123456"}, nil
	}
	pauses := 0
	pause := func() func() { pauses++; return func() {} }
	ch := keyboardInteractive("h:22", "secreto", prompt, pause).(ssh.KeyboardInteractiveChallenge)

	// La pregunta de contraseña se contesta sola, una única vez.
	if ans, err := ch("", "", []string{"Password: "}, []bool{false}); err != nil || ans[0] != "secreto" {
		t.Fatalf("password: %v %v", ans, err)
	}
	// El OTP se le pregunta al usuario, con el deadline pausado.
	if ans, err := ch("", "", []string{"Verification code: "}, []bool{false}); err != nil || ans[0] != "123456" {
		t.Fatalf("otp: %v %v", ans, err)
	}
	if len(asked) != 1 || asked[0].Questions[0].Text != "Verification code: " || pauses != 1 {
		t.Fatalf("asked=%v pauses=%d", asked, pauses)
	}
	// Un segundo pedido de contraseña (p. ej. la anterior era incorrecta) ya no se autocontesta.
	if _, err := ch("", "", []string{"Password: "}, []bool{false}); err != nil || len(asked) != 2 {
		t.Fatalf("segundo password: asked=%d err=%v", len(asked), err)
	}
	// Ronda informativa sin preguntas.
	if ans, err := ch("", "Bienvenido", nil, nil); err != nil || len(ans) != 0 {
		t.Fatalf("info: %v %v", ans, err)
	}
}

func TestKeyboardInteractiveCancel(t *testing.T) {
	prompt := func(PromptRequest) ([]string, error) { return nil, errPromptCancelled }
	ch := keyboardInteractive("h:22", "", prompt, func() func() { return func() {} }).(ssh.KeyboardInteractiveChallenge)
	_, err := ch("", "", []string{"Code: "}, []bool{true})
	var ke *Error
	if !errors.As(err, &ke) || ke.Code != CodeCancelled {
		t.Fatalf("err = %v", err)
	}
}

func TestSharedClientNoResurrect(t *testing.T) {
	sh := newSharedClient(&ssh.Client{})
	sh.refs = 1
	sh.client = nil // no queremos cerrar nada real
	sh.mu.Lock()
	sh.refs = 0 // simula que la última pestaña se cerró
	sh.mu.Unlock()
	if err := sh.acquire(); !errors.Is(err, errConnClosed) {
		t.Fatalf("acquire sobre conexión liberada: %v", err)
	}
	sh.release() // no debe ir a negativo ni cerrar de nuevo
	if sh.refs != 0 {
		t.Fatalf("refs = %d", sh.refs)
	}
}

func TestWriteRemoteProgressCounter(t *testing.T) {
	cl := memSFTP(t)
	cl.Mkdir("/d")
	var ctr atomic.Int64
	ctr.Store(100) // bytes ya contados de otros archivos de la carpeta
	if err := writeRemote(context.Background(), cl, "/d/x", strings.NewReader("hola"), 4, 0, &ctr); err != nil {
		t.Fatal(err)
	}
	if ctr.Load() != 104 {
		t.Fatalf("contador = %d", ctr.Load())
	}
}

func TestMoveFiles(t *testing.T) {
	cl := memSFTP(t)
	cl.MkdirAll("/d/sub")
	cl.MkdirAll("/d/otra")
	put(t, cl, "/d/a.txt", "a")
	put(t, cl, "/d/b.txt", "b")
	put(t, cl, "/d/sub/b.txt", "ya estaba")

	// Mover archivo y carpeta a otra carpeta.
	if err := moveFiles(cl, []string{"/d/a.txt", "/d/otra"}, "/d/sub"); err != nil {
		t.Fatal(err)
	}
	if got := get(t, cl, "/d/sub/a.txt"); got != "a" {
		t.Fatalf("a.txt no se movió: %q", got)
	}
	if fi, err := cl.Stat("/d/sub/otra"); err != nil || !fi.IsDir() {
		t.Fatalf("la carpeta no se movió: %v", err)
	}

	// Conflicto: no pisa y no mueve nada.
	var ke *Error
	err := moveFiles(cl, []string{"/d/b.txt"}, "/d/sub")
	if !errors.As(err, &ke) || ke.Code != CodeConflict || ke.Data["names"] != "b.txt" {
		t.Fatalf("esperaba CONFLICT, obtuve %v", err)
	}
	if get(t, cl, "/d/sub/b.txt") != "ya estaba" || get(t, cl, "/d/b.txt") != "b" {
		t.Fatal("el conflicto modificó archivos")
	}

	// Una carpeta no puede ir dentro de sí misma.
	if err := moveFiles(cl, []string{"/d/sub"}, "/d/sub/otra"); err == nil {
		t.Fatal("esperaba error al mover una carpeta dentro de sí misma")
	}
	// Soltar en la carpeta donde ya está no hace nada.
	if err := moveFiles(cl, []string{"/d/b.txt"}, "/d"); err != nil {
		t.Fatal(err)
	}
}

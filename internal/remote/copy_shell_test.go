//go:build shelltest

// Pruebas de la copia por shell (cp -a) contra un sshd mínimo en proceso que ejecuta
// comandos con sh de verdad. Van aparte (no corren con `go test` a secas) porque
// necesitan un sh en el PATH y porque algunos antivirus bloquean el binario de
// pruebas (un servidor SSH que lanza procesos) y con él todas las pruebas del paquete.
// Uso: go test -tags shelltest -run TestCopy ./internal/remote/ -v

package remote

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// testServer es un sshd mínimo en proceso: sirve SFTP sobre el disco real y,
// según mode, ejecuta los comandos con sh de verdad o falla de distintas formas.
type testServer struct {
	mode string // "real" | "reject" (rechaza exec) | "notfound" (127) | "fail" (cp falla: exit 1)

	mu    sync.Mutex
	execs []string
}

func (s *testServer) commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.execs...)
}

// lagConn retrasa cada escritura (el tiempo de ida de un enlace con latencia).
type lagConn struct {
	net.Conn
	d time.Duration
}

func (c lagConn) Write(b []byte) (int, error) {
	time.Sleep(c.d)
	return c.Conn.Write(b)
}

func startTestServer(t *testing.T, mode string, lag time.Duration) (*testServer, *ssh.Client, *sftp.Client) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil && mode == "real" {
		t.Skip("no hay sh en el PATH")
	}
	srv := &testServer{mode: mode}
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{NoClientAuth: true}
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
				_, chans, reqs, err := ssh.NewServerConn(lagConn{nc, lag}, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for nch := range chans {
					go srv.session(nch)
				}
			}()
		}
	}()
	nc, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	ccfg := &ssh.ClientConfig{User: "t", HostKeyCallback: ssh.InsecureIgnoreHostKey()}
	sc, chans, reqs, err := ssh.NewClientConn(lagConn{nc, lag}, ln.Addr().String(), ccfg)
	if err != nil {
		t.Fatal(err)
	}
	c := ssh.NewClient(sc, chans, reqs)
	cl, err := sftp.NewClient(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cl.Close(); c.Close() })
	return srv, c, cl
}

func (s *testServer) session(nch ssh.NewChannel) {
	if nch.ChannelType() != "session" {
		nch.Reject(ssh.UnknownChannelType, "no")
		return
	}
	ch, reqs, err := nch.Accept()
	if err != nil {
		return
	}
	for req := range reqs {
		switch req.Type {
		case "subsystem":
			ok := len(req.Payload) > 4 && string(req.Payload[4:]) == "sftp"
			req.Reply(ok, nil)
			if ok {
				srv, err := sftp.NewServer(ch)
				if err != nil {
					return
				}
				go func() { srv.Serve(); srv.Close() }()
			}
		case "exec":
			if s.mode == "reject" {
				req.Reply(false, nil)
				continue
			}
			cmd := string(req.Payload[4:])
			if int(binary.BigEndian.Uint32(req.Payload[:4])) != len(cmd) {
				req.Reply(false, nil)
				continue
			}
			req.Reply(true, nil)
			s.mu.Lock()
			s.execs = append(s.execs, cmd)
			s.mu.Unlock()
			go func() {
				status := 0
				switch s.mode {
				case "notfound":
					ch.Stderr().Write([]byte("sh: cp: command not found\n"))
					status = 127
				case "fail":
					ch.Stderr().Write([]byte("cp: cannot create regular file: Permission denied\n"))
					status = 1
				default:
					c := exec.Command("sh", "-c", cmd)
					c.Stderr = ch.Stderr()
					if err := c.Run(); err != nil {
						status = 1
						if ee, ok := err.(*exec.ExitError); ok {
							status = ee.ExitCode()
						}
					}
				}
				ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(status)}))
				ch.Close()
			}()
		default:
			req.Reply(false, nil)
		}
	}
}

func TestCopyViaShell(t *testing.T) {
	srv, c, cl := startTestServer(t, "real", 0)
	sh := &shell{c: c, off: new(atomic.Bool)}
	ctx := context.Background()

	// Rutas del disco real, en el formato que usan SFTP y cp (con "/", también en Windows).
	dir := filepath.ToSlash(t.TempDir())
	write := func(rel, content string) string {
		p := dir + "/" + rel
		os.MkdirAll(filepath.Dir(filepath.FromSlash(p)), 0o755)
		if err := os.WriteFile(filepath.FromSlash(p), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cpp := write("main.cpp", "// informaci\xf3n\r\nint main(){}\r\n")
	odd := write("it's a file (1).txt", "comillas y espacios")
	write("src/a.txt", "a")
	write("src/sub/b.txt", "bb")

	got, err := copyFiles(ctx, cl, sh, []string{cpp, odd, dir + "/src"}, dir)
	want := []string{dir + "/main - copia.cpp", dir + "/it's a file (1) - copia.txt", dir + "/src - copia"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("copyFiles = %v, %v\nquería %v", got, err, want)
	}
	read := func(p string) string {
		b, err := os.ReadFile(filepath.FromSlash(p))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if read(got[0]) != "// informaci\xf3n\r\nint main(){}\r\n" || read(got[1]) != "comillas y espacios" || read(dir+"/src - copia/sub/b.txt") != "bb" {
		t.Error("el contenido copiado no coincide")
	}

	// Una sola ejecución de cp por elemento (no por archivo): un viaje sin importar el tamaño.
	cmds := srv.commands()
	if len(cmds) != 3 {
		t.Fatalf("comandos ejecutados = %d, quería 3: %q", len(cmds), cmds)
	}
	for _, c := range cmds {
		if !strings.HasPrefix(c, "cp -a -- '") {
			t.Errorf("comando inesperado: %q", c)
		}
	}
	if !strings.Contains(cmds[1], `'\''`) {
		t.Errorf("la comilla simple del nombre no se escapó: %q", cmds[1])
	}
	if sh.off.Load() {
		t.Error("el shell no debería haberse marcado como no disponible")
	}
}

func TestCopyShellFallsBackToSFTP(t *testing.T) {
	for _, mode := range []string{"reject", "notfound"} {
		t.Run(mode, func(t *testing.T) {
			srv, c, cl := startTestServer(t, mode, 0)
			sh := &shell{c: c, off: new(atomic.Bool)}
			dir := filepath.ToSlash(t.TempDir())
			p := dir + "/a.txt"
			if err := os.WriteFile(filepath.FromSlash(p), []byte("hola"), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := copyFiles(context.Background(), cl, sh, []string{p}, dir)
			if err != nil || !reflect.DeepEqual(got, []string{dir + "/a - copia.txt"}) {
				t.Fatalf("copyFiles = %v, %v", got, err)
			}
			if b, _ := os.ReadFile(filepath.FromSlash(dir + "/a - copia.txt")); string(b) != "hola" {
				t.Errorf("contenido = %q", b)
			}
			if !sh.off.Load() {
				t.Error("tenía que recordar que el servidor no permite comandos")
			}
			// La segunda copia ya no vuelve a intentar el shell.
			n := len(srv.commands())
			if _, err := copyFiles(context.Background(), cl, sh, []string{p}, dir); err != nil {
				t.Fatal(err)
			}
			if len(srv.commands()) != n {
				t.Error("reintentó ejecutar un comando en un servidor que ya lo rechazó")
			}
		})
	}
}

// Si cp falla por otra razón (permisos, disco lleno…) no se reintenta por SFTP:
// se muestra el error de cp.
func TestCopyShellReportsCpError(t *testing.T) {
	_, c, cl := startTestServer(t, "fail", 0)
	sh := &shell{c: c, off: new(atomic.Bool)}
	dir := filepath.ToSlash(t.TempDir())
	p := dir + "/a.txt"
	os.WriteFile(filepath.FromSlash(p), []byte("hola"), 0o644)
	_, err := copyFiles(context.Background(), cl, sh, []string{p}, dir)
	if err == nil || !strings.Contains(err.Error(), "Permission denied") {
		t.Fatalf("err = %v", err)
	}
	if sh.off.Load() {
		t.Error("un error de cp no debería desactivar el shell")
	}
	if _, err := os.Stat(filepath.FromSlash(dir + "/a - copia.txt")); err == nil {
		t.Error("no debería haberse creado la copia por SFTP")
	}
}

// Con latencia, copiar por el shell cuesta unos pocos viajes y no depende del
// tamaño (por SFTP, un archivo de 1 MB eran ~100 viajes).
func TestCopyViaShellRoundTrips(t *testing.T) {
	const oneWay = 10 * time.Millisecond
	const rtt = 2 * oneWay
	_, c, cl := startTestServer(t, "real", oneWay)
	sh := &shell{c: c, off: new(atomic.Bool)}
	dir := filepath.ToSlash(t.TempDir())
	small, big := dir+"/chico.txt", dir+"/1mb.bin"
	os.WriteFile(filepath.FromSlash(small), []byte("hola"), 0o644)
	os.WriteFile(filepath.FromSlash(big), []byte(strings.Repeat("x", 1<<20)), 0o644)

	trips := func(p string) float64 {
		start := time.Now()
		if _, err := copyFiles(context.Background(), cl, sh, []string{p}, dir); err != nil {
			t.Fatal(err)
		}
		return float64(time.Since(start)) / float64(rtt)
	}
	a, b := trips(small), trips(big)
	t.Logf("4 bytes: ≈ %.1f RTT · 1 MB: ≈ %.1f RTT", a, b)
	if b > 12 || b > a+3 {
		t.Errorf("copiar 1 MB cuesta ≈ %.1f viajes (4 bytes: %.1f): no debería depender del tamaño", b, a)
	}
}

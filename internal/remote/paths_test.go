package remote

import (
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestLocalNameFor(t *testing.T) {
	ok := []struct{ goos, in, want string }{
		{"windows", "informe.pdf", "informe.pdf"},
		{"windows", `..\..\Startup\x.bat`, ".._.._Startup_x.bat"},
		{"windows", "backup 12:00.tar", "backup 12_00.tar"},
		{"windows", "a*b?c", "a_b_c"},
		{"windows", "CON", "_CON"},
		{"windows", "nul.txt", "_nul.txt"},
		{"windows", "fin. ", "fin"},
		{"windows", "...", "_"},
		{"linux", `a\b:c`, `a\b:c`},
	}
	for _, c := range ok {
		got, err := localNameFor(c.goos, c.in)
		if err != nil || got != c.want {
			t.Errorf("localNameFor(%s, %q) = %q, %v; quería %q", c.goos, c.in, got, err, c.want)
		}
	}
	for _, in := range []string{"", ".", "..", "a/b", "../x", "a\x00b"} {
		for _, goos := range []string{"windows", "linux"} {
			if got, err := localNameFor(goos, in); err == nil {
				t.Errorf("localNameFor(%s, %q) = %q; esperaba error", goos, in, got)
			}
		}
	}
}

func TestLocalPathUnder(t *testing.T) {
	root := filepath.Join(t.TempDir(), "dest")
	got, err := localPathUnder(root, "/srv/data/", "/srv/data/sub/file.txt")
	if err != nil || got != filepath.Join(root, "sub", "file.txt") {
		t.Fatalf("got %q, %v", got, err)
	}
	if got, err := localPathUnder(root, "/srv/data", "/srv/data"); err != nil || got != root {
		t.Fatalf("raíz: got %q, %v", got, err)
	}
	// Rutas que el servidor podría devolver para escapar de la carpeta.
	for _, rp := range []string{"/srv/data/../etc/passwd", "/srv/database/x", "/etc/passwd"} {
		if got, err := localPathUnder(root, "/srv/data", rp); err == nil {
			t.Errorf("localPathUnder(%q) = %q; esperaba error", rp, got)
		}
	}
	// Ningún nombre remoto puede generar una ruta fuera de root.
	for _, name := range []string{`..\..\evil.bat`, `x\..\..\y`, "a:b"} {
		got, err := localPathUnder(root, "/srv/data", "/srv/data/"+name)
		if err != nil {
			continue
		}
		if rel, _ := filepath.Rel(root, got); rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Errorf("%q escapó de la carpeta: %q", name, got)
		}
	}
}

// Un servidor que acepta la conexión y nunca habla no debe colgar el dial.
func TestDialHandshakeTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()

	prev := handshakeTimeout
	handshakeTimeout = 300 * time.Millisecond
	defer func() { handshakeTimeout = prev }()

	cfg := &ssh.ClientConfig{User: "x", HostKeyCallback: ssh.InsecureIgnoreHostKey()}
	start := time.Now()
	_, err = dialWithDeadline(ln.Addr().String(), cfg, nil, nil)
	if err == nil {
		t.Fatal("esperaba error de timeout")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("el dial tardó %v", d)
	}
}

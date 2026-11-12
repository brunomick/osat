package localterm

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// collect lee la salida de p en segundo plano.
type collect struct {
	mu  sync.Mutex
	buf strings.Builder
}

func newCollect(p Proc) *collect {
	c := &collect{}
	go func() {
		b := make([]byte, 4096)
		for {
			n, err := p.Read(b)
			c.mu.Lock()
			c.buf.Write(b[:n])
			c.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return c
}

func (c *collect) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// waitFor espera hasta que la salida contenga want.
func (c *collect) waitFor(t *testing.T, want string) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
		if strings.Contains(c.String(), want) {
			return
		}
	}
	t.Fatalf("no apareció %q en la salida: %q", want, c.String())
}

func TestStartFallsBackToHomeForMissingFolder(t *testing.T) {
	home, _ := os.UserHomeDir()
	if got := workDir(filepath.Join(os.TempDir(), "osat-no-existe-xyz")); got != home {
		t.Fatalf("workDir = %q, quería la carpeta personal %q", got, home)
	}
	if got := workDir(""); got != home {
		t.Fatalf("workDir(\"\") = %q, quería %q", got, home)
	}
}

func TestStartRejectsUnknownShell(t *testing.T) {
	if _, _, err := Start("no-existe", "", 80, 24); err == nil {
		t.Fatal("pedir una shell que no está debería fallar")
	}
}

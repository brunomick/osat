//go:build windows

package localterm

import (
	"os"
	"path/filepath"
	"testing"
)

// Cada shell avisa la carpeta (OSC 9;9) al mostrar el prompt, también después de un cd.
func TestShellsReportTheirFolder(t *testing.T) {
	ids := map[string]bool{}
	for _, s := range Shells() {
		ids[s.ID] = true
	}
	for _, c := range []struct{ id, cd string }{
		{"powershell", "cd sub\r"},
		{"pwsh", "cd sub\r"},
		{"cmd", "cd sub\r\n"},
		{"gitbash", "cd sub\n"},
		{"wsl", "cd sub\n"},
	} {
		t.Run(c.id, func(t *testing.T) {
			if !ids[c.id] {
				t.Skip("no está instalada")
			}
			dir, err := os.MkdirTemp("", "osat-cwd-*")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(dir) })
			dir, _ = filepath.EvalSymlinks(dir)
			sub := filepath.Join(dir, "sub")
			if err := os.Mkdir(sub, 0o755); err != nil {
				t.Fatal(err)
			}
			p, _, err := Start(c.id, dir, 120, 30)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { p.Close() })
			out := newCollect(p)
			// Solo el final de la ruta: la carpeta temporal puede venir corta (C:\Users\BTM~1\...).
			out.waitFor(t, "\\"+filepath.Base(dir)+"\x1b\\")
			p.Write([]byte(c.cd))
			out.waitFor(t, "\\"+filepath.Base(dir)+"\\sub\x1b\\")
		})
	}
}

func TestSetEnvReplacesTheKey(t *testing.T) {
	got := setEnv([]string{"A=1", "Prompt=$P$G", "B=2"}, "PROMPT=x")
	want := []string{"A=1", "B=2", "PROMPT=x"}
	if len(got) != len(want) {
		t.Fatalf("got %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

// En WSL, una carpeta de Linux se avisa por la red interna de WSL (\\wsl.localhost\<distro>\...).
func TestWSLReportsLinuxFoldersAsWSLLocalhost(t *testing.T) {
	p, _, err := Start("wsl", "", 120, 30)
	if err != nil {
		t.Skip("WSL no está disponible:", err)
	}
	t.Cleanup(func() { p.Close() })
	out := newCollect(p)
	p.Write([]byte("cd /etc\n"))
	out.waitFor(t, `]9;9;\\wsl.localhost\`)
	out.waitFor(t, `\etc`+"\x1b\\")
}

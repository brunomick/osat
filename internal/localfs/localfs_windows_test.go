//go:build windows

package localfs

import (
	"os/exec"
	"strings"
	"testing"
)

// Una carpeta de Linux de WSL (la avisa una terminal de WSL) se lista como cualquier otra,
// con su carpeta superior y sus migas hasta la raíz de la distribución.
func TestListWSLFolder(t *testing.T) {
	out, err := exec.Command("wsl.exe", "-e", "printenv", "WSL_DISTRO_NAME").Output()
	distro := strings.TrimSpace(string(out))
	if err != nil || distro == "" {
		t.Skip("WSL no está disponible")
	}
	root := `\\wsl.localhost\` + distro + `\`
	l, err := List(root + "etc")
	if err != nil {
		t.Fatal(err)
	}
	if l.Parent != root {
		t.Errorf("Parent = %q, want %q", l.Parent, root)
	}
	if len(l.Entries) == 0 {
		t.Error("/etc vacía")
	}
	names := []string{}
	for _, c := range l.Crumbs {
		names = append(names, c.Name)
	}
	if got := strings.Join(names[1:], " › "); got != `\\wsl.localhost\`+distro+" › etc" {
		t.Errorf("migas = %q", got)
	}
	if _, err := List(root); err != nil {
		t.Errorf("la raíz de la distribución: %v", err)
	}
}

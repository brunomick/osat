//go:build shelltest

// Control de versiones contra el servidor de prueba de copy_shell_test.go, que ejecuta los comandos
// con sh y git de verdad sobre el disco real. Necesita sh y git en el PATH (en Windows, los de Git).
// Uso: go test -tags shelltest -run TestGitShell ./internal/remote/ -v

package remote

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func TestGitShell(t *testing.T) {
	// Sin la configuración global de git de esta PC: así falta la identidad, como en un servidor nuevo.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "gitconfig"))

	_, c, cl := startTestServer(t, "real", 0)
	m := newBridgeManager(t)
	m.register(&Session{ID: "srv", Kind: KindSFTP, ConnID: "c", sftp: cl, shared: newSharedClient(c)})

	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	os.Mkdir(repo, 0o755)
	dir := filepath.ToSlash(repo)
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	status := func() GitStatus {
		t.Helper()
		st, err := m.GitStatus("srv", dir)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	file := func(st GitStatus, path string) *GitFile {
		for i := range st.Files {
			if st.Files[i].Path == path {
				return &st.Files[i]
			}
		}
		return nil
	}

	if st := status(); st.IsRepo {
		t.Fatalf("una carpeta sin git no es un repositorio: %+v", st)
	}

	git("init", "-q", "-b", "main")
	write("a.txt", "uno\n")
	st := status()
	if !st.IsRepo || st.Branch != "main" || st.Head != "" || file(st, "a.txt") == nil || file(st, "a.txt").X != "?" {
		t.Fatalf("repositorio nuevo: %+v", st)
	}
	root := st.Root

	if err := m.GitStage("srv", root, []string{"a.txt"}); err != nil {
		t.Fatal(err)
	}
	if f := file(status(), "a.txt"); f == nil || f.X != "A" {
		t.Fatalf("preparado: %+v", f)
	}

	// Sin user.name ni user.email, git no commitea: el frontend pide la identidad.
	var ke *Error
	if err := m.GitCommit("srv", root, "primero"); !errors.As(err, &ke) || ke.Code != CodeGitIdentity {
		t.Fatalf("sin identidad: %v", err)
	}
	if err := m.GitSetIdentity("srv", root, "Prueba", "prueba@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := m.GitCommit("srv", root, "primero\n\ncon 'comillas' y $variables"); err != nil {
		t.Fatal(err)
	}
	st = status()
	if st.Head == "" || st.Subject != "primero" || len(st.Files) != 0 {
		t.Fatalf("después del commit: %+v", st)
	}

	// Diff: lo del último commit, lo preparado y descartar.
	write("a.txt", "dos\n")
	if f := file(status(), "a.txt"); f == nil || f.Y != "M" {
		t.Fatalf("modificado: %+v", f)
	}
	if text, ok, err := m.GitFileAt("srv", root, "HEAD", "a.txt"); err != nil || !ok || text != "uno\n" {
		t.Fatalf("HEAD:a.txt = %q %v %v", text, ok, err)
	}
	if _, ok, err := m.GitFileAt("srv", root, "HEAD", "no-existe.txt"); err != nil || ok {
		t.Fatalf("un archivo que no está en HEAD: %v %v", ok, err)
	}
	m.GitStage("srv", root, []string{"a.txt"})
	if text, _, err := m.GitFileAt("srv", root, "", "a.txt"); err != nil || text != "dos\n" {
		t.Fatalf(":a.txt = %q %v", text, err)
	}
	if err := m.GitUnstage("srv", root, []string{"a.txt"}); err != nil {
		t.Fatal(err)
	}
	if f := file(status(), "a.txt"); f == nil || f.X != "." || f.Y != "M" {
		t.Fatalf("sacado de los preparados: %+v", f)
	}
	write("nuevo.txt", "x")
	if err := m.GitDiscard("srv", root, []string{"a.txt"}, []string{"nuevo.txt"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "a.txt")); string(b) != "uno\n" {
		t.Fatalf("descartar: a.txt = %q", b)
	}
	if _, err := os.Stat(filepath.Join(repo, "nuevo.txt")); !os.IsNotExist(err) {
		t.Fatal("descartar un archivo nuevo lo borra")
	}

	// Ramas.
	if err := m.GitCheckout("srv", root, "feature/x", true); err != nil {
		t.Fatal(err)
	}
	if b, err := m.GitBranches("srv", root); err != nil || !slices.Equal(b, []string{"feature/x", "main"}) {
		t.Fatalf("ramas: %v %v", b, err)
	}
	if err := m.GitCheckout("srv", root, "-x", false); err == nil {
		t.Fatal("un nombre que empieza con - no es una rama")
	}
	if err := m.GitCheckout("srv", root, "main", false); err != nil {
		t.Fatal(err)
	}

	// Push a un remoto (un repositorio vacío en el disco): la primera vez publica la rama.
	bare := filepath.ToSlash(filepath.Join(base, "remoto.git"))
	if out, err := exec.Command("git", "init", "-q", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	git("remote", "add", "origin", bare)
	if err := m.GitPush("srv", root); err != nil {
		t.Fatal(err)
	}
	st = status()
	if st.Upstream != "origin/main" || st.Ahead != 0 || st.Host != "other" {
		t.Fatalf("después del push: %+v", st)
	}
	write("a.txt", "tres\n")
	m.GitStage("srv", root, []string{"a.txt"})
	m.GitCommit("srv", root, "segundo")
	if st = status(); st.Ahead != 1 {
		t.Fatalf("un commit sin pushear: %+v", st)
	}
	if err := m.GitPush("srv", root); err != nil {
		t.Fatal(err)
	}
	if err := m.GitPull("srv", root); err != nil {
		t.Fatal(err)
	}
	if err := m.GitFetch("srv", root); err != nil {
		t.Fatal(err)
	}
	if st = status(); st.Ahead != 0 || st.Behind != 0 {
		t.Fatalf("sincronizado: %+v", st)
	}
}

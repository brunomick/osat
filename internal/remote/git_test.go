package remote

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseGitStatus(t *testing.T) {
	porcelain := strings.Join([]string{
		"# branch.oid 1234567890abcdef",
		"# branch.head main",
		"# branch.upstream origin/main",
		"# branch.ab +2 -1",
		"1 .M N... 100644 100644 100644 aaa aaa docker-compose.yml",
		"1 M. N... 100644 100644 100644 aaa bbb k8s/deployment.yaml",
		"1 D. N... 100644 000000 000000 aaa 000 nginx.old.conf",
		"2 R. N... 100644 100644 100644 aaa aaa R100 nuevo nombre.txt", "viejo.txt",
		"u UU N... 100644 100644 100644 100644 a b c conflicto.go",
		"? notas con espacios.md",
		"",
	}, "\x00")
	out := "/srv/infra" + rs + porcelain + rs + "git@github.com:acme/infra.git\n" + rs + "a3f9c1\x1fSubir réplicas\x1f20 minutes ago\n"
	st := parseGitStatus(out)
	want := GitStatus{
		IsRepo: true, Root: "/srv/infra", Branch: "main", Upstream: "origin/main", Ahead: 2, Behind: 1,
		Remote: "git@github.com:acme/infra.git", Host: "github",
		Head: "a3f9c1", Subject: "Subir réplicas", When: "20 minutes ago",
		Files: []GitFile{
			{Path: "docker-compose.yml", X: ".", Y: "M"},
			{Path: "k8s/deployment.yaml", X: "M", Y: "."},
			{Path: "nginx.old.conf", X: "D", Y: "."},
			{Path: "nuevo nombre.txt", OrigPath: "viejo.txt", X: "R", Y: "."},
			{Path: "conflicto.go", X: "U", Y: "U"},
			{Path: "notas con espacios.md", X: "?", Y: "?"},
		},
	}
	if !reflect.DeepEqual(st, want) {
		t.Errorf("parseGitStatus:\n got %+v\nwant %+v", st, want)
	}
}

func TestParseGitStatusNewRepo(t *testing.T) {
	// Repositorio recién creado: sin commits, sin remoto, rama sin upstream.
	out := "/home/u/r" + rs + "# branch.oid (initial)\x00# branch.head main\x00? a.txt\x00" + rs + "" + rs + ""
	st := parseGitStatus(out)
	if !st.IsRepo || st.Branch != "main" || st.Upstream != "" || st.Remote != "" || st.Host != "" || st.Head != "" || len(st.Files) != 1 {
		t.Errorf("%+v", st)
	}
}

func TestParseGitStatusDetached(t *testing.T) {
	st := parseGitStatus("/r" + rs + "# branch.oid abc\x00# branch.head (detached)\x00" + rs + rs)
	if st.Branch != "" {
		t.Errorf("detached: rama %q", st.Branch)
	}
}

func TestRemoteHost(t *testing.T) {
	for url, want := range map[string]string{
		"":                                  "",
		"https://github.com/a/b.git":        "github",
		"git@gitlab.example.com:a/b.git":    "gitlab",
		"https://user@bitbucket.org/a/b":    "bitbucket",
		"ssh://git@git.miempresa.local/a/b": "other",
		"/srv/git/infra.git":                "other",
	} {
		if got := remoteHost(url); got != want {
			t.Errorf("remoteHost(%q) = %q, quería %q", url, got, want)
		}
	}
}

func TestGitErr(t *testing.T) {
	var ke *Error
	if err := gitErr(128, "*** Please tell me who you are.\n\nRun git config..."); !errors.As(err, &ke) || ke.Code != CodeGitIdentity {
		t.Errorf("identidad: %v", err)
	}
	if err := gitErr(128, "fatal: detected dubious ownership in repository at '/srv/x'"); err == nil || !strings.Contains(err.Error(), "safe.directory") {
		t.Errorf("dubious ownership: %v", err)
	}
	if err := gitErr(1, "error: failed to push some refs"); err == nil || err.Error() != "error: failed to push some refs" {
		t.Errorf("mensaje de git: %v", err)
	}
}

func TestPathArgs(t *testing.T) {
	got, err := pathArgs([]string{"a b.txt", "it's"})
	if err != nil || got != ` -- 'a b.txt' 'it'\''s'` {
		t.Errorf("pathArgs = %q, %v", got, err)
	}
	if _, err := pathArgs(nil); err == nil {
		t.Error("sin rutas debería ser un error")
	}
	if _, err := pathArgs([]string{""}); err == nil {
		t.Error("una ruta vacía debería ser un error")
	}
}

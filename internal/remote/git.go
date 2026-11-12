package remote

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"osat/internal/i18n"
	"osat/internal/store"
)

// Control de versiones de un repositorio (la pestaña Repositorio): todo corre con el
// git del servidor, por un canal de comandos de la conexión SSH que ya está abierta.
// Cada comando va en `sh -c` (así no depende del shell del usuario) con
// GIT_TERMINAL_PROMPT=0: sin terminal, git falla en vez de quedarse esperando una
// contraseña. Las rutas de los archivos son relativas a la raíz del repositorio.

// CodeGitIdentity: git no sabe quién commitea (falta user.name / user.email en el servidor).
const CodeGitIdentity = "GIT_IDENTITY"

var errNoGit = i18n.Err("remote.gitNotInstalled")

const (
	gitTimeout     = 30 * time.Second
	gitNetTimeout  = 3 * time.Minute // pull y push
	gitMaxFileSize = 4 << 20         // contenido de un archivo para el diff
)

// GitFile es un archivo con cambios. X es su estado en el índice (preparado) e Y en la copia de trabajo,
// con las letras de `git status`: M modificado, A agregado, D borrado, R renombrado, C copiado,
// U en conflicto, ? sin seguimiento, "." sin cambios.
type GitFile struct {
	Path     string `json:"path"`
	OrigPath string `json:"origPath,omitempty"` // renombrados: el nombre anterior
	X        string `json:"x"`
	Y        string `json:"y"`
}

// GitStatus es el estado del repositorio que contiene una carpeta.
type GitStatus struct {
	IsRepo   bool      `json:"isRepo"`
	Root     string    `json:"root"`     // raíz del repositorio en el servidor
	Branch   string    `json:"branch"`   // "" si está en un commit suelto (detached)
	Upstream string    `json:"upstream"` // "origin/main"; "" si la rama no sigue a ninguna
	Ahead    int       `json:"ahead"`    // commits sin pushear
	Behind   int       `json:"behind"`   // commits del remoto que no están acá
	Remote   string    `json:"remote"`   // URL de origin ("" = repositorio solo local)
	Host     string    `json:"host"`     // github, gitlab, bitbucket, other o ""
	Files    []GitFile `json:"files"`
	Head     string    `json:"head"`    // hash corto del último commit ("" si no hay commits)
	Subject  string    `json:"subject"` // su título
	When     string    `json:"when"`    // hace cuánto (como lo dice git, en inglés)
}

// gitRun corre script (sh) en la carpeta dir del servidor y devuelve stdout, el código de salida y stderr.
// stdin, si no es nil, es la entrada del comando. stdout se corta en max bytes (0 = sin límite).
func (m *Manager) gitRun(sessionID, dir, script string, stdin []byte, timeout time.Duration, max int64) (out []byte, status int, stderr string, err error) {
	src, err := m.get(sessionID)
	if err != nil {
		return nil, 0, "", err
	}
	forwardAgent := false
	if c, ok := m.storeConn(src.ConnID); ok {
		forwardAgent = c.ForwardAgent
	}
	err = m.withClient(sessionID, func(c *ssh.Client) error {
		sess, err := c.NewSession()
		if err != nil {
			return errNoShell
		}
		defer sess.Close()
		// Con el reenvío del agente activo, el push por SSH puede usar las claves de esta PC.
		if forwardAgent && agentAvailable() {
			src.shared.serveAgent()
			_ = agent.RequestAgentForwarding(sess)
		}
		var o limitedBuffer
		o.max = max
		var e bytes.Buffer
		sess.Stdout, sess.Stderr = &o, &e
		if stdin != nil {
			sess.Stdin = bytes.NewReader(stdin)
		}
		full := "cd -- " + shellQuote(dir) + " || exit 3\n" +
			"command -v git >/dev/null 2>&1 || exit 127\n" +
			"export GIT_TERMINAL_PROMPT=0 LC_ALL=C GIT_OPTIONAL_LOCKS=0\n" + script
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- sess.Run("sh -c " + shellQuote(full)) }()
		var runErr error
		select {
		case runErr = <-done:
		case <-ctx.Done():
			return i18n.Err("remote.gitTimeout")
		}
		out, stderr = o.Bytes(), strings.TrimSpace(e.String())
		var ee *ssh.ExitError
		switch {
		case runErr == nil:
		case errors.As(runErr, &ee):
			status = ee.ExitStatus()
		default:
			return errNoShell
		}
		if o.over {
			return i18n.Err("remote.gitFileTooBig")
		}
		return nil
	})
	if err == nil && status == 127 {
		err = errNoGit
	}
	return out, status, stderr, err
}

// limitedBuffer es un bytes.Buffer que deja de guardar después de max bytes (0 = sin límite).
type limitedBuffer struct {
	bytes.Buffer
	max  int64
	over bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.max > 0 && int64(b.Len()+len(p)) > b.max {
		b.over = true
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

var _ io.Writer = (*limitedBuffer)(nil)

// gitErr arma el error de un comando de git que salió con status != 0.
func gitErr(status int, stderr string) error {
	low := strings.ToLower(stderr)
	switch {
	case strings.Contains(low, "please tell me who you are") || strings.Contains(low, "unable to auto-detect email"):
		return newErr(CodeGitIdentity, "git needs user.name and user.email", nil)
	case strings.Contains(low, "dubious ownership"):
		return i18n.Err("remote.gitDubious")
	case status == 3:
		return i18n.Err("remote.gitNoDir")
	}
	if stderr == "" {
		return i18n.Err("remote.gitFailed", status)
	}
	return errors.New(shorten(stderr))
}

const rs = "\x1e" // separa las partes de la salida del script de GitStatus

// GitStatus devuelve el estado del repositorio que contiene dir (IsRepo false si no está en uno).
func (m *Manager) GitStatus(sessionID, dir string) (GitStatus, error) {
	out, status, stderr, err := m.gitRun(sessionID, dir, `top=$(git rev-parse --show-toplevel) || exit 4
printf '%s\036' "$top"
cd -- "$top" || exit 3
git status --porcelain=v2 --branch -z --untracked-files=all || exit 5
printf '\036'
git remote get-url origin 2>/dev/null
printf '\036'
git log -1 --format='%h%x1f%s%x1f%cr' 2>/dev/null
exit 0`, nil, gitTimeout, 0)
	if err != nil {
		return GitStatus{}, err
	}
	if status == 4 && strings.Contains(strings.ToLower(stderr), "not a git repository") {
		return GitStatus{}, nil
	}
	if status != 0 {
		return GitStatus{}, gitErr(status, stderr)
	}
	return parseGitStatus(string(out)), nil
}

func parseGitStatus(out string) GitStatus {
	parts := strings.SplitN(out, rs, 4)
	for len(parts) < 4 {
		parts = append(parts, "")
	}
	st := GitStatus{IsRepo: true, Root: strings.TrimSpace(parts[0]), Files: []GitFile{}}
	entries := strings.Split(parts[1], "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		switch {
		case strings.HasPrefix(e, "# branch.head "):
			if h := strings.TrimPrefix(e, "# branch.head "); h != "(detached)" {
				st.Branch = h
			}
		case strings.HasPrefix(e, "# branch.upstream "):
			st.Upstream = strings.TrimPrefix(e, "# branch.upstream ")
		case strings.HasPrefix(e, "# branch.ab "):
			f := strings.Fields(strings.TrimPrefix(e, "# branch.ab "))
			if len(f) == 2 {
				st.Ahead, _ = strconv.Atoi(strings.TrimPrefix(f[0], "+"))
				st.Behind, _ = strconv.Atoi(strings.TrimPrefix(f[1], "-"))
			}
		case strings.HasPrefix(e, "1 "):
			if f := strings.SplitN(e, " ", 9); len(f) == 9 {
				st.Files = append(st.Files, GitFile{Path: f[8], X: f[1][:1], Y: f[1][1:]})
			}
		case strings.HasPrefix(e, "2 "):
			if f := strings.SplitN(e, " ", 10); len(f) == 10 {
				gf := GitFile{Path: f[9], X: f[1][:1], Y: f[1][1:]}
				if i+1 < len(entries) {
					i++
					gf.OrigPath = entries[i]
				}
				st.Files = append(st.Files, gf)
			}
		case strings.HasPrefix(e, "u "):
			if f := strings.SplitN(e, " ", 11); len(f) == 11 {
				st.Files = append(st.Files, GitFile{Path: f[10], X: "U", Y: "U"})
			}
		case strings.HasPrefix(e, "? "):
			st.Files = append(st.Files, GitFile{Path: e[2:], X: "?", Y: "?"})
		}
	}
	st.Remote = strings.TrimSpace(parts[2])
	st.Host = remoteHost(st.Remote)
	if f := strings.SplitN(strings.TrimSpace(parts[3]), "\x1f", 3); len(f) == 3 {
		st.Head, st.Subject, st.When = f[0], f[1], f[2]
	}
	return st
}

// remoteHost dice dónde está alojado el remoto, por su URL.
func remoteHost(url string) string {
	u := strings.ToLower(url)
	switch {
	case u == "":
		return ""
	case strings.Contains(u, "github"):
		return "github"
	case strings.Contains(u, "gitlab"):
		return "gitlab"
	case strings.Contains(u, "bitbucket"):
		return "bitbucket"
	}
	return "other"
}

// pathArgs arma la lista de rutas para la línea de comandos ("--" y cada una entre comillas).
func pathArgs(paths []string) (string, error) {
	if len(paths) == 0 {
		return "", i18n.Err("remote.gitNoFiles")
	}
	var b strings.Builder
	b.WriteString(" --")
	for _, p := range paths {
		if p == "" || strings.ContainsRune(p, 0) {
			return "", i18n.Err("remote.gitNoFiles")
		}
		b.WriteString(" ")
		b.WriteString(shellQuote(p))
	}
	return b.String(), nil
}

// gitDo corre script en la raíz del repositorio y convierte un status != 0 en error.
func (m *Manager) gitDo(sessionID, root, script string, stdin []byte, timeout time.Duration) error {
	_, status, stderr, err := m.gitRun(sessionID, root, script, stdin, timeout, 0)
	if err != nil {
		return err
	}
	if status != 0 {
		return gitErr(status, stderr)
	}
	return nil
}

// GitStage prepara los archivos para el próximo commit (también los borrados).
func (m *Manager) GitStage(sessionID, root string, paths []string) error {
	args, err := pathArgs(paths)
	if err != nil {
		return err
	}
	return m.gitDo(sessionID, root, "git add -A"+args, nil, gitTimeout)
}

// GitUnstage saca los archivos de los preparados (los cambios siguen en la copia de trabajo).
func (m *Manager) GitUnstage(sessionID, root string, paths []string) error {
	args, err := pathArgs(paths)
	if err != nil {
		return err
	}
	return m.gitDo(sessionID, root, "git reset -q"+args, nil, gitTimeout)
}

// GitDiscard descarta los cambios de la copia de trabajo: los archivos con seguimiento vuelven a
// como están preparados (o en el último commit) y los nuevos sin seguimiento se borran.
func (m *Manager) GitDiscard(sessionID, root string, tracked, untracked []string) error {
	var script []string
	if len(tracked) > 0 {
		args, err := pathArgs(tracked)
		if err != nil {
			return err
		}
		script = append(script, "git checkout -q"+args+" || exit $?")
	}
	if len(untracked) > 0 {
		args, err := pathArgs(untracked)
		if err != nil {
			return err
		}
		script = append(script, "git clean -f -q"+args)
	}
	if len(script) == 0 {
		return i18n.Err("remote.gitNoFiles")
	}
	return m.gitDo(sessionID, root, strings.Join(script, "\n"), nil, gitTimeout)
}

// GitCommit crea un commit con lo preparado. El mensaje va por la entrada estándar.
func (m *Manager) GitCommit(sessionID, root, message string) error {
	if strings.TrimSpace(message) == "" {
		return i18n.Err("remote.gitNoMessage")
	}
	return m.gitDo(sessionID, root, "git commit -q -F -", []byte(message), gitTimeout)
}

// GitSetIdentity guarda nombre y email para los commits de este repositorio (git config local).
func (m *Manager) GitSetIdentity(sessionID, root, name, email string) error {
	name, email = strings.TrimSpace(name), strings.TrimSpace(email)
	if name == "" || email == "" || strings.ContainsAny(name+email, "\n\r\x00") {
		return i18n.Err("remote.gitBadIdentity")
	}
	return m.gitDo(sessionID, root, "git config user.name "+shellQuote(name)+" && git config user.email "+shellQuote(email), nil, gitTimeout)
}

// GitPull trae los cambios del remoto solo si no hay que mezclar (--ff-only): si las ramas se
// separaron, git falla y eso se resuelve en una terminal.
func (m *Manager) GitPull(sessionID, root string) error {
	return m.gitDo(sessionID, root, "git pull --ff-only -q", nil, gitNetTimeout)
}

// GitPush sube los commits. Si la rama todavía no sigue a ninguna del remoto, la publica en origin.
func (m *Manager) GitPush(sessionID, root string) error {
	return m.gitDo(sessionID, root, `if git rev-parse --abbrev-ref --symbolic-full-name '@{u}' >/dev/null 2>&1; then
  git push -q
else
  git push -q -u origin HEAD
fi`, nil, gitNetTimeout)
}

// GitFetch actualiza lo que se sabe del remoto (para los commits por traer).
func (m *Manager) GitFetch(sessionID, root string) error {
	return m.gitDo(sessionID, root, "git fetch -q", nil, gitNetTimeout)
}

// GitBranches son las ramas locales del repositorio.
func (m *Manager) GitBranches(sessionID, root string) ([]string, error) {
	out, status, stderr, err := m.gitRun(sessionID, root, "git for-each-ref --format='%(refname:short)' refs/heads", nil, gitTimeout, 0)
	if err != nil {
		return nil, err
	}
	if status != 0 {
		return nil, gitErr(status, stderr)
	}
	branches := []string{}
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			branches = append(branches, l)
		}
	}
	return branches, nil
}

// GitCheckout cambia a la rama name; create la crea desde donde se está.
func (m *Manager) GitCheckout(sessionID, root, name string, create bool) error {
	name = strings.TrimSpace(name)
	if name == "" || strings.HasPrefix(name, "-") || strings.ContainsAny(name, " \t\n\r\x00") {
		return i18n.Err("remote.gitBadBranch", name)
	}
	flag := ""
	if create {
		flag = " -b"
	}
	return m.gitDo(sessionID, root, "git check-ref-format --branch "+shellQuote(name)+" >/dev/null || exit 6\ngit checkout -q"+flag+" "+shellQuote(name), nil, gitTimeout)
}

// GitFileAt devuelve el contenido de un archivo en el último commit (ref "HEAD") o como está
// preparado (ref ""), para el diff. exists es false si el archivo no está ahí (nuevo o borrado).
func (m *Manager) GitFileAt(sessionID, root, ref, path string) (text string, exists bool, err error) {
	if ref != "" && ref != "HEAD" {
		return "", false, i18n.Err("remote.gitNoFiles")
	}
	if path == "" || strings.ContainsRune(path, 0) {
		return "", false, i18n.Err("remote.gitNoFiles")
	}
	spec := shellQuote(ref + ":" + path)
	out, status, stderr, err := m.gitRun(sessionID, root, "git cat-file -e "+spec+" 2>/dev/null || exit 10\ngit cat-file -p "+spec, nil, gitTimeout, gitMaxFileSize)
	if err != nil {
		return "", false, err
	}
	if status == 10 {
		return "", false, nil
	}
	if status != 0 {
		return "", false, gitErr(status, stderr)
	}
	if bytes.IndexByte(out, 0) >= 0 {
		return "", true, i18n.Err("remote.gitBinary")
	}
	return string(out), true, nil
}

// storeConn es la conexión guardada de una sesión (sin store, en las pruebas, no hay ninguna).
func (m *Manager) storeConn(id string) (store.Connection, bool) {
	if m.store == nil {
		return store.Connection{}, false
	}
	return m.store.Get(id)
}

package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"osat/internal/i18n"
)

// CopyName es el nombre con el que se pega una copia de base en una carpeta
// donde exists dice qué nombres ya están ocupados. Si base está libre se deja
// igual (pegar en otra carpeta); si no, sale "x - copia.ext", "x - copia
// (2).ext", etc. Nunca devuelve un nombre ocupado: pegar no pisa nada.
func CopyName(base string, isDir bool, exists func(name string) bool) string {
	if !exists(base) {
		return base
	}
	stem, ext := base, ""
	if !isDir {
		// ".bashrc" no tiene extensión: es todo nombre.
		if e := path.Ext(base); e != "" && e != base {
			stem, ext = strings.TrimSuffix(base, e), e
		}
	}
	name := stem + " - copia" + ext
	for n := 2; exists(name); n++ {
		name = fmt.Sprintf("%s - copia (%d)%s", stem, n, ext)
	}
	return name
}

// Copy copia archivos y carpetas (con todo su contenido) a dir, en el mismo
// servidor. Nunca pisa nada (ver CopyName) y devuelve las rutas creadas.
//
// Si el servidor deja ejecutar comandos, copia con `cp -a` en el propio
// servidor: un solo viaje sin importar el tamaño, y sin que los bytes pasen por
// esta PC (con una latencia de 100+ ms, la diferencia es de segundos a minutos).
// Si no (cuenta solo SFTP, sin cp), cae a copiar por SFTP.
func (m *Manager) Copy(id string, paths []string, dir string) ([]string, error) {
	s, err := m.get(id)
	if err != nil {
		return nil, err
	}
	if s.sftp == nil {
		return nil, i18n.Err("remote.theSessionIsNotSftp")
	}
	var sh *shell
	if s.shared != nil {
		sh = &shell{c: s.shared.client, off: &s.noShell}
	}
	return copyFiles(context.Background(), s.sftp, sh, paths, dir)
}

// shell ejecuta `cp` en el servidor sobre la conexión SSH que ya hay. off
// recuerda (por sesión) que el servidor no lo permite, para no reintentarlo.
type shell struct {
	c   *ssh.Client
	off *atomic.Bool
}

var errNoShell = i18n.Err("remote.theServerDoesnTAllow2")

func (s *shell) usable() bool { return s != nil && s.c != nil && !s.off.Load() }

func shellQuote(p string) string { return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'" }

// cp copia src en dst (que no existe) con `cp -a`: recursivo y conserva
// permisos, fechas y enlaces. errNoShell = usar SFTP; cualquier otro error es de cp
// (sin permiso, disco lleno…) y se informa tal cual.
func (s *shell) cp(ctx context.Context, src, dst string) error {
	status, msg, err := s.run(ctx, "cp -a -- "+shellQuote(src)+" "+shellQuote(dst))
	switch {
	case err != nil:
		return err
	case status == 0:
		return nil
	case status == 126 || status == 127:
		s.off.Store(true) // el shell no encontró o no pudo ejecutar cp
		return errNoShell
	}
	if msg == "" {
		msg = fmt.Sprintf("Process exited with status %d", status)
	}
	return errors.New(msg)
}

// run ejecuta cmd en el servidor y devuelve su código de salida y lo que escribió en stderr. err solo
// es un error de ejecutar: errNoShell si el servidor no deja (o cerró el canal sin código de salida,
// p. ej. con ForceCommand) o el del contexto.
func (s *shell) run(ctx context.Context, cmd string) (status int, stderr string, err error) {
	_, status, stderr, err = s.runOut(ctx, cmd)
	return status, stderr, err
}

// runOut es run y además devuelve lo que el comando escribió en stdout (para comandos con salida corta).
func (s *shell) runOut(ctx context.Context, cmd string) (stdout string, status int, stderr string, err error) {
	sess, err := s.c.NewSession()
	if err != nil {
		s.off.Store(true) // el servidor rechazó abrir un canal de comandos
		return "", 0, "", errNoShell
	}
	defer sess.Close()
	var out, errBuf bytes.Buffer
	sess.Stdout, sess.Stderr = &out, &errBuf
	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()
	select {
	case err = <-done:
	case <-ctx.Done():
		return "", 0, "", ctx.Err()
	}
	var ee *ssh.ExitError
	switch {
	case err == nil:
		return out.String(), 0, "", nil
	case errors.As(err, &ee):
		return out.String(), ee.ExitStatus(), strings.TrimSpace(errBuf.String()), nil
	default:
		s.off.Store(true)
		return "", 0, "", errNoShell
	}
}

// guardedWriter deja de pasar bytes a w una vez cerrado: después de cancelar un comando, nada
// del canal puede escribir en el destino del que llamó.
type guardedWriter struct {
	mu     sync.Mutex
	w      io.Writer
	closed bool
}

func (g *guardedWriter) Write(p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return 0, io.ErrClosedPipe
	}
	return g.w.Write(p)
}

func (g *guardedWriter) close() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
}

// stream es runOut con stdin y con la salida en flujo: stdin (nil = vacío) se manda al comando y
// su stdout se escribe en stdout a medida que llega, sin juntarlo. Devuelve el código de salida y
// stderr, y err con la misma semántica que run (errNoShell si el servidor no deja ejecutar
// comandos), salvo que si escribir en stdout falla se devuelve ese error y el shell sigue usable.
//
// Al cancelar ctx manda SIGKILL (si el servidor lo acepta; sshd no contesta ni avisa si lo ignora),
// cierra la sesión y devuelve ctx.Err() sin esperar al comando; desde que vuelve, ya no se
// escribe nada en stdout.
func (s *shell) stream(ctx context.Context, cmd string, stdin io.Reader, stdout io.Writer) (status int, stderr string, err error) {
	sess, err := s.c.NewSession()
	if err != nil {
		s.off.Store(true) // el servidor rechazó abrir un canal de comandos
		return 0, "", errNoShell
	}
	defer sess.Close()
	var errBuf bytes.Buffer
	sess.Stderr = &errBuf
	if stdin != nil {
		sess.Stdin = stdin
	}
	if stdout != nil {
		g := &guardedWriter{w: stdout}
		defer g.close()
		sess.Stdout = g
	}
	if err := sess.Start(cmd); err != nil {
		s.off.Store(true)
		return 0, "", errNoShell
	}
	done := make(chan error, 1)
	go func() { done <- sess.Wait() }()
	select {
	case err = <-done:
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		return 0, "", ctx.Err()
	}
	var ee *ssh.ExitError
	var missing *ssh.ExitMissingError
	switch {
	case err == nil:
		return 0, "", nil
	case errors.As(err, &ee):
		return ee.ExitStatus(), strings.TrimSpace(errBuf.String()), nil
	case errors.As(err, &missing):
		s.off.Store(true) // cerró el canal sin código de salida (p. ej. ForceCommand)
		return 0, "", errNoShell
	default:
		return 0, "", err // falló la copia de stdin o stdout, no el comando
	}
}

func copyFiles(ctx context.Context, cl *sftp.Client, sh *shell, paths []string, dir string) ([]string, error) {
	dir = path.Clean(dir)
	var out []string
	for _, p := range paths {
		p = path.Clean(p)
		fi, err := cl.Lstat(p)
		if err != nil {
			return out, err
		}
		if fi.IsDir() && (p == dir || strings.HasPrefix(dir, strings.TrimSuffix(p, "/")+"/")) {
			return out, i18n.Err("remote.canTCopyQInto", path.Base(p))
		}
		name := CopyName(path.Base(p), fi.IsDir(), func(n string) bool {
			_, err := cl.Lstat(path.Join(dir, n))
			return err == nil
		})
		dst := path.Join(dir, name)
		if sh.usable() {
			err := sh.cp(ctx, p, dst)
			switch {
			case err == nil:
				if _, serr := cl.Lstat(dst); serr == nil {
					out = append(out, dst)
					continue
				}
				sh.off.Store(true) // el shell y SFTP no ven las mismas rutas: seguimos por SFTP
			case errors.Is(err, errNoShell):
				// seguimos por SFTP
			default:
				return out, err
			}
		}
		if err := copyEntry(ctx, cl, p, fi, dst); err != nil {
			return out, err
		}
		out = append(out, dst)
	}
	return out, nil
}

// Hasta este tamaño el archivo se lee entero en memoria con lecturas en
// paralelo (ReadAt); un archivo más grande se copia en streaming, más lento
// con latencia porque lee de a un pedido por vez.
const copyInMemory = 32 << 20

func copyEntry(ctx context.Context, cl *sftp.Client, src string, fi os.FileInfo, dst string) error {
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		target, err := cl.ReadLink(src)
		if err != nil {
			return err
		}
		return cl.Symlink(target, dst)
	case fi.IsDir():
		if err := cl.Mkdir(dst); err != nil {
			return err
		}
		_ = cl.Chmod(dst, fi.Mode().Perm())
		children, err := cl.ReadDir(src)
		if err != nil {
			return err
		}
		for _, c := range children {
			if err := copyEntry(ctx, cl, path.Join(src, c.Name()), c, path.Join(dst, c.Name())); err != nil {
				return err
			}
		}
		return nil
	case fi.Mode().IsRegular():
		f, err := cl.Open(src)
		if err != nil {
			return err
		}
		defer f.Close()
		if fi.Size() <= copyInMemory {
			buf := make([]byte, fi.Size())
			n, err := f.ReadAt(buf, 0)
			if err != nil && !errors.Is(err, io.EOF) {
				return err
			}
			return writeRemote(ctx, cl, dst, bytes.NewReader(buf[:n]), int64(n), fi.Mode().Perm(), nil)
		}
		return writeRemote(ctx, cl, dst, f, fi.Size(), fi.Mode().Perm(), nil)
	default:
		return i18n.Err("remote.canTCopyQIt", path.Base(src))
	}
}

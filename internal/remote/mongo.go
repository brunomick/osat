package remote

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"

	"osat/internal/applog"
	"osat/internal/i18n"
	"osat/internal/localterm"
	"osat/internal/mongouri"
	"osat/internal/portfwd"
	"osat/internal/store"
	"osat/internal/vt"
)

// Conexiones MongoDB: se abren con el mongosh instalado en esta PC, en una
// terminal como las de "Este equipo" (y se comparten con Claude Code igual).
//
// La contraseña no va en la línea de comandos (la vería cualquier programa de
// la PC): mongosh la pide al arrancar y osat la escribe en ese momento; no se
// muestra en pantalla. Si la conexión tiene un salto, osat abre un proxy SOCKS5
// en 127.0.0.1 que sale por esa conexión SSH, y mongosh lo usa (opciones
// proxyHost/proxyPort): así los nombres de los servidores se mantienen y andan
// TLS, los replica sets y mongodb+srv:// (el registro SRV se busca en esta PC).

// mongoPasswordWait es cuánto se espera a que mongosh pida la contraseña.
const mongoPasswordWait = 30 * time.Second

// mongoEarlyExit: si mongosh termina antes de esto, casi seguro no pudo
// conectar; la pestaña muestra el error en vez de cerrarse.
const mongoEarlyExit = 15 * time.Second

// FindMongosh busca mongosh en el PATH y donde lo dejan sus instaladores (las
// apps de macOS no heredan el PATH de la terminal). Variable para los tests.
var FindMongosh = func() (string, error) {
	if p, err := exec.LookPath("mongosh"); err == nil {
		return p, nil
	}
	for _, p := range mongoshCandidates() {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, nil
		}
	}
	return "", i18n.Err("mongo.noMongosh")
}

func mongoshCandidates() []string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		var out []string
		if d := os.Getenv("LOCALAPPDATA"); d != "" { // instalación solo para el usuario (MSI)
			out = append(out, filepath.Join(d, "Programs", "mongosh", "mongosh.exe"))
		}
		if d := os.Getenv("ProgramFiles"); d != "" {
			out = append(out, filepath.Join(d, "mongosh", "mongosh.exe"))
		}
		return out
	case "darwin":
		return []string{"/opt/homebrew/bin/mongosh", "/usr/local/bin/mongosh", filepath.Join(home, ".local", "bin", "mongosh")}
	default:
		return []string{"/usr/bin/mongosh", "/usr/local/bin/mongosh", "/snap/bin/mongosh", filepath.Join(home, ".local", "bin", "mongosh")}
	}
}

// OpenMongoShell abre mongosh conectado a la conexión MongoDB de req, en una
// terminal de este equipo.
func (m *Manager) OpenMongoShell(req ConnectRequest) (SessionInfo, error) {
	c, ok := m.store.Get(req.ConnectionID)
	if !ok {
		return SessionInfo{}, newErr(CodeConnGone, "conexión inexistente", map[string]string{"connId": req.ConnectionID})
	}
	if c.Protocol != store.ProtoMongo {
		return SessionInfo{}, i18n.Err("mongo.notMongo")
	}
	bin, err := FindMongosh()
	if err != nil {
		return SessionInfo{}, err
	}
	u, err := mongouri.Parse(c.URI)
	if err != nil {
		return SessionInfo{}, err
	}
	u.Password = "" // la contraseña la escribe osat cuando mongosh la pide
	secret := req.Secret
	if secret == "" {
		secret = m.store.SecretFor(c)
	}

	var cleanup []func()
	undo := func() {
		for _, f := range cleanup {
			f()
		}
	}
	if c.JumpID != "" {
		prompt := m.askUser
		if req.NoPrompt {
			prompt = noPrompt
		}
		client, err := m.dialJump(c, req.JumpSecrets, prompt, 0)
		if err != nil {
			return SessionInfo{}, err
		}
		cleanup = append(cleanup, func() { client.Close() })
		ln, err := portfwd.ListenSOCKS(0, sshDialAddr(client))
		if err != nil {
			undo()
			return SessionInfo{}, err
		}
		cleanup = append(cleanup, ln.Close)
		u = u.WithOption("proxyHost", "127.0.0.1").WithOption("proxyPort", strconv.Itoa(ln.Port()))
	}
	// Los scripts de varias líneas que manda Claude Code se escriben acá y se
	// cargan con load() (ver mongoScript).
	scripts, err := os.MkdirTemp("", "osat-mongosh-")
	if err != nil {
		undo()
		return SessionInfo{}, err
	}
	cleanup = append(cleanup, func() { os.RemoveAll(scripts) })

	cols, rows := req.Cols, req.Rows
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 32
	}
	p, err := localterm.StartProgram("mongosh", bin, []string{u.Redacted()}, cols, rows)
	if err != nil {
		undo()
		slog.Warn("mongosh: no se pudo abrir", "conn", c.Name, "path", bin, "err", err)
		return SessionInfo{}, err
	}
	started := time.Now()
	s := &Session{ID: uuid.NewString(), Kind: KindTerminal, ConnID: c.ID, name: c.Name, local: p, stdin: p,
		ready: make(chan struct{}), screen: vt.New(cols, rows), title: c.Name, host: c.Host,
		mongo: true, cleanup: cleanup, scripts: scripts}
	m.register(s)
	slog.Info("mongosh: abierto", "conn", c.Name, "host", c.Host, "via_ssh", c.JumpID != "", "id", s.ID)

	dataEvt := "term:data:" + s.ID
	read := make(chan struct{})
	logf := openSessionLog(currentOptions().LogDir, c.Name) // nil si el ajuste está apagado
	go func() {
		defer close(read)
		defer logf.Close()
		defer applog.Recover("mongosh: lectura")
		select {
		case <-s.ready:
		case <-time.After(5 * time.Second):
		}
		answer := newPasswordAnswer(secret, started)
		secret = ""
		buf := make([]byte, 32*1024)
		for {
			n, err := p.Read(buf)
			if n > 0 {
				s.screen.Write(buf[:n])
				logf.Write(buf[:n])
				m.emit(dataEvt, base64.StdEncoding.EncodeToString(buf[:n]))
				answer.check(s.screen, p)
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		defer applog.Recover("mongosh: fin del proceso")
		p.Wait()
		time.Sleep(150 * time.Millisecond)
		p.Close()
		<-read
		msg := ""
		if time.Since(started) < mongoEarlyExit {
			msg = i18n.T("mongo.ended")
		}
		m.emit("term:exit:"+s.ID, msg)
		m.Close(s.ID)
	}()
	return SessionInfo{ID: s.ID, Kind: KindTerminal, ConnID: c.ID, Title: c.Name}, nil
}

// sshDialAddr abre los destinos del proxy SOCKS desde el servidor SSH.
func sshDialAddr(client *ssh.Client) portfwd.DialAddr {
	return func(ctx context.Context, addr string) (net.Conn, error) {
		conn, err := client.DialContext(ctx, "tcp", addr)
		if err != nil {
			var oe *ssh.OpenChannelError
			if errors.As(err, &oe) { // el servidor no pudo llegar al destino
				return nil, portfwd.Refused(err)
			}
			return nil, err
		}
		return conn, nil
	}
}

// passwordAnswer escribe la contraseña la primera vez que mongosh la pide
// ("Enter password: "), solo al principio y una sola vez: lo que imprima
// después (la base, un script) no la puede volver a sacar.
type passwordAnswer struct {
	secret   string
	deadline time.Time
}

func newPasswordAnswer(secret string, started time.Time) *passwordAnswer {
	return &passwordAnswer{secret: secret, deadline: started.Add(mongoPasswordWait)}
}

func (a *passwordAnswer) check(screen *vt.Screen, w io.Writer) {
	if a.secret == "" {
		return
	}
	if time.Now().After(a.deadline) {
		a.secret = ""
		return
	}
	if _, before := screen.CursorLine(); strings.HasSuffix(strings.TrimRight(before, " "), "Enter password:") {
		_, _ = io.WriteString(w, a.secret+"\r")
		a.secret = ""
	}
}

// mongoScript prepara un comando para mongosh: uno de una línea va tal cual;
// uno de varias (un script) se guarda en un archivo y se carga con load(), ya
// que tipeado línea por línea mongosh ejecutaría cada una por separado.
// remove borra el archivo.
func mongoScript(s *Session, command string) (line string, remove func(), err error) {
	if !strings.ContainsAny(command, "\r\n") {
		return command, func() {}, nil
	}
	f, err := os.CreateTemp(s.scripts, "script-*.js")
	if err != nil {
		return "", nil, err
	}
	_, err = f.WriteString(command)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", nil, err
	}
	quoted, _ := json.Marshal(f.Name()) // una cadena de JSON es una cadena de JavaScript válida
	return "load(" + string(quoted) + ")", func() { os.Remove(f.Name()) }, nil
}

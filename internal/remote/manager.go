package remote

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"osat/internal/applog"
	"osat/internal/i18n"
	"osat/internal/localterm"
	"osat/internal/store"
	"osat/internal/textenc"
	"osat/internal/vt"
	"osat/internal/web"
)

// Emitter envía eventos al frontend (runtime.EventsEmit en Wails).
type Emitter func(event string, data ...interface{})

type Kind string

const (
	KindTerminal Kind = "terminal"
	KindSFTP     Kind = "sftp"
)

// sharedClient permite que una pestaña SFTP reutilice la conexión SSH de una
// terminal ya abierta (sin volver a autenticarse).
type sharedClient struct {
	mu     sync.Mutex
	client *ssh.Client
	refs   int

	lostOnce  sync.Once
	dead      chan struct{}
	watchOnce sync.Once // el aviso de session:lost se arma una sola vez (ver watchLost)

	agentOnce sync.Once // el manejador del reenvío del agente se registra una sola vez
	sudoPass  string    // contraseña de sudo que funcionó, solo en memoria (ver sudo.go); protegida por mu
}

// lost se cierra cuando la conexión SSH se corta (o se cierra).
func (s *sharedClient) lost() <-chan struct{} {
	s.lostOnce.Do(func() {
		s.dead = make(chan struct{})
		go func() {
			_ = s.client.Wait()
			s.mu.Lock()
			s.sudoPass = "" // la contraseña de sudo no sobrevive a la conexión
			s.mu.Unlock()
			close(s.dead)
		}()
	})
	return s.dead
}

var errConnClosed = i18n.Err("remote.theConnectionIsAlreadyClosed")

// newSharedClient arranca con una referencia (la de quien lo crea).
func newSharedClient(c *ssh.Client) *sharedClient { return &sharedClient{client: c, refs: 1} }

// acquire suma una referencia; falla si la conexión ya se liberó, para no
// revivir un cliente cerrado (p. ej. la terminal origen se cerró justo antes).
func (s *sharedClient) acquire() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refs <= 0 {
		return errConnClosed
	}
	s.refs++
	return nil
}

// serveAgent atiende los canales del agente que abre el servidor (reenvío del
// agente). Cada canal conecta al agente de esta PC en ese momento: en Windows
// no hay una conexión fija que mantener, y así sirve aunque el agente se haya
// reiniciado o abierto después de la primera terminal.
func (s *sharedClient) serveAgent() {
	s.agentOnce.Do(func() {
		chans := s.client.HandleChannelOpen("auth-agent@openssh.com")
		if chans == nil {
			return
		}
		go func() {
			for nch := range chans {
				ag, conn, err := newAgent()
				if err != nil {
					nch.Reject(ssh.ConnectionFailed, "no hay un agente SSH")
					continue
				}
				ch, reqs, err := nch.Accept()
				if err != nil {
					if conn != nil {
						conn.Close()
					}
					continue
				}
				go ssh.DiscardRequests(reqs)
				go func() {
					_ = agent.ServeAgent(ag, ch)
					ch.Close()
					if conn != nil {
						conn.Close()
					}
				}()
			}
		}()
	})
}

func (s *sharedClient) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refs <= 0 {
		return
	}
	s.refs--
	if s.refs == 0 {
		s.sudoPass = ""
		s.client.Close()
	}
}

type Session struct {
	ID     string
	Kind   Kind
	ConnID string
	name   string // nombre de la conexión, para el log

	shared *sharedClient // nil en las terminales de este equipo (ver localterm.go)
	ssh    *ssh.Session
	local  localterm.Proc
	stdin  io.Writer
	sftp   *sftp.Client

	// Solo en terminales: lo que se ve (para leerla desde el puente con Claude
	// Code, ver bridge.go) y si el usuario la compartió con él.
	screen     *vt.Screen
	title      string
	host       string
	sharedWith atomic.Bool

	noShell atomic.Bool // el servidor no deja ejecutar comandos (ver Copy)

	capsMu sync.Mutex
	caps   *findCaps // qué hay en el servidor para buscar archivos, una vez por sesión (ver search.go)

	ready     chan struct{} // se cierra cuando el frontend ya escucha eventos
	readyOnce sync.Once
	closeOnce sync.Once
}

// SessionInfo es lo que recibe el frontend al abrir una pestaña.
type SessionInfo struct {
	ID     string `json:"id"`
	Kind   Kind   `json:"kind"`
	ConnID string `json:"connId"`
	Title  string `json:"title"`
	Home   string `json:"home,omitempty"`
}

// ConnectRequest: Secret es opcional (si se pidió al usuario en el momento).
type ConnectRequest struct {
	ConnectionID string `json:"connectionId"`
	Secret       string `json:"secret"`
	SaveSecret   bool   `json:"saveSecret"`
	Cols         int    `json:"cols"`
	Rows         int    `json:"rows"`
	// Contraseñas de los saltos pedidas al conectar, por id de conexión.
	JumpSecrets map[string]JumpSecret `json:"jumpSecrets,omitempty"`
	// NoPrompt: la reconexión automática no le pregunta nada al usuario; si el
	// servidor pide datos (p. ej. un OTP), se corta con NEED_SECRET.
	NoPrompt bool `json:"noPrompt,omitempty"`
	// Reconnect: la terminal reconecta sobre lo que ya había en pantalla, así
	// que el arranque limpio no la borra (ver cleanStart.keepScreen).
	Reconnect bool `json:"reconnect,omitempty"`
}

// JumpSecret es la contraseña (o passphrase) de un salto y si hay que guardarla.
type JumpSecret struct {
	Secret string `json:"secret"`
	Save   bool   `json:"save"`
}

type Manager struct {
	mu        sync.Mutex
	sessions  map[string]*Session
	store     *store.Store
	hostKeys  *HostKeys
	emit      Emitter
	transfers *transfers
	prompts   map[string]chan []string // preguntas keyboard-interactive en curso
	web       *web.Hub                 // sitios del navegador integrado (ver web.go)
	fwMu      sync.Mutex
	fwds      map[string]*fwd // reenvíos de puertos en marcha, por id de reenvío (ver forwards.go)

	encMu    sync.Mutex
	textEncs map[string]textenc.Encoding // archivos leídos para editar que no son UTF-8 (ver files.go)

	searchMu sync.Mutex
	searches map[string]*search // búsquedas de archivos en curso, por id (ver search.go)

	syncMu    sync.Mutex
	syncPlans map[string]*syncPlan // última comparación de cada sesión, para sincronizar (ver sync.go)
}

func NewManager(st *store.Store, hk *HostKeys, emit Emitter) *Manager {
	m := &Manager{sessions: map[string]*Session{}, store: st, hostKeys: hk, emit: emit, prompts: map[string]chan []string{}, web: web.NewHub(), fwds: map[string]*fwd{}, searches: map[string]*search{}, syncPlans: map[string]*syncPlan{}}
	m.transfers = newTransfers(m)
	return m
}

func (m *Manager) get(id string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, i18n.Err("remote.theSessionDoesnTExist")
	}
	return s, nil
}

func (m *Manager) dial(req ConnectRequest) (*ssh.Client, store.Connection, error) {
	c, ok := m.store.Get(req.ConnectionID)
	if !ok {
		return nil, c, newErr(CodeConnGone, "conexión inexistente", map[string]string{"connId": req.ConnectionID})
	}
	prompt := m.askUser
	if req.NoPrompt {
		prompt = noPrompt
	}
	client, err := m.dialChain(c, req.Secret, req.SaveSecret, req.JumpSecrets, prompt, 0)
	return client, c, err
}

// dialChain conecta a c, primero a través de su salto (y del salto de ese…).
// Los errores de un salto llevan su id y nombre (connId, name) para que el
// frontend pida su contraseña o muestre de cuál se trata.
func (m *Manager) dialChain(c store.Connection, given string, save bool, jumps map[string]JumpSecret, prompt Prompter, depth int) (*ssh.Client, error) {
	c = m.store.WithDefaults(c)
	attrs := m.connAttrs(c)
	slog.Debug("conectando", append(attrs, "auth", c.AuthType)...)
	var via *ssh.Client
	if c.JumpID != "" {
		v, err := m.dialJump(c, jumps, prompt, depth)
		if err != nil {
			LogError("no se pudo llegar por el salto", err, attrs...)
			return nil, err
		}
		via = v
	}
	secret := given
	if secret == "" {
		secret = m.store.SecretFor(c)
	}
	client, err := Dial(c, secret, m.hostKeys, prompt, via)
	if err != nil {
		if via != nil {
			via.Close()
		}
		if depth > 0 {
			err = jumpError(c, err)
		}
		LogError("no se pudo conectar", err, attrs...)
		return nil, err
	}
	slog.Info("conexión abierta", attrs...)
	if via != nil { // el salto vive lo mismo que la conexión que pasa por él
		go func() {
			client.Wait()
			via.Close()
		}()
	}
	if given != "" && save {
		if err := m.store.SetSecret(c.ID, given); err != nil {
			slog.Warn("no se pudo guardar la contraseña", append(attrs, "err", err)...)
		}
	}
	return client, nil
}

// connAttrs son los datos de una conexión para el log: nombre, usuario@host:puerto
// y, si tiene, los saltos por los que se llega (del primero al último).
func (m *Manager) connAttrs(c store.Connection) []any {
	attrs := []any{"conn", c.Name, "addr", fmt.Sprintf("%s@%s:%d", c.User, c.Host, c.Port)}
	var hops []string
	for id, n := c.JumpID, 0; id != "" && n < store.MaxJumps; n++ {
		j, ok := m.store.Get(id)
		if !ok {
			break
		}
		hops = append([]string{j.Name}, hops...)
		id = j.JumpID
	}
	if len(hops) > 0 {
		attrs = append(attrs, "via", strings.Join(hops, " > "))
	}
	return attrs
}

// sessionConn es el nombre de la conexión de la sesión con ese id ("" si ya no existe).
func (m *Manager) sessionConn(id string) string {
	s, err := m.get(id)
	if err != nil {
		return ""
	}
	return s.name
}

// DialJump conecta al salto de c (c.JumpID, a través de sus propios saltos)
// para llegar a c por otro protocolo, como RDP. Quien llama cierra el cliente.
func (m *Manager) DialJump(c store.Connection, jumps map[string]JumpSecret) (*ssh.Client, error) {
	return m.dialJump(c, jumps, m.askUser, 0)
}

func (m *Manager) dialJump(c store.Connection, jumps map[string]JumpSecret, prompt Prompter, depth int) (*ssh.Client, error) {
	j, ok := m.store.Get(c.JumpID)
	if !ok {
		// name es la conexión que apunta al salto borrado; jumpId, el salto.
		return nil, newErr(CodeConnGone, fmt.Sprintf("la conexión de salto de «%s» ya no existe", c.Name),
			map[string]string{"kind": "jump", "name": c.Name, "jumpId": c.JumpID})
	}
	if depth >= store.MaxJumps {
		return nil, i18n.Err("remote.tooManyChainedJumpHosts")
	}
	js := jumps[j.ID]
	return m.dialChain(j, js.Secret, js.Save, jumps, prompt, depth+1)
}

// jumpError marca un error de conexión al salto j.
func jumpError(j store.Connection, err error) error {
	var ke *Error
	if !errors.As(err, &ke) {
		return i18n.Err("remote.jumpHostSV", j.Name, err)
	}
	data := map[string]string{"connId": j.ID, "name": j.Name, "jump": j.Name} // jump: el frontend antepone «Salto …:» al texto
	for k, v := range ke.Data {
		data[k] = v
	}
	return newErr(ke.Code, fmt.Sprintf("Salto «%s»: %s", j.Name, ke.Message), data)
}

func (m *Manager) register(s *Session) {
	m.mu.Lock()
	m.sessions[s.ID] = s
	m.mu.Unlock()
	slog.Debug("sesión abierta", "id", s.ID, "kind", s.Kind, "conn_id", s.ConnID)
	if s.shared != nil {
		s.shared.watchOnce.Do(func() { go m.watchLost(s.shared) })
	}
}

// watchLost emite "session:lost:<id>" para cada sesión que seguía abierta
// sobre sh cuando se cortó la conexión, para que la pestaña reconecte. Las que
// cerró el usuario ya no están en m.sessions, y si las cerró todas la conexión
// se cierra sin nadie a quien avisar.
func (m *Manager) watchLost(sh *sharedClient) {
	defer applog.Recover("aviso de conexión perdida")
	<-sh.lost()
	m.mu.Lock()
	var ids []string
	for id, s := range m.sessions {
		if s.shared == sh {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.emit("session:lost:" + id)
	}
}

// OpenTerminal abre una shell interactiva con PTY.
func (m *Manager) OpenTerminal(req ConnectRequest) (SessionInfo, error) {
	client, c, err := m.dial(req)
	if err != nil {
		return SessionInfo{}, err
	}
	sh := newSharedClient(client)
	s, err := m.startShell(sh, c, req.Cols, req.Rows, req.Reconnect)
	if err != nil {
		sh.release()
		return SessionInfo{}, err
	}
	return SessionInfo{ID: s.ID, Kind: KindTerminal, ConnID: c.ID, Title: c.Name}, nil
}

// reconnect: la terminal ya tiene algo en pantalla y no se borra (ver ConnectRequest.Reconnect).
func (m *Manager) startShell(sh *sharedClient, c store.Connection, cols, rows int, reconnect bool) (*Session, error) {
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 32
	}
	sess, err := sh.client.NewSession()
	if err != nil {
		return nil, err
	}
	// Reenvío del agente: solo en el servidor final (los saltos no abren shells).
	var notices []byte
	if c.ForwardAgent {
		if !agentAvailable() {
			notices = append(notices, grayNotice(i18n.T("remote.agentNone"))...)
		} else {
			sh.serveAgent()
			if err := agent.RequestAgentForwarding(sess); err != nil {
				notices = append(notices, grayNotice(i18n.T("remote.agentDenied"))...)
			}
		}
	}
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 115200,
		ssh.TTY_OP_OSPEED: 115200,
	}
	o := currentOptions()
	if err := sess.RequestPty(o.TermType, rows, cols, modes); err != nil {
		sess.Close()
		return nil, i18n.Err("remote.couldnTRequestAPty", err)
	}
	// Variables de entorno: sshd solo acepta las de AcceptEnv. Las que rechaza
	// se avisan en la terminal y se sigue.
	for _, e := range c.Env {
		name, value, _ := strings.Cut(e, "=")
		if err := sess.Setenv(name, value); err != nil {
			notices = append(notices, grayNotice(i18n.T("remote.envRejected", name))...)
		}
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		sess.Close()
		return nil, err
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		sess.Close()
		return nil, err
	}
	stderr, err := sess.StderrPipe()
	if err != nil {
		sess.Close()
		return nil, err
	}
	if err := sess.Shell(); err != nil {
		sess.Close()
		return nil, err
	}

	s := &Session{ID: uuid.NewString(), Kind: KindTerminal, ConnID: c.ID, name: c.Name, shared: sh, ssh: sess, stdin: stdin,
		ready: make(chan struct{}), screen: vt.New(cols, rows), title: c.Name,
		host: fmt.Sprintf("%s@%s:%d", c.User, c.Host, c.Port)}
	m.register(s)

	dataEvt := "term:data:" + s.ID
	logf := openSessionLog(o.LogDir, c.Name) // nil si el ajuste está apagado
	send := func(b []byte) {
		s.screen.Write(b)
		logf.Write(b)
		// base64: los bytes pueden cortar secuencias UTF-8 a la mitad.
		m.emit(dataEvt, base64.StdEncoding.EncodeToString(b))
	}
	// El comando inicial se escribe cuando termina el arranque limpio, para que
	// no se mezcle con él; los avisos van entre el borrado y el prompt.
	writeCommand := func() {
		if c.InitialCommand != "" {
			_, _ = io.WriteString(stdin, c.InitialCommand+"\r")
		}
	}
	// Arranque limpio: sin el MOTD ni el "Last login" del servidor y con el
	// mismo prompt en todos (ver cleanstart.go). Si no se puede, sale todo tal cual.
	var clean *cleanStart
	if cleanStartEnabled(string(sh.client.ServerVersion())) {
		if _, err := io.WriteString(stdin, cleanStartInput); err == nil {
			clean = newCleanStart(send)
			clean.keepScreen = reconnect
			clean.onDone = func() []byte {
				writeCommand()
				return notices
			}
		}
	}
	if clean == nil {
		writeCommand()
	}
	var wg sync.WaitGroup
	// first se muestra apenas el frontend escucha, antes que la salida.
	pump := func(r io.Reader, out func([]byte), clean *cleanStart, first []byte) {
		defer wg.Done()
		defer applog.Recover("startShell: lectura de la terminal")
		if clean != nil {
			defer clean.giveUp() // si la sesión termina antes de la marca (p. ej. el servidor cierra), se muestra lo que dijo
		}
		// Esperamos a que el frontend se suscriba para no perder el comienzo de la salida.
		select {
		case <-s.ready:
		case <-time.After(5 * time.Second):
		}
		if len(first) > 0 {
			out(first)
		}
		if clean != nil {
			clean.start()
		}
		buf := make([]byte, 32*1024)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				out(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}
	wg.Add(2)
	if clean != nil {
		go pump(stdout, clean.write, clean, nil)
	} else {
		go pump(stdout, send, nil, notices) // sin arranque limpio, los avisos van primero
	}
	go pump(stderr, send, nil, nil)
	go func() {
		defer applog.Recover("startShell: fin de la shell")
		err := sess.Wait()
		wg.Wait()
		logf.Close()
		msg := ""
		if err != nil {
			var ee *ssh.ExitError
			if !errors.As(err, &ee) {
				msg = err.Error()
				slog.Warn("la shell terminó con error", "conn", c.Name, "id", s.ID, "err", msg)
			}
		}
		m.emit("term:exit:"+s.ID, msg)
		m.Close(s.ID)
	}()
	return s, nil
}

// grayNotice arma un aviso en gris para mostrar en la terminal.
func grayNotice(msg string) []byte {
	return []byte("\x1b[90m" + msg + "\x1b[0m\r\n")
}

// OpenSFTP abre un explorador SFTP con una conexión nueva.
func (m *Manager) OpenSFTP(req ConnectRequest) (SessionInfo, error) {
	client, c, err := m.dial(req)
	if err != nil {
		return SessionInfo{}, err
	}
	sh := newSharedClient(client)
	info, err := m.startSFTP(sh, c)
	if err != nil {
		sh.release()
	}
	return info, err
}

// OpenSFTPFrom abre SFTP reutilizando la conexión de una sesión existente.
func (m *Manager) OpenSFTPFrom(sessionID string) (SessionInfo, error) {
	src, err := m.get(sessionID)
	if err != nil {
		return SessionInfo{}, err
	}
	if src.shared == nil {
		return SessionInfo{}, errNotRemote
	}
	c, _ := m.store.Get(src.ConnID)
	if err := src.shared.acquire(); err != nil {
		return SessionInfo{}, err
	}
	info, err := m.startSFTP(src.shared, c)
	if err != nil {
		src.shared.release()
	}
	return info, err
}

// OpenTerminalFrom abre otra terminal sobre la conexión de una sesión existente.
func (m *Manager) OpenTerminalFrom(sessionID string, cols, rows int) (SessionInfo, error) {
	src, err := m.get(sessionID)
	if err != nil {
		return SessionInfo{}, err
	}
	if src.shared == nil {
		return SessionInfo{}, errNotRemote
	}
	c, _ := m.store.Get(src.ConnID)
	if err := src.shared.acquire(); err != nil {
		return SessionInfo{}, err
	}
	s, err := m.startShell(src.shared, c, cols, rows, false)
	if err != nil {
		src.shared.release()
		return SessionInfo{}, err
	}
	return SessionInfo{ID: s.ID, Kind: KindTerminal, ConnID: c.ID, Title: c.Name}, nil
}

func (m *Manager) startSFTP(sh *sharedClient, c store.Connection) (SessionInfo, error) {
	cl, err := sftp.NewClient(sh.client,
		sftp.UseConcurrentWrites(true),
		sftp.UseConcurrentReads(true),
		sftp.MaxConcurrentRequestsPerFile(64),
	)
	if err != nil {
		return SessionInfo{}, i18n.Err("remote.theServerDoesnTSupport", err)
	}
	home, err := cl.Getwd()
	if err != nil || home == "" {
		home = "/"
	}
	s := &Session{ID: uuid.NewString(), Kind: KindSFTP, ConnID: c.ID, name: c.Name, shared: sh, sftp: cl}
	m.register(s)
	return SessionInfo{ID: s.ID, Kind: KindSFTP, ConnID: c.ID, Title: c.Name, Home: home}, nil
}

// Attach indica que el frontend ya está escuchando la salida de la terminal.
func (m *Manager) Attach(id string) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	if s.ready != nil {
		s.readyOnce.Do(func() { close(s.ready) })
	}
	return nil
}

// Write envía teclas a la terminal.
func (m *Manager) Write(id, data string) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	if s.stdin == nil {
		return i18n.Err("remote.theSessionIsNotA")
	}
	_, err = io.WriteString(s.stdin, data)
	return err
}

// Resize ajusta el tamaño de la PTY.
func (m *Manager) Resize(id string, cols, rows int) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	if cols <= 0 || rows <= 0 {
		return nil
	}
	if s.screen != nil {
		s.screen.Resize(cols, rows)
	}
	if s.local != nil {
		return s.local.Resize(cols, rows)
	}
	if s.ssh == nil {
		return nil
	}
	return s.ssh.WindowChange(rows, cols)
}

// Close cierra una pestaña y libera la conexión si nadie más la usa.
func (m *Manager) Close(id string) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()
	if !ok {
		return
	}
	s.closeOnce.Do(func() {
		slog.Info("sesión cerrada", "id", id, "kind", s.Kind, "conn", s.name)
		m.transfers.cancelSession(id)
		m.stopSearches(id)
		m.dropSyncPlan(id)
		if s.ssh != nil {
			s.ssh.Close()
		}
		if s.local != nil {
			s.local.Close()
		}
		if s.sftp != nil {
			s.sftp.Close()
		}
		if s.shared != nil {
			s.shared.release()
		}
	})
}

func (m *Manager) CloseAll() {
	m.stopAllForwards()
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Close(id)
	}
	m.web.CloseAll()
}

func (m *Manager) sftpOf(id string) (*sftp.Client, error) {
	s, err := m.get(id)
	if err != nil {
		return nil, err
	}
	if s.sftp == nil {
		return nil, i18n.Err("remote.theSessionIsNotSftp")
	}
	return s.sftp, nil
}

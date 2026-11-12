package remote

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"

	"osat/internal/applog"
	"osat/internal/i18n"
	"osat/internal/portfwd"
	"osat/internal/store"
)

// Reenvío de puertos: un puerto de esta PC (solo loopback) que llega, por la
// conexión SSH de una sesión, a un puerto visto desde el servidor o a un pod o
// servicio de Kubernetes (ver k8s.go). El remoto es al revés: un puerto del
// servidor que llega a un puerto de esta PC. El dinámico es un proxy SOCKS5
// en un puerto de esta PC que sale por el servidor. Cada reenvío pertenece a
// una pestaña (owner) y se detiene con ella.

const KindTunnel Kind = "tunnel"

// errServerGone: la pestaña de puertos lo reconoce (por su código) y reconecta.
var errServerGone = newErr(CodeServerGone, "se cortó la conexión con el servidor", nil)

// OpenTunnel abre una sesión que solo mantiene la conexión SSH (sin shell ni SFTP), para los reenvíos.
func (m *Manager) OpenTunnel(req ConnectRequest) (SessionInfo, error) {
	client, c, err := m.dial(req)
	if err != nil {
		return SessionInfo{}, err
	}
	return m.startTunnel(newSharedClient(client), c), nil
}

// OpenTunnelFrom hace lo mismo reutilizando la conexión de una sesión existente.
func (m *Manager) OpenTunnelFrom(sessionID string) (SessionInfo, error) {
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
	return m.startTunnel(src.shared, c), nil
}

func (m *Manager) startTunnel(sh *sharedClient, c store.Connection) SessionInfo {
	s := &Session{ID: uuid.NewString(), Kind: KindTunnel, ConnID: c.ID, name: c.Name, shared: sh}
	m.register(s)
	return SessionInfo{ID: s.ID, Kind: KindTunnel, ConnID: c.ID, Title: c.Name}
}

// Estados de un reenvío en marcha. Uno detenido no figura.
const (
	FwStarting = "starting"
	FwRunning  = "running"
	FwError    = "error"
)

// ForwardState es lo que se ve de un reenvío.
type ForwardState struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`   // Status error: por qué se cortó o no arrancó
	Warning   string `json:"warning,omitempty"` // en marcha, pero el último intento no llegó al destino
	LocalPort int    `json:"localPort"`
	Active    int    `json:"active"`
	Total     int64  `json:"total"`
	BytesIn   int64  `json:"bytesIn"`
	BytesOut  int64  `json:"bytesOut"`
}

type fwd struct {
	rule   store.Forward
	owner  string
	shared *sharedClient

	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once

	mu     sync.Mutex
	status string
	errMsg string
	target string // host:puerto visto desde el servidor ("" hasta que está listo)
	ln     *portfwd.Listener
	kube   *kubePF
	last   portfwd.Stats // al terminar, lo último que se contó
}

// forwardAttrs son los datos de un reenvío para el log: su nombre, el puerto de esta
// PC y el destino (host:puerto, o recurso de Kubernetes).
func forwardAttrs(f store.Forward) []any {
	target := net.JoinHostPort(f.Host, strconv.Itoa(f.Port))
	if f.IsKubernetes() {
		target = f.Namespace + "/" + f.Resource + ":" + strconv.Itoa(f.Port)
	}
	return []any{"forward", f.Name, "local_port", f.LocalPort, "target", target}
}

func (fw *fwd) state() ForwardState {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	s := ForwardState{ID: fw.rule.ID, Status: fw.status, Error: fw.errMsg, LocalPort: fw.rule.LocalPort}
	st := fw.last
	if fw.ln != nil {
		st = fw.ln.Stats()
	}
	if fw.rule.Kind == store.ForwardRemote { // las conexiones llegan del servidor: lo que entra a esta PC es lo que sale hacia el destino
		st.BytesIn, st.BytesOut = st.BytesOut, st.BytesIn
	}
	s.Active, s.Total, s.BytesIn, s.BytesOut = st.Active, st.Total, st.BytesIn, st.BytesOut
	if fw.status == FwRunning {
		s.Warning = st.LastErr
	}
	return s
}

// finish termina el reenvío (una sola vez). msg vacío = lo detuvo el usuario; si no, queda en error con ese motivo.
func (fw *fwd) finish(msg string) {
	fw.once.Do(func() {
		if msg != "" {
			slog.Warn("reenvío cortado", append(forwardAttrs(fw.rule), "err", msg)...)
		} else {
			slog.Info("reenvío detenido", forwardAttrs(fw.rule)...)
		}
		fw.cancel()
		fw.mu.Lock()
		if msg != "" {
			fw.status, fw.errMsg = FwError, msg
		}
		ln, kube := fw.ln, fw.kube
		fw.mu.Unlock()
		close(fw.done)
		if ln != nil {
			fw.mu.Lock()
			fw.last = ln.Stats()
			fw.ln = nil
			fw.mu.Unlock()
			ln.Close()
		}
		if kube != nil {
			kube.stop()
		}
		fw.shared.release()
	})
}

func (fw *fwd) targetAddr() string {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	return fw.target
}

// dial abre una conexión al destino, desde el servidor.
func (fw *fwd) dial(ctx context.Context) (net.Conn, error) {
	addr := fw.targetAddr()
	if addr == "" {
		return nil, i18n.Err("remote.theForwardIsStillStarting")
	}
	return fw.dialAddr(ctx, addr, fw.rule.Host+":"+strconv.Itoa(fw.rule.Port))
}

// dialSOCKS abre la conexión que pidió un programa por el proxy SOCKS: addr
// es "host:puerto" y un nombre lo resuelve el servidor.
func (fw *fwd) dialSOCKS(ctx context.Context, addr string) (net.Conn, error) {
	return fw.dialAddr(ctx, addr, addr)
}

// dialAddr abre una conexión a addr desde el servidor; shown es cómo se nombra el destino en los errores.
func (fw *fwd) dialAddr(ctx context.Context, addr, shown string) (net.Conn, error) {
	type result struct {
		c   net.Conn
		err error
	}
	ch := make(chan result, 1)
	go func() {
		defer applog.Recover("reenvío: conexión al destino")
		c, err := fw.shared.client.Dial("tcp", addr)
		ch <- result{c, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			err := fw.explain(r.err, shown)
			var oe *ssh.OpenChannelError
			if errors.As(r.err, &oe) { // el servidor no pudo abrir el canal (el SOCKS lo contesta como rechazada)
				err = portfwd.Refused(err)
			}
			return nil, err
		}
		return r.c, nil
	case <-ctx.Done():
		go func() { // el Dial sigue en curso: si llega, se descarta
			if r := <-ch; r.c != nil {
				r.c.Close()
			}
		}()
		return nil, i18n.Err("remote.theServerDidnTAnswer")
	}
}

// dialLocal abre la conexión al destino en esta PC (reenvío remoto).
func (fw *fwd) dialLocal(ctx context.Context) (net.Conn, error) {
	addr := net.JoinHostPort(fw.rule.Host, strconv.Itoa(fw.rule.LocalPort))
	c, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, i18n.Err("remote.couldnTReachSOn", addr, err)
	}
	return c, nil
}

// listenRemote pide al servidor que abra el puerto del reenvío remoto.
func listenRemote(client *ssh.Client, f store.Forward) (net.Listener, error) {
	bind := "127.0.0.1"
	if f.BindAll {
		bind = "0.0.0.0" // el servidor solo lo respeta con GatewayPorts
	}
	ln, err := client.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(f.Port)))
	if err != nil {
		// SSH no dice por qué se rechazó (x/crypto solo devuelve "request denied by peer").
		if strings.Contains(err.Error(), "denied") {
			return nil, i18n.Err("remote.serverDeniedPort", f.Port)
		}
		return nil, i18n.Err("remote.couldnTOpenPortD", f.Port, err)
	}
	return ln, nil
}

func (fw *fwd) explain(err error, shown string) error {
	var oe *ssh.OpenChannelError
	if errors.As(err, &oe) {
		switch {
		case oe.Reason == ssh.Prohibited:
			return i18n.Err("remote.theServerDoesnTAllow4")
		case strings.Contains(strings.ToLower(oe.Message), "refused"):
			return i18n.Err("remote.nothingIsListeningOnS", shown)
		default:
			return i18n.Err("remote.couldnTReachSFrom", shown, oe.Message)
		}
	}
	return err
}

// StartForward arranca un reenvío guardado sobre la conexión de la sesión. Vuelve cuando ya está
// escuchando (con Kubernetes, cuando kubectl abrió el puerto). Si falla, además de devolver el error
// el reenvío queda en estado "error" para que se vea el motivo.
func (m *Manager) StartForward(owner, sessionID string, f store.Forward) error {
	if err := f.Validate(); err != nil {
		return err
	}
	slog.Debug("reenvío pedido", append(forwardAttrs(f), "conn", m.sessionConn(sessionID))...)
	src, err := m.get(sessionID)
	if err != nil {
		return err
	}
	if src.shared == nil {
		return errNotRemote
	}

	select {
	case <-src.shared.lost():
		return errServerGone // la pestaña tiene que reconectar
	default:
	}

	m.fwMu.Lock()
	if old, ok := m.fwds[f.ID]; ok {
		if old.state().Status != FwError {
			m.fwMu.Unlock()
			return i18n.Err("remote.thatForwardIsAlreadyRunning")
		}
		delete(m.fwds, f.ID) // reintento tras un error
	}
	for _, o := range m.fwds {
		// El remoto no abre nada en esta PC: su LocalPort es el destino.
		if f.Kind == store.ForwardRemote || o.rule.Kind == store.ForwardRemote {
			continue
		}
		if o.rule.LocalPort == f.LocalPort && o.state().Status != FwError {
			m.fwMu.Unlock()
			return i18n.Err("remote.portDOnThisPc", f.LocalPort, o.rule.Name)
		}
	}
	if err := src.shared.acquire(); err != nil {
		m.fwMu.Unlock()
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	fw := &fwd{rule: f, owner: owner, shared: src.shared, cancel: cancel, done: make(chan struct{}), status: FwStarting}
	m.fwds[f.ID] = fw
	m.fwMu.Unlock()

	fail := func(err error) error {
		fw.finish(err.Error())
		return err
	}
	var ln *portfwd.Listener
	switch f.Kind {
	case store.ForwardRemote:
		rln, err := listenRemote(src.shared.client, f)
		if err != nil {
			return fail(err)
		}
		ln = portfwd.Serve(rln, fw.dialLocal)
	case store.ForwardDynamic:
		ln, err = portfwd.ListenSOCKS(f.LocalPort, fw.dialSOCKS)
		if err != nil {
			return fail(err)
		}
	default:
		ln, err = portfwd.Listen(f.LocalPort, fw.dial)
		if err != nil {
			return fail(err)
		}
	}
	fw.mu.Lock()
	fw.ln = ln
	fw.mu.Unlock()

	if f.IsKubernetes() {
		kube, port, err := startPortForward(ctx, src.shared.client, f.Namespace, f.Resource, f.Port)
		if err != nil {
			return fail(i18n.Err("remote.couldnTOpenSV", f.Resource, err))
		}
		fw.mu.Lock()
		fw.kube = kube
		fw.target = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		fw.mu.Unlock()
		select {
		case <-fw.done: // se detuvo justo mientras arrancaba: finish no vio este kubectl
			kube.stop()
			return i18n.Err("remote.itWasStoppedWhileStarting")
		default:
		}
		go func() { // el pod se reinició, desapareció, o kubectl murió
			defer applog.Recover("reenvío: vigilancia de kubectl")
			select {
			case <-kube.done:
				fw.finish(kubeEnded(kube))
			case <-fw.done:
			}
		}()
	} else if f.Kind == store.ForwardLocal { // el remoto va a Host:LocalPort de esta PC (ver dialLocal)
		fw.mu.Lock()
		fw.target = net.JoinHostPort(f.Host, strconv.Itoa(f.Port))
		fw.mu.Unlock()
	}

	fw.mu.Lock()
	if fw.status == FwStarting { // por si se detuvo mientras arrancaba
		fw.status = FwRunning
	}
	fw.mu.Unlock()
	go func() { // se cayó la conexión con el servidor
		defer applog.Recover("reenvío: vigilancia de la conexión")
		select {
		case <-src.shared.lost():
			fw.finish(errServerGone.Message)
		case <-fw.done:
		}
	}()
	slog.Info("reenvío iniciado", forwardAttrs(f)...)
	return nil
}

func kubeEnded(p *kubePF) string {
	return i18n.T("remote.kubeEnded", p.failure().Error())
}

// StopForward detiene un reenvío (o descarta uno que quedó en error).
func (m *Manager) StopForward(id string) {
	m.fwMu.Lock()
	fw := m.fwds[id]
	delete(m.fwds, id)
	m.fwMu.Unlock()
	if fw != nil {
		fw.finish("")
	}
}

// StopForwardsOf detiene los reenvíos que arrancó una pestaña.
func (m *Manager) StopForwardsOf(owner string) {
	m.fwMu.Lock()
	var stop []*fwd
	for id, fw := range m.fwds {
		if fw.owner == owner {
			stop = append(stop, fw)
			delete(m.fwds, id)
		}
	}
	m.fwMu.Unlock()
	for _, fw := range stop {
		fw.finish("")
	}
}

// ForwardStates lista los reenvíos de una conexión que están en marcha o en error.
func (m *Manager) ForwardStates(connID string) []ForwardState {
	m.fwMu.Lock()
	all := make([]*fwd, 0, len(m.fwds))
	for _, fw := range m.fwds {
		if fw.rule.ConnID == connID {
			all = append(all, fw)
		}
	}
	m.fwMu.Unlock()
	out := make([]ForwardState, 0, len(all))
	for _, fw := range all {
		out = append(out, fw.state())
	}
	return out
}

func (m *Manager) stopAllForwards() {
	m.fwMu.Lock()
	all := m.fwds
	m.fwds = map[string]*fwd{}
	m.fwMu.Unlock()
	for _, fw := range all {
		fw.finish("")
	}
}

// Package portfwd abre un puerto en esta PC (solo loopback) y pasa cada
// conexión que llega a otro lugar: normalmente un puerto al que se llega desde
// el servidor, por la conexión SSH. Es el "-L" de ssh.
package portfwd

import (
	"context"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"osat/internal/i18n"
)

// Dial abre la conexión de destino de una conexión que llegó al puerto local.
type Dial func(ctx context.Context) (net.Conn, error)

// connect abre la conexión de destino conociendo la que llegó: Dial no la
// necesita, pero el SOCKS tiene que leer de ahí a dónde quiere ir el programa.
type connect func(ctx context.Context, local net.Conn) (net.Conn, error)

func fixed(dial Dial) connect {
	return func(ctx context.Context, _ net.Conn) (net.Conn, error) { return dial(ctx) }
}

// Stats es lo que se muestra de un reenvío en marcha.
type Stats struct {
	Active   int    // conexiones abiertas ahora
	Total    int64  // conexiones desde que arrancó
	BytesIn  int64  // del destino hacia esta PC
	BytesOut int64  // de esta PC hacia el destino
	LastErr  string // último intento que no pudo llegar al destino ("" si el último anduvo)
}

const dialTimeout = 10 * time.Second

// Listener escucha en 127.0.0.1 (y en ::1, si el equipo lo tiene, para que
// "localhost" ande aunque el programa elija IPv6).
type Listener struct {
	lns     []net.Listener
	connect connect
	port    int

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	active  atomic.Int64
	total   atomic.Int64
	in, out atomic.Int64
	mu      sync.Mutex
	lastErr string
	conns   map[net.Conn]struct{}
	closed  bool
}

// Listen empieza a escuchar en el puerto (0 = uno libre, ver Port).
func Listen(port int, dial Dial) (*Listener, error) {
	return listen(port, fixed(dial))
}

func listen(port int, conn connect) (*Listener, error) {
	if port < 0 || port > 65535 {
		return nil, i18n.Err("app.invalidPort")
	}
	v4, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, busy(port, err)
	}
	lns := []net.Listener{v4}
	port = v4.Addr().(*net.TCPAddr).Port
	// IPv6 es opcional: si no hay o el puerto está tomado en ::1, queda solo IPv4.
	if v6, err := net.Listen("tcp", net.JoinHostPort("::1", strconv.Itoa(port))); err == nil {
		lns = append(lns, v6)
	}
	return serve(lns, port, conn), nil
}

// Serve atiende las conexiones que lleguen a ln, que ya está escuchando (por
// ejemplo el puerto que abrió el servidor SSH en un reenvío remoto), con las
// mismas cuentas que Listen. Close también cierra ln.
func Serve(ln net.Listener, dial Dial) *Listener {
	port := 0
	if a, ok := ln.Addr().(*net.TCPAddr); ok {
		port = a.Port
	}
	return serve([]net.Listener{ln}, port, fixed(dial))
}

func serve(lns []net.Listener, port int, conn connect) *Listener {
	l := &Listener{lns: lns, connect: conn, port: port, conns: map[net.Conn]struct{}{}}
	l.ctx, l.cancel = context.WithCancel(context.Background())
	for _, ln := range l.lns {
		l.wg.Add(1)
		go l.accept(ln)
	}
	return l
}

// busy explica el error de abrir el puerto, que casi siempre es que otro programa ya lo usa
// (el texto del sistema cambia con el idioma, así que no se mira).
func busy(port int, err error) error {
	return i18n.Err("app.couldnTOpenPortD", port, err)
}

// Port es el puerto en el que se escucha (de esta PC, o del servidor con Serve).
func (l *Listener) Port() int { return l.port }

func (l *Listener) Stats() Stats {
	l.mu.Lock()
	defer l.mu.Unlock()
	return Stats{Active: int(l.active.Load()), Total: l.total.Load(), BytesIn: l.in.Load(), BytesOut: l.out.Load(), LastErr: l.lastErr}
}

func (l *Listener) setErr(s string) {
	l.mu.Lock()
	l.lastErr = s
	l.mu.Unlock()
}

// Close deja de escuchar y corta las conexiones abiertas.
func (l *Listener) Close() {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.closed = true
	conns := make([]net.Conn, 0, len(l.conns))
	for c := range l.conns {
		conns = append(conns, c)
	}
	l.mu.Unlock()
	l.cancel()
	for _, ln := range l.lns {
		ln.Close()
	}
	for _, c := range conns {
		c.Close()
	}
	l.wg.Wait()
}

func (l *Listener) track(c net.Conn) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return false
	}
	l.conns[c] = struct{}{}
	return true
}

func (l *Listener) untrack(c net.Conn) {
	l.mu.Lock()
	delete(l.conns, c)
	l.mu.Unlock()
}

func (l *Listener) accept(ln net.Listener) {
	defer l.wg.Done()
	for {
		c, err := ln.Accept()
		if err != nil {
			return // cerrado
		}
		l.wg.Add(1)
		go l.serve(c)
	}
}

func (l *Listener) serve(local net.Conn) {
	defer l.wg.Done()
	if !l.track(local) {
		local.Close()
		return
	}
	defer l.untrack(local)
	defer local.Close()
	l.total.Add(1)
	l.active.Add(1)
	defer l.active.Add(-1)

	ctx, cancel := context.WithTimeout(l.ctx, dialTimeout)
	remote, err := l.connect(ctx, local)
	cancel()
	if err != nil {
		l.setErr(err.Error())
		return
	}
	l.setErr("")
	if !l.track(remote) {
		remote.Close()
		return
	}
	defer l.untrack(remote)
	defer remote.Close()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); relay(remote, local, &l.out) }()
	go func() { defer wg.Done(); relay(local, remote, &l.in) }()
	wg.Wait()
}

// relay copia src → dst y avisa que no va a escribir más (así el otro lado
// puede terminar de contestar, p. ej. un cliente que cierra su envío primero).
func relay(dst, src net.Conn, n *atomic.Int64) {
	_, _ = io.Copy(dst, countingReader{src, n})
	if cw, ok := dst.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	} else {
		dst.Close()
	}
}

type countingReader struct {
	r io.Reader
	n *atomic.Int64
}

func (c countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// FreePort devuelve preferred si está libre en esta PC y, si no, el próximo que lo esté
// (0 si no encuentra ninguno).
func FreePort(preferred int) int {
	if preferred < 1 || preferred > 65535 {
		preferred = 1024
	}
	for p := preferred; p < preferred+200 && p <= 65535; p++ {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
		if err == nil {
			ln.Close()
			return p
		}
	}
	return 0
}

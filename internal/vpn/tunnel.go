package vpn

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"

	"osat/internal/i18n"
)

// defaultKeepalive se usa si el .conf no indica PersistentKeepalive: hace que el
// handshake salga apenas se activa la VPN (así se sabe enseguida si el servidor
// contesta) y mantiene abierto el NAT del router.
const defaultKeepalive = 25

// Cuánto se espera el primer handshake antes de decir que el servidor no
// contesta, y cuánto vale uno: WireGuard renueva cada 2 minutos mientras hay
// tráfico (el keepalive lo genera), así que uno de hace más de 3 ya es viejo.
const (
	handshakeWait = 15 * time.Second
	handshakeLife = 3 * time.Minute
)

// ErrDown: la VPN está activada pero el túnel no está levantado. La conexión
// falla en vez de salir directo por la red del equipo.
var ErrDown = i18n.Err("vpn.down")

type tunnel struct {
	dev     *device.Device
	net     *netstack.Net
	noDNS   bool // el .conf no trae DNS: los nombres se resuelven con el del sistema
	started time.Time
	address string
	server  string
}

// start levanta el túnel con cfg.
func start(cfg Config) (*tunnel, error) {
	addrs := make([]netip.Addr, len(cfg.Addresses))
	for i, p := range cfg.Addresses {
		addrs[i] = p.Addr()
	}
	tdev, tnet, err := netstack.CreateNetTUN(addrs, cfg.DNS, cfg.MTU)
	if err != nil {
		return nil, i18n.Err("vpn.startFailed", err)
	}
	ipc, err := ipcConfig(cfg)
	if err != nil {
		tdev.Close()
		return nil, err
	}
	logger := &device.Logger{
		Verbosef: device.DiscardLogf,
		Errorf: func(format string, args ...any) {
			slog.Debug("wireguard: " + fmt.Sprintf(format, args...))
		},
	}
	dev := device.NewDevice(tdev, conn.NewDefaultBind(), logger)
	if err := dev.IpcSet(ipc); err != nil {
		dev.Close()
		return nil, i18n.Err("vpn.startFailed", err)
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		return nil, i18n.Err("vpn.startFailed", err)
	}
	return &tunnel{
		dev:     dev,
		net:     tnet,
		noDNS:   len(cfg.DNS) == 0,
		started: time.Now(),
		address: cfg.Addresses[0].Addr().String(),
		server:  cfg.Peers[0].Endpoint,
	}, nil
}

// ipcConfig arma la configuración en el formato de wireguard-go (claves en
// hexadecimal, endpoints como IP:puerto).
func ipcConfig(cfg Config) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "private_key=%s\n", hex.EncodeToString(cfg.PrivateKey))
	for _, p := range cfg.Peers {
		ep, err := resolveEndpoint(p.Endpoint)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "public_key=%s\n", hex.EncodeToString(p.PublicKey))
		if p.PresharedKey != nil {
			fmt.Fprintf(&b, "preshared_key=%s\n", hex.EncodeToString(p.PresharedKey))
		}
		fmt.Fprintf(&b, "endpoint=%s\n", ep)
		ka := p.Keepalive
		if ka == 0 {
			ka = defaultKeepalive
		}
		fmt.Fprintf(&b, "persistent_keepalive_interval=%d\n", ka)
		allowed := p.AllowedIPs
		if len(allowed) == 0 {
			allowed = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}
		}
		for _, a := range allowed {
			fmt.Fprintf(&b, "allowed_ip=%s\n", a)
		}
	}
	return b.String(), nil
}

// resolveEndpoint pasa "host:puerto" a "IP:puerto" con el DNS del sistema (es
// lo único que sale por fuera del túnel: hace falta para llegar a él).
// Prefiere IPv4, que es lo que tienen casi todas las redes.
func resolveEndpoint(ep string) (string, error) {
	host, port, err := splitHostPort(ep)
	if err != nil {
		return "", i18n.Err("vpn.badEndpoint", ep)
	}
	if a, err := netip.ParseAddr(host); err == nil {
		return netip.AddrPortFrom(a, port).String(), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return "", i18n.Err("vpn.endpointLookup", host, err)
	}
	pick := ips[0]
	for _, ip := range ips {
		if ip.Unmap().Is4() {
			pick = ip
			break
		}
	}
	return netip.AddrPortFrom(pick.Unmap(), port).String(), nil
}

func (t *tunnel) close() { t.dev.Close() }

func (t *tunnel) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	if t.noDNS {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if _, err := netip.ParseAddr(host); err != nil {
			ips, err := net.DefaultResolver.LookupHost(ctx, host)
			if err != nil {
				return nil, err
			}
			addr = net.JoinHostPort(ips[0], port)
		}
	}
	return t.net.DialContext(ctx, network, addr)
}

// lastHandshake es el handshake más reciente con algún servidor (cero si no hubo).
func (t *tunnel) lastHandshake() time.Time {
	s, err := t.dev.IpcGet()
	if err != nil {
		return time.Time{}
	}
	var last int64
	for _, l := range strings.Split(s, "\n") {
		if v, ok := strings.CutPrefix(l, "last_handshake_time_sec="); ok {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > last {
				last = n
			}
		}
	}
	if last == 0 {
		return time.Time{}
	}
	return time.Unix(last, 0)
}

// ---------- Estado global ----------

var (
	mu      sync.Mutex
	cur     *tunnel // nil si no hay túnel levantado
	wanted  bool    // la VPN está activada (aunque el túnel haya fallado)
	lastErr error   // por qué falló el túnel, si wanted y cur == nil
)

// Start activa la VPN con cfg (si ya había un túnel, lo reemplaza). Aunque
// falle, la VPN queda activada: las conexiones dan error en vez de salir
// directo, hasta que se desactive.
func Start(cfg Config) error {
	mu.Lock()
	defer mu.Unlock()
	if cur != nil {
		cur.close()
		cur = nil
	}
	wanted = true
	err := cfg.validate()
	var t *tunnel
	if err == nil {
		t, err = start(cfg)
	}
	if err != nil {
		lastErr = err
		slog.Warn("no se pudo levantar la VPN", "err", err)
		return err
	}
	cur, lastErr = t, nil
	slog.Info("VPN activada", "server", t.server, "address", t.address)
	return nil
}

// Stop desactiva la VPN: las conexiones nuevas vuelven a salir directo. Las que
// ya estaban abiertas por el túnel se cortan.
func Stop() {
	mu.Lock()
	defer mu.Unlock()
	if cur != nil {
		cur.close()
		cur = nil
		slog.Info("VPN desactivada")
	}
	wanted, lastErr = false, nil
}

// Enabled dice si la VPN está activada (aunque el túnel haya fallado: en ese
// caso Dial da error). Para los programas aparte (el cliente de Escritorio
// remoto de Windows, mongosh), que no usan Dial: osat les da un puerto local
// que sale por el túnel.
func Enabled() bool {
	mu.Lock()
	defer mu.Unlock()
	return wanted
}

// Dial abre una conexión: por el túnel si la VPN está activada, directo si no.
func Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	mu.Lock()
	t, w := cur, wanted
	mu.Unlock()
	if !w {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	if t == nil {
		return nil, ErrDown
	}
	c, err := t.dial(ctx, network, addr)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, i18n.Err("vpn.dialTimeout", addr)
		}
		return nil, i18n.Err("vpn.dialFailed", addr, err)
	}
	return c, nil
}

// DialTimeout es Dial con un plazo, como net.DialTimeout.
func DialTimeout(network, addr string, timeout time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return Dial(ctx, network, addr)
}

// Estados de la VPN (Status.State).
const (
	StateOff        = "off"
	StateConnecting = "connecting" // recién activada, esperando el primer handshake
	StateOn         = "on"         // hubo handshake hace poco
	StateNoResponse = "noResponse" // activada, pero el servidor no contesta
	StateError      = "error"      // activada, pero el túnel no se pudo levantar
)

// Status es el estado de la VPN para mostrar.
type Status struct {
	State         string `json:"state"`
	Error         string `json:"error,omitempty"`
	Address       string `json:"address,omitempty"` // IP dentro del túnel
	Server        string `json:"server,omitempty"`  // endpoint del .conf
	LastHandshake int64  `json:"lastHandshake,omitempty"`
}

// Current devuelve el estado de la VPN.
func Current() Status {
	mu.Lock()
	t, w, e := cur, wanted, lastErr
	mu.Unlock()
	switch {
	case !w:
		return Status{State: StateOff}
	case t == nil:
		msg := ""
		if e != nil {
			msg = e.Error()
		}
		return Status{State: StateError, Error: msg}
	}
	s := Status{Address: t.address, Server: t.server}
	hs := t.lastHandshake()
	switch {
	case !hs.IsZero() && time.Since(hs) < handshakeLife:
		s.State = StateOn
		s.LastHandshake = hs.Unix()
	case time.Since(t.started) < handshakeWait:
		s.State = StateConnecting
	default:
		s.State = StateNoResponse
	}
	return s
}

package remote

import (
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"osat/internal/portfwd"
	"osat/internal/store"
)

// remoteFwdServer es un servidor SSH que atiende tcpip-forward como sshd: por
// cada pedido escucha de verdad (en 127.0.0.1:0, para no chocar con nada) y
// cada conexión que llega la pasa al cliente por un canal forwarded-tcpip.
// Con deny rechaza todos los pedidos.
type remoteFwdServer struct {
	mu       sync.Mutex
	binds    map[uint32]string       // puerto pedido → dirección en la que escucha de verdad
	lns      map[uint32]net.Listener // los abiertos
	requests []string                // "addr:puerto" de cada tcpip-forward, como llegó
	canceled []uint32
}

func newRemoteFwdServer(t *testing.T, deny bool) (*remoteFwdServer, *ssh.Client) {
	t.Helper()
	srv := &remoteFwdServer{binds: map[uint32]string{}, lns: map[uint32]net.Listener{}}
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ln.Close()
		srv.mu.Lock()
		for _, l := range srv.lns {
			l.Close()
		}
		srv.mu.Unlock()
	})
	go func() {
		nc, err := ln.Accept()
		if err != nil {
			return
		}
		conn, chans, reqs, err := ssh.NewServerConn(nc, cfg)
		if err != nil {
			return
		}
		go func() {
			for nch := range chans {
				nch.Reject(ssh.UnknownChannelType, "solo reenvío remoto")
			}
		}()
		for req := range reqs {
			var m struct {
				Addr string
				Port uint32
			}
			ssh.Unmarshal(req.Payload, &m)
			switch req.Type {
			case "tcpip-forward":
				srv.mu.Lock()
				srv.requests = append(srv.requests, net.JoinHostPort(m.Addr, strconv.Itoa(int(m.Port))))
				srv.mu.Unlock()
				if deny {
					req.Reply(false, nil)
					continue
				}
				l, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					req.Reply(false, nil)
					continue
				}
				srv.mu.Lock()
				srv.binds[m.Port] = l.Addr().String()
				srv.lns[m.Port] = l
				srv.mu.Unlock()
				req.Reply(true, nil)
				go srv.accept(conn, l, m.Addr, m.Port)
			case "cancel-tcpip-forward":
				srv.mu.Lock()
				srv.canceled = append(srv.canceled, m.Port)
				if l := srv.lns[m.Port]; l != nil {
					l.Close()
					delete(srv.lns, m.Port)
				}
				srv.mu.Unlock()
				req.Reply(true, nil)
			default:
				if req.WantReply {
					req.Reply(false, nil)
				}
			}
		}
	}()
	client, err := ssh.Dial("tcp", ln.Addr().String(), &ssh.ClientConfig{User: "u", HostKeyCallback: ssh.InsecureIgnoreHostKey()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return srv, client
}

// accept abre un canal forwarded-tcpip por cada conexión al puerto del "servidor".
func (srv *remoteFwdServer) accept(conn *ssh.ServerConn, l net.Listener, addr string, port uint32) {
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			payload := ssh.Marshal(struct {
				Addr       string
				Port       uint32
				OriginAddr string
				OriginPort uint32
			}{addr, port, "127.0.0.1", 40000})
			ch, reqs, err := conn.OpenChannel("forwarded-tcpip", payload)
			if err != nil {
				return
			}
			go ssh.DiscardRequests(reqs)
			go func() { io.Copy(ch, c); ch.CloseWrite() }()
			io.Copy(c, ch)
			ch.Close()
		}()
	}
}

func (srv *remoteFwdServer) addr(port int) string {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	return srv.binds[uint32(port)]
}

func (srv *remoteFwdServer) log() (requests []string, canceled []uint32) {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	return append([]string(nil), srv.requests...), append([]uint32(nil), srv.canceled...)
}

// throughAddr manda msg a addr, cierra el envío y devuelve la respuesta.
func throughAddr(t *testing.T, addr, msg string) string {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	c.Write([]byte(msg))
	c.(*net.TCPConn).CloseWrite()
	b, _ := io.ReadAll(c)
	return string(b)
}

func remoteRig(t *testing.T, deny bool) (*Manager, *sharedClient, *remoteFwdServer) {
	t.Helper()
	srv, client := newRemoteFwdServer(t, deny)
	m := newBridgeManager(t)
	t.Cleanup(m.CloseAll)
	sh := newSharedClient(client)
	m.register(&Session{ID: "srv", Kind: KindTunnel, ConnID: "c", shared: sh})
	return m, sh, srv
}

func TestRemoteForward(t *testing.T) {
	backend := echoBackend(t) // el servicio de esta PC
	m, sh, srv := remoteRig(t, false)

	rule := store.Forward{ID: "r", ConnID: "c", Kind: store.ForwardRemote, Port: 8022, LocalPort: backend}
	if err := m.StartForward("tab", "srv", rule); err != nil {
		t.Fatal(err)
	}
	if reqs, _ := srv.log(); len(reqs) != 1 || reqs[0] != "127.0.0.1:8022" {
		t.Fatalf("por defecto se pide solo el localhost del servidor: %q", reqs)
	}
	if got := throughAddr(t, srv.addr(8022), "hola"); got != "HOLA" {
		t.Fatalf("respuesta: %q", got)
	}
	waitFor(t, "estadísticas", func() bool { s, _ := stateOf(m, "r"); return s.Total == 1 && s.Active == 0 })
	s, _ := stateOf(m, "r")
	if s.Status != FwRunning || s.BytesIn != 4 || s.BytesOut != 4 || s.Warning != "" {
		t.Fatalf("estado: %+v", s)
	}

	// Un local con el mismo puerto de esta PC no choca con el remoto: el remoto no abre nada acá.
	local := store.Forward{ID: "l", ConnID: "c", LocalPort: portfwd.FreePort(21500), Port: 80}
	other := store.Forward{ID: "r2", ConnID: "c", Kind: store.ForwardRemote, Port: 8023, LocalPort: local.LocalPort}
	if err := m.StartForward("tab", "srv", local); err != nil {
		t.Fatal(err)
	}
	if err := m.StartForward("tab", "srv", other); err != nil {
		t.Fatalf("remoto hacia el puerto de un local: %v", err)
	}

	// Detener cancela el pedido en el servidor y libera el puerto.
	addr := srv.addr(8022)
	m.StopForward("r")
	if _, canceled := srv.log(); len(canceled) != 1 || canceled[0] != 8022 {
		t.Fatalf("cancel-tcpip-forward: %v", canceled)
	}
	waitFor(t, "puerto del servidor libre", func() bool {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			c.Close()
		}
		return err != nil
	})
	if _, ok := stateOf(m, "r"); ok {
		t.Fatal("un reenvío detenido no figura")
	}
	m.StopForward("l")
	m.StopForward("r2")
	if sh.refs != 1 {
		t.Fatalf("al detenerlos se suelta la conexión: refs=%d", sh.refs)
	}
}

func TestRemoteForwardBindAll(t *testing.T) {
	m, _, srv := remoteRig(t, false)
	rule := store.Forward{ID: "r", ConnID: "c", Kind: store.ForwardRemote, Port: 9000, LocalPort: 3000, BindAll: true}
	if err := m.StartForward("tab", "srv", rule); err != nil {
		t.Fatal(err)
	}
	if reqs, _ := srv.log(); len(reqs) != 1 || reqs[0] != "0.0.0.0:9000" {
		t.Fatalf("pedido: %q", reqs)
	}
}

func TestRemoteForwardDeniedByServer(t *testing.T) {
	m, sh, _ := remoteRig(t, true)
	rule := store.Forward{ID: "r", ConnID: "c", Kind: store.ForwardRemote, Port: 80, LocalPort: 3000}
	err := m.StartForward("tab", "srv", rule)
	if err == nil {
		t.Fatal("el servidor lo rechazó: tiene que fallar")
	}
	s, ok := stateOf(m, "r")
	if !ok || s.Status != FwError {
		t.Fatalf("el motivo tiene que verse en el reenvío: %+v", s)
	}
	for _, want := range []string{"no permitió abrir el puerto 80", "ocupado", "root", "1024", "AllowTcpForwarding no"} {
		if !strings.Contains(s.Error, want) {
			t.Errorf("falta %q en %q", want, s.Error)
		}
	}
	if sh.refs != 1 {
		t.Fatalf("un reenvío que no arrancó no retiene la conexión: refs=%d", sh.refs)
	}
}

// Si en esta PC no hay nada escuchando, se avisa en la fila y el reenvío sigue.
func TestRemoteForwardToAClosedLocalPortShowsAWarning(t *testing.T) {
	m, _, srv := remoteRig(t, false)
	closed := portfwd.FreePort(21600)
	rule := store.Forward{ID: "r", ConnID: "c", Kind: store.ForwardRemote, Port: 8080, LocalPort: closed}
	if err := m.StartForward("tab", "srv", rule); err != nil {
		t.Fatal(err)
	}
	throughAddr(t, srv.addr(8080), "hola")
	waitFor(t, "aviso", func() bool { s, _ := stateOf(m, "r"); return s.Warning != "" })
	s, _ := stateOf(m, "r")
	if s.Status != FwRunning || !strings.Contains(s.Warning, "localhost:"+strconv.Itoa(closed)+" en esta PC") {
		t.Fatalf("estado: %+v", s)
	}
}

// Al cortarse la conexión con el servidor, el remoto pasa a error como los demás.
func TestRemoteForwardEndsWhenTheServerConnectionDrops(t *testing.T) {
	m, sh, _ := remoteRig(t, false)
	if err := m.StartForward("tab", "srv", store.Forward{ID: "r", ConnID: "c", Kind: store.ForwardRemote, Port: 8081, LocalPort: 3000}); err != nil {
		t.Fatal(err)
	}
	sh.client.Close()
	waitFor(t, "estado de error", func() bool { s, _ := stateOf(m, "r"); return s.Status == FwError })
	if s, _ := stateOf(m, "r"); !strings.Contains(s.Error, "se cortó la conexión") {
		t.Fatalf("estado: %+v", s)
	}
}

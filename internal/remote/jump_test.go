package remote

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
	"golang.org/x/crypto/ssh"

	"osat/internal/store"
)

// pwServer es un servidor SSH con contraseña que reenvía puertos
// (direct-tcpip); tunnels cuenta los reenvíos que abrió.
func pwServer(t *testing.T, password string, tunnels *atomic.Int32) (string, int) {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{PasswordCallback: func(_ ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
		if string(pw) == password {
			return nil, nil
		}
		return nil, errors.New("contraseña incorrecta")
	}}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, chans, reqs, err := ssh.NewServerConn(nc, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for nch := range chans {
					if nch.ChannelType() != "direct-tcpip" {
						nch.Reject(ssh.UnknownChannelType, "solo direct-tcpip")
						continue
					}
					var p struct {
						Host     string
						Port     uint32
						OrigHost string
						OrigPort uint32
					}
					ssh.Unmarshal(nch.ExtraData(), &p)
					dst, err := net.Dial("tcp", net.JoinHostPort(p.Host, strconv.Itoa(int(p.Port))))
					if err != nil {
						nch.Reject(ssh.ConnectionFailed, "Connection refused")
						continue
					}
					ch, chReqs, err := nch.Accept()
					if err != nil {
						dst.Close()
						continue
					}
					if tunnels != nil {
						tunnels.Add(1)
					}
					go ssh.DiscardRequests(chReqs)
					go func() { io.Copy(dst, ch); dst.Close() }()
					go func() { io.Copy(ch, dst); ch.Close() }()
				}
			}()
		}
	}()
	a := ln.Addr().(*net.TCPAddr)
	return a.IP.String(), a.Port
}

func jumpRig(t *testing.T) (*Manager, *store.Store) {
	t.Helper()
	keyring.MockInit()
	tmp := t.TempDir()
	t.Setenv("AppData", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)
	st, err := store.New()
	if err != nil {
		t.Fatal(err)
	}
	hk, err := NewHostKeys(filepath.Join(st.Dir(), "known_hosts"))
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(st, hk, func(string, ...interface{}) {})
	t.Cleanup(m.CloseAll)
	return m, st
}

// dialTrusting conecta confiando en las huellas nuevas (de cada salto) y
// devuelve el primer error que no sea de huella.
func dialTrusting(m *Manager, req ConnectRequest) (*ssh.Client, error) {
	for i := 0; i < 5; i++ {
		client, _, err := m.dial(req)
		var ke *Error
		if errors.As(err, &ke) && ke.Code == CodeHostKeyUnknown {
			if err := m.hostKeys.Trust(ke.Data["host"]); err != nil {
				return nil, err
			}
			continue
		}
		return client, err
	}
	return nil, errors.New("demasiados intentos")
}

func TestDialThroughJump(t *testing.T) {
	m, st := jumpRig(t)
	var tunnels atomic.Int32
	bh, bp := pwServer(t, "pw-bastion", &tunnels)
	th, tp := pwServer(t, "pw-target", nil)
	save := func(c store.Connection, secret string) store.Connection {
		t.Helper()
		c, err := st.Save(store.ConnectionInput{Connection: c, Secret: secret})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	bastion := save(store.Connection{Name: "bastion", Host: bh, Port: bp, User: "u", AuthType: store.AuthPassword}, "")
	target := save(store.Connection{Name: "db", Host: th, Port: tp, User: "u", AuthType: store.AuthPassword, JumpID: bastion.ID}, "pw-target")

	// El bastión no tiene contraseña guardada: se pide indicando cuál es.
	_, err := dialTrusting(m, ConnectRequest{ConnectionID: target.ID})
	var ke *Error
	if !errors.As(err, &ke) || ke.Code != CodeNeedSecret || ke.Data["connId"] != bastion.ID || ke.Data["name"] != "bastion" {
		t.Fatalf("err = %v", err)
	}
	// Contraseña equivocada del bastión: AUTH_FAILED del bastión.
	_, err = dialTrusting(m, ConnectRequest{ConnectionID: target.ID, JumpSecrets: map[string]JumpSecret{bastion.ID: {Secret: "mal"}}})
	if !errors.As(err, &ke) || ke.Code != CodeAuthFailed || ke.Data["connId"] != bastion.ID {
		t.Fatalf("err = %v", err)
	}
	client, err := dialTrusting(m, ConnectRequest{ConnectionID: target.ID, JumpSecrets: map[string]JumpSecret{bastion.ID: {Secret: "pw-bastion", Save: true}}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if tunnels.Load() == 0 {
		t.Fatal("la conexión no pasó por el bastión")
	}
	if b, _ := st.Get(bastion.ID); !b.HasSecret || st.Secret(bastion.ID) != "pw-bastion" {
		t.Fatalf("no se guardó la contraseña del bastión: %+v", b)
	}
	// La contraseña equivocada del destino es AUTH_FAILED del destino (sin connId).
	_, err = dialTrusting(m, ConnectRequest{ConnectionID: target.ID, Secret: "mal"})
	if !errors.As(err, &ke) || ke.Code != CodeAuthFailed || ke.Data["connId"] != "" {
		t.Fatalf("err = %v", err)
	}
}

func TestJumpHandshakeTimeout(t *testing.T) {
	bh, bp := pwServer(t, "pw", nil)
	via, err := ssh.Dial("tcp", net.JoinHostPort(bh, strconv.Itoa(bp)), &ssh.ClientConfig{
		User: "u", Auth: []ssh.AuthMethod{ssh.Password("pw")}, HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer via.Close()
	// Un servidor que acepta y nunca contesta, alcanzado por el túnel (que no acepta deadlines).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	prev := handshakeTimeout
	handshakeTimeout = 300 * time.Millisecond
	defer func() { handshakeTimeout = prev }()
	start := time.Now()
	_, err = dialWithDeadline(ln.Addr().String(), &ssh.ClientConfig{User: "x", HostKeyCallback: ssh.InsecureIgnoreHostKey()}, nil, via)
	if err == nil {
		t.Fatal("esperaba error de timeout")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("el dial por el salto tardó %v", d)
	}
}

func TestDialJumpForRDP(t *testing.T) {
	m, st := jumpRig(t)
	var tunnels atomic.Int32
	bh, bp := pwServer(t, "pw-bastion", &tunnels)
	bastion, err := st.Save(store.ConnectionInput{Connection: store.Connection{Name: "bastion", Host: bh, Port: bp, User: "u", AuthType: store.AuthPassword}})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0") // el "servidor RDP"
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		if c, err := ln.Accept(); err == nil {
			c.Write([]byte("rdp"))
			c.Close()
		}
	}()
	a := ln.Addr().(*net.TCPAddr)
	rdp, err := st.Save(store.ConnectionInput{Connection: store.Connection{Name: "win", Protocol: store.ProtoRDP, Host: a.IP.String(), Port: a.Port, User: "admin", JumpID: bastion.ID}})
	if err != nil {
		t.Fatal(err)
	}
	// Sin contraseña del bastión: se pide, marcada como del salto.
	trust := func() (*ssh.Client, error) {
		for i := 0; i < 3; i++ {
			client, err := m.DialJump(rdp, map[string]JumpSecret{bastion.ID: {Secret: "pw-bastion"}})
			var ke *Error
			if errors.As(err, &ke) && ke.Code == CodeHostKeyUnknown && ke.Data["connId"] == bastion.ID {
				m.hostKeys.Trust(ke.Data["host"])
				continue
			}
			return client, err
		}
		return nil, errors.New("demasiados intentos")
	}
	if _, err := m.DialJump(rdp, nil); err == nil {
		t.Fatal("conectó al bastión sin contraseña")
	}
	via, err := trust()
	if err != nil {
		t.Fatal(err)
	}
	defer via.Close()
	c, err := via.Dial("tcp", net.JoinHostPort(rdp.Host, strconv.Itoa(rdp.Port)))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(c)
	if string(b) != "rdp" || tunnels.Load() != 1 {
		t.Fatalf("leído %q, túneles %d", b, tunnels.Load())
	}
}

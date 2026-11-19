package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"osat/internal/browser"
	"osat/internal/rdpwin"
	"osat/internal/remote"
	"osat/internal/store"
	"osat/internal/vpn"
)

// ---------- Escritorio remoto con el cliente de Windows (internal/rdpwin) ----------
//
// En Windows el escritorio remoto lo muestra el control de mstsc en una ventana hija sobre la
// pestaña (como los sitios web); en Linux y macOS sigue IronRDP (RdpOpen). El RD Gateway lo
// maneja el propio control; un salto SSH (Proxy Jump) o la VPN de osat se hacen con un puerto
// local que osat reenvía por el salto o por el túnel.

// rdpNativeState: el motor (se crea con el primer escritorio y no se destruye) y los
// reenvíos (salto SSH o VPN) de cada pestaña.
type rdpNativeState struct {
	mu    sync.Mutex
	eng   rdpwin.Engine
	jumps map[string]*jumpForward
}

// RdpNativeRequest: para abrir un escritorio con el cliente de Windows en la pestaña TabID.
// Bounds y Scale son el lugar de la pestaña (como en BrowserOpen).
type RdpNativeRequest struct {
	TabID        string                       `json:"tabId"`
	ConnectionID string                       `json:"connectionId"`
	Secret       string                       `json:"secret"`
	JumpSecrets  map[string]remote.JumpSecret `json:"jumpSecrets,omitempty"`
	Bounds       browser.Bounds               `json:"bounds"`
	Scale        float64                      `json:"scale"`
}

// RdpNativeSupported dice si el escritorio remoto va con el cliente de Windows.
func (a *App) RdpNativeSupported() bool { return rdpwin.Supported() }

func (a *App) rdpEngine() (rdpwin.Engine, error) {
	s := &a.rdpNative
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.eng == nil {
		eng, err := rdpwin.New(nil, func(ev rdpwin.Event) {
			if ev.Kind == "disconnected" {
				slog.Info("escritorio remoto: desconectado", "tab", ev.ID, "reason", ev.Reason)
			}
			if a.ctx != nil {
				nativeEventsEmit(a.ctx, "rdp:native", ev)
			}
		})
		if err != nil {
			return nil, err
		}
		s.eng = eng
	}
	return s.eng, nil
}

// RdpNativeOpen conecta la pestaña. Los resultados (conectado, sesión iniciada, desconectado)
// llegan por el evento "rdp:native".
func (a *App) RdpNativeOpen(req RdpNativeRequest) error {
	c, secret, err := a.rdpConnection(req.ConnectionID, req.Secret)
	if err != nil {
		return err
	}
	eng, err := a.rdpEngine()
	if err != nil {
		return err
	}
	a.closeJump(req.TabID) // al reconectar, el reenvío anterior ya no sirve
	cfg := rdpwin.Config{Server: c.Host, Port: c.Port, User: c.User, Domain: c.Domain, Password: secret}
	switch {
	case c.Gateway != "":
		host, port := store.GatewayAddr(c.Gateway)
		cfg.Gateway = host
		if port != 443 {
			cfg.Gateway = net.JoinHostPort(host, strconv.Itoa(port))
		}
		// Con un RD Gateway el control habla HTTPS con el gateway por su cuenta y
		// no se lo puede mandar por un puerto local (no le coincidiría el
		// certificado): esas conexiones no salen por la VPN de osat.
	case c.JumpID != "":
		client, err := a.mgr.DialJump(c, req.JumpSecrets) // el salto ya sale por la VPN, si está activada
		if err != nil {
			return err
		}
		fw, err := forwardThrough(func(ctx context.Context, network, addr string) (net.Conn, error) {
			return client.DialContext(ctx, network, addr)
		}, func() { client.Close() }, net.JoinHostPort(c.Host, strconv.Itoa(c.Port)))
		if err != nil {
			client.Close()
			return err
		}
		a.setNativeForward(req.TabID, fw)
		cfg.Server, cfg.Port = "127.0.0.1", fw.port
	case vpn.Enabled():
		// El control se conecta solo, sin pasar por osat: con la VPN activada
		// se conecta a un puerto local que osat lleva por el túnel.
		fw, err := forwardThrough(vpn.Dial, nil, net.JoinHostPort(c.Host, strconv.Itoa(c.Port)))
		if err != nil {
			return err
		}
		a.setNativeForward(req.TabID, fw)
		cfg.Server, cfg.Port = "127.0.0.1", fw.port
	}
	slog.Info("escritorio remoto: abriendo con el cliente de Windows", "conn", c.Name, "host", c.Host, "port", c.Port, "gateway", c.Gateway != "", "jump", c.JumpID != "", "vpn", c.Gateway == "" && c.JumpID == "" && vpn.Enabled())
	if err := eng.Open(req.TabID, cfg, req.Bounds, req.Scale); err != nil {
		a.closeJump(req.TabID)
		remote.LogError("escritorio remoto: no se pudo abrir", err, "conn", c.Name)
		return err
	}
	return nil
}

func (a *App) RdpNativeSetBounds(id string, b browser.Bounds, scale float64) {
	if eng := a.rdpNativeEngine(); eng != nil {
		eng.SetBounds(id, b, scale)
	}
}

func (a *App) RdpNativeSetVisible(id string, visible bool) {
	if eng := a.rdpNativeEngine(); eng != nil {
		eng.SetVisible(id, visible)
	}
}

// RdpNativeNotice muestra un aviso encima del escritorio: los de osat (HTML) quedarían tapados.
func (a *App) RdpNativeNotice(id, text string) {
	if eng := a.rdpNativeEngine(); eng != nil {
		eng.Notice(id, text)
	}
}

func (a *App) RdpNativeFocus(id string) {
	if eng := a.rdpNativeEngine(); eng != nil {
		eng.Focus(id)
	}
}

// RdpNativeClose desconecta la pestaña y cierra su reenvío por salto, si tiene.
func (a *App) RdpNativeClose(id string) {
	if eng := a.rdpNativeEngine(); eng != nil {
		eng.Close(id)
	}
	a.closeJump(id)
}

// rdpNativeEngine: el motor si ya se creó (nil si nunca se abrió un escritorio).
func (a *App) rdpNativeEngine() rdpwin.Engine {
	a.rdpNative.mu.Lock()
	defer a.rdpNative.mu.Unlock()
	return a.rdpNative.eng
}

func (a *App) closeJump(id string) {
	a.rdpNative.mu.Lock()
	fw := a.rdpNative.jumps[id]
	delete(a.rdpNative.jumps, id)
	a.rdpNative.mu.Unlock()
	if fw != nil {
		fw.Close()
	}
}

// closeRdpNative cierra todos los escritorios y reenvíos (al cerrar osat).
func (a *App) closeRdpNative() {
	if eng := a.rdpNativeEngine(); eng != nil {
		eng.CloseAll()
	}
	a.rdpNative.mu.Lock()
	jumps := a.rdpNative.jumps
	a.rdpNative.jumps = nil
	a.rdpNative.mu.Unlock()
	for _, fw := range jumps {
		fw.Close()
	}
}

func (a *App) setNativeForward(tabID string, fw *jumpForward) {
	a.rdpNative.mu.Lock()
	if a.rdpNative.jumps == nil {
		a.rdpNative.jumps = map[string]*jumpForward{}
	}
	a.rdpNative.jumps[tabID] = fw
	a.rdpNative.mu.Unlock()
}

// jumpForward es un puerto local (127.0.0.1) que lleva cada conexión al equipo
// por otro camino: un salto SSH o la VPN de osat.
type jumpForward struct {
	ln      net.Listener
	release func() // lo que se cierra con el reenvío (el cliente del salto); puede ser nil
	port    int
}

func forwardThrough(dial func(ctx context.Context, network, addr string) (net.Conn, error), release func(), target string) (*jumpForward, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	fw := &jumpForward{ln: ln, release: release, port: ln.Addr().(*net.TCPAddr).Port}
	go func() {
		for {
			local, err := ln.Accept()
			if err != nil {
				return // se cerró
			}
			go func() {
				defer local.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				remoteConn, err := dial(ctx, "tcp", target)
				cancel()
				if err != nil {
					slog.Warn("escritorio remoto: no se llegó al equipo", "target", target, "err", err)
					return
				}
				defer remoteConn.Close()
				go func() {
					io.Copy(remoteConn, local)
					remoteConn.Close()
				}()
				io.Copy(local, remoteConn)
			}()
		}
	}()
	return fw, nil
}

func (f *jumpForward) Close() {
	f.ln.Close()
	if f.release != nil {
		f.release()
	}
}

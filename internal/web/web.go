// Package web arma los sitios que muestra el navegador integrado: cada uno es
// un servidor HTTP en 127.0.0.1 (puerto al azar) que sirve una carpeta o hace
// de proxy hacia un puerto, y que se cierra junto con la pestaña que lo pidió.
package web

import (
	"context"
	"net"
	"net/http"
	"sync"

	"github.com/google/uuid"

	"osat/internal/i18n"
)

// Site es un sitio en marcha.
type Site struct {
	ID    string `json:"id"`
	URL   string `json:"url"` // http://127.0.0.1:<puerto>, sin barra final
	owner string
	srv   *http.Server
	done  func()
}

// Hub lleva la cuenta de los sitios abiertos, agrupados por quién los pidió.
type Hub struct {
	mu    sync.Mutex
	sites map[string]*Site
}

func NewHub() *Hub { return &Hub{sites: map[string]*Site{}} }

// Add abre un sitio atendido por h. done se llama una vez al cerrarlo (para
// soltar la conexión que usaba). Solo contesta si el pedido va dirigido a
// 127.0.0.1:<puerto>: así una página de otro origen que apunte su nombre a esa
// dirección (DNS rebinding) no puede leerlo.
func (hub *Hub) Add(owner string, h http.Handler, done func()) (*Site, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	host := ln.Addr().String()
	guarded := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != host {
			http.Error(w, "host no permitido", http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r)
	})
	s := &Site{ID: uuid.NewString(), URL: "http://" + host, owner: owner, srv: &http.Server{Handler: guarded}, done: done}
	go func() { _ = s.srv.Serve(ln) }()
	hub.mu.Lock()
	hub.sites[s.ID] = s
	hub.mu.Unlock()
	return s, nil
}

func (s *Site) close() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // sin espera: las conexiones abiertas (websockets del proxy) se cortan ya
	_ = s.srv.Shutdown(ctx)
	_ = s.srv.Close()
	if s.done != nil {
		s.done()
	}
}

// CloseOwner cierra los sitios que abrió owner (una pestaña).
func (hub *Hub) CloseOwner(owner string) {
	hub.mu.Lock()
	var out []*Site
	for id, s := range hub.sites {
		if s.owner == owner {
			out = append(out, s)
			delete(hub.sites, id)
		}
	}
	hub.mu.Unlock()
	for _, s := range out {
		s.close()
	}
}

func (hub *Hub) CloseAll() {
	hub.mu.Lock()
	out := make([]*Site, 0, len(hub.sites))
	for _, s := range hub.sites {
		out = append(out, s)
	}
	hub.sites = map[string]*Site{}
	hub.mu.Unlock()
	for _, s := range out {
		s.close()
	}
}

var errBadPort = i18n.Err("app.invalidPort")

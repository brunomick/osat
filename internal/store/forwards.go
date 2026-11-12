package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"

	"osat/internal/i18n"
)

// Forward es un reenvío de puerto guardado: un puerto de esta PC que llega a un
// puerto al que se accede desde el servidor (o a un pod/servicio de Kubernetes,
// con el kubectl del servidor). Se guardan aparte de las conexiones, en forwards.json.
//
// Con Kind "remote" es al revés (el -R de ssh): un puerto del servidor (Port)
// que llega a Host:LocalPort de esta PC. Con Kind "dynamic" (el -D) es un
// proxy SOCKS en LocalPort y no hay destino fijo.
type Forward struct {
	ID        string `json:"id"`
	ConnID    string `json:"connId"`
	Name      string `json:"name"`
	Kind      string `json:"kind,omitempty"` // "" (local), "remote" o "dynamic"
	LocalPort int    `json:"localPort"`      // en esta PC (solo loopback)
	// Puerto del servidor: Host (por defecto "localhost") y Port, vistos desde el servidor.
	Host string `json:"host,omitempty"`
	Port int    `json:"port"`
	// Solo remoto: el servidor escucha en todas sus interfaces y no solo en su localhost.
	BindAll bool `json:"bindAll,omitempty"`
	// Kubernetes: Namespace y Resource ("pod/x", "svc/x" o "deployment/x"); Port es el del pod o servicio.
	Namespace string `json:"namespace,omitempty"`
	Resource  string `json:"resource,omitempty"`
	// Arranca solo al abrir la pestaña de puertos de la conexión.
	AutoStart bool `json:"autoStart,omitempty"`
}

// Tipos de reenvío (Forward.Kind). Kubernetes es local y se deduce de Resource.
const (
	ForwardLocal   = ""
	ForwardRemote  = "remote"
	ForwardDynamic = "dynamic"
)

// IsKubernetes dice si el reenvío pasa por kubectl.
func (f Forward) IsKubernetes() bool { return f.Resource != "" }

var (
	// Nombres que acepta Kubernetes (DNS-1123). Lo que se guarda termina en un
	// comando del servidor, así que es estricto: nada de espacios ni comillas.
	k8sNamespace = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	k8sResource  = regexp.MustCompile(`^(pod|svc|deployment)/[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)
	hostName     = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._:-]*[A-Za-z0-9])?$`)
)

// Validate normaliza y comprueba el reenvío.
func (f *Forward) Validate() error {
	f.Name = strings.TrimSpace(f.Name)
	f.Host = strings.TrimSpace(f.Host)
	f.Namespace = strings.TrimSpace(f.Namespace)
	f.Resource = strings.TrimSpace(f.Resource)
	if f.ConnID == "" {
		return i18n.Err("data.theConnectionIsMissing")
	}
	if f.LocalPort < 1 || f.LocalPort > 65535 {
		return i18n.Err("data.invalidPortOnThisPc")
	}
	switch f.Kind {
	case ForwardLocal:
	case ForwardRemote:
		if f.Resource != "" || f.Namespace != "" {
			return i18n.Err("data.aRemoteForwardCanT")
		}
	case ForwardDynamic: // solo el puerto de esta PC: el destino lo pide cada programa
		f.Host, f.Port, f.Namespace, f.Resource, f.BindAll = "", 0, "", "", false
		if f.Name == "" {
			f.Name = fmt.Sprintf("SOCKS %d", f.LocalPort)
		}
		return nil
	default:
		return i18n.Err("data.unknownForwardTypeQ", f.Kind)
	}
	if f.Kind != ForwardRemote {
		f.BindAll = false
	}
	if f.Port < 1 || f.Port > 65535 {
		return i18n.Err("data.invalidServerPortTo")
	}
	if f.IsKubernetes() {
		if !k8sResource.MatchString(f.Resource) || len(f.Resource) > 253 {
			return i18n.Err("data.invalidKubernetesResourceQ", f.Resource)
		}
		if !k8sNamespace.MatchString(f.Namespace) || len(f.Namespace) > 63 {
			return i18n.Err("remote.invalidNamespaceQ", f.Namespace)
		}
		f.Host = ""
	} else {
		if f.Host == "" {
			f.Host = "localhost"
		}
		if net.ParseIP(f.Host) == nil && !hostName.MatchString(f.Host) {
			return i18n.Err("app.invalidHostQ", f.Host)
		}
		f.Namespace = ""
	}
	if f.Name == "" {
		f.Name = fmt.Sprintf("Puerto %d", f.Port)
	}
	return nil
}

func (s *Store) forwardsPath() string { return filepath.Join(s.dir, "forwards.json") }

type forwardsFile struct {
	Version  int       `json:"version"`
	Forwards []Forward `json:"forwards"`
}

func (s *Store) loadForwards() error {
	b, err := os.ReadFile(s.forwardsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var f forwardsFile
	if err := json.Unmarshal(b, &f); err != nil {
		return i18n.Err("data.invalidForwardsJsonV", err)
	}
	s.forwards = map[string]Forward{}
	for _, fw := range f.Forwards {
		s.forwards[fw.ID] = fw
	}
	return nil
}

func (s *Store) saveForwardsLocked() error {
	out := forwardsFile{Version: 1, Forwards: s.forwardsLocked("")}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.forwardsPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.forwardsPath())
}

// forwardsLocked devuelve los de una conexión (o todos si connID es ""), ordenados por nombre.
func (s *Store) forwardsLocked(connID string) []Forward {
	out := make([]Forward, 0, len(s.forwards))
	for _, f := range s.forwards {
		if connID == "" || f.ConnID == connID {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name); a != b {
			return a < b
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Forwards son los reenvíos guardados de una conexión.
func (s *Store) Forwards(connID string) []Forward {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.forwardsLocked(connID)
}

func (s *Store) GetForward(id string) (Forward, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f, ok := s.forwards[id]
	return f, ok
}

// SaveForward crea (ID vacío) o actualiza un reenvío.
func (s *Store) SaveForward(f Forward) (Forward, error) {
	if err := f.Validate(); err != nil {
		return Forward{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.conns[f.ConnID]; !ok {
		return Forward{}, i18n.Err("app.connectionDoesNotExist")
	}
	if f.ID == "" {
		f.ID = uuid.NewString()
	} else if prev, ok := s.forwards[f.ID]; !ok || prev.ConnID != f.ConnID {
		return Forward{}, i18n.Err("data.forwardDoesNotExist")
	}
	if s.forwards == nil {
		s.forwards = map[string]Forward{}
	}
	s.forwards[f.ID] = f
	return f, s.saveForwardsLocked()
}

func (s *Store) DeleteForward(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.forwards[id]; !ok {
		return nil
	}
	delete(s.forwards, id)
	return s.saveForwardsLocked()
}

// dropForwardsLocked borra los reenvíos de una conexión que se elimina.
func (s *Store) dropForwardsLocked(connID string) error {
	n := len(s.forwards)
	for id, f := range s.forwards {
		if f.ConnID == connID {
			delete(s.forwards, id)
		}
	}
	if len(s.forwards) == n {
		return nil
	}
	return s.saveForwardsLocked()
}

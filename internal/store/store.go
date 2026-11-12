// Package store persiste las conexiones guardadas en un JSON dentro del
// directorio de configuración del usuario. Los secretos (contraseñas y
// passphrases) NUNCA se escriben en el JSON: van al keyring del sistema
// operativo (Windows Credential Manager, macOS Keychain, Secret Service).
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/zalando/go-keyring"

	"osat/internal/i18n"
)

const keyringService = "osat"

// Nombres anteriores de la app (se llamaba Kairos): se migran al arrancar.
const (
	legacyDirName        = "Kairos"
	legacyKeyringService = "kairos"
)

type AuthType string

const (
	AuthPassword AuthType = "password"
	AuthKey      AuthType = "key"
	AuthAgent    AuthType = "agent"
)

// Protocolos de conexión.
const (
	ProtoSSH = "ssh"
	ProtoRDP = "rdp"
	ProtoVNC = "vnc"
)

// Connection es un servidor guardado.
type Connection struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Group     string   `json:"group"`
	Protocol  string   `json:"protocol,omitempty"` // "" o "ssh" = SSH/SFTP; "rdp" o "vnc" = escritorio remoto
	Host      string   `json:"host"`
	Port      int      `json:"port"`
	User      string   `json:"user"`
	Domain    string   `json:"domain,omitempty"`  // solo RDP
	Gateway   string   `json:"gateway,omitempty"` // solo RDP: RD Gateway, "host" o "host:puerto"
	AuthType  AuthType `json:"authType"`
	KeyPath   string   `json:"keyPath,omitempty"`
	JumpID    string   `json:"jumpId,omitempty"` // conexión SSH a través de la cual se llega (ProxyJump)
	Color     string   `json:"color,omitempty"`
	Order     int      `json:"order,omitempty"` // lugar dentro de su grupo (lo maneja el store; a igual Order, por nombre)
	HasSecret bool     `json:"hasSecret"`
	CreatedAt int64    `json:"createdAt"`
	UpdatedAt int64    `json:"updatedAt"`

	// Usa el usuario y la contraseña por defecto (ver defaults.go) en vez de los suyos.
	UseDefaultCreds bool `json:"useDefaultCreds,omitempty"`

	// Solo SSH.
	ForwardAgent   bool     `json:"forwardAgent,omitempty"`
	InitialCommand string   `json:"initialCommand,omitempty"` // una línea, se escribe en el shell al abrir la terminal
	Env            []string `json:"env,omitempty"`            // "NOMBRE=valor"

	// Barra lateral. Save no los pisa (el formulario no los manda): se cambian con PinDir, SetFavorite y compañía.
	Pins     []PinnedDir `json:"pins,omitempty"` // carpetas fijadas del servidor
	Favorite bool        `json:"favorite,omitempty"`
	FavOrder int         `json:"favOrder,omitempty"` // lugar en Favoritos
}

// PinnedDir es una carpeta del servidor fijada en la barra lateral.
type PinnedDir struct {
	Path string `json:"path"`
	Name string `json:"name,omitempty"` // vacío = se muestra la ruta
}

// ConnectionInput es lo que envía el frontend al crear/editar.
// Secret vacío + KeepSecret=true conserva el secreto existente.
type ConnectionInput struct {
	Connection
	Secret     string `json:"secret"`
	KeepSecret bool   `json:"keepSecret"`
}

type fileFormat struct {
	Version     int          `json:"version"`
	Connections []Connection `json:"connections"`
	// Groups son todos los grupos (rutas "A/B"), en el orden en que se
	// muestran; así siguen existiendo aunque no tengan servidores.
	Groups []string `json:"groups,omitempty"`
}

type Store struct {
	mu     sync.RWMutex
	dir    string
	path   string
	conns  map[string]Connection
	groups []string // en orden (ver groups.go)

	forwards map[string]Forward // ver forwards.go
	snippets map[string]Snippet // ver snippets.go
	sites    map[string]Site    // ver sites.go
	defaults DefaultCreds       // ver defaults.go

	workspaces    map[string]Workspace // ver workspaces.go
	lastWorkspace json.RawMessage
}

// ConfigDir devuelve (y crea) el directorio de configuración de osat.
func ConfigDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "osat")
	migrateLegacyDir(filepath.Join(base, legacyDirName), dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// migrateLegacyDir mueve los datos de la versión anterior (connections.json,
// known_hosts) si existen y la carpeta nueva todavía no. Si falla, se arranca
// con la carpeta nueva vacía y la vieja queda intacta.
func migrateLegacyDir(old, dir string) {
	if _, err := os.Stat(dir); err == nil {
		return
	}
	if fi, err := os.Stat(old); err == nil && fi.IsDir() {
		_ = os.Rename(old, dir)
	}
}

func New() (*Store, error) {
	dir, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	s := &Store{dir: dir, path: filepath.Join(dir, "connections.json"), conns: map[string]Connection{}}
	if err := s.load(); err != nil {
		return nil, err
	}
	if err := s.loadForwards(); err != nil {
		return nil, err
	}
	if err := s.loadSnippets(); err != nil {
		return nil, err
	}
	if err := s.loadSites(); err != nil {
		return nil, err
	}
	if err := s.loadDefaults(); err != nil {
		return nil, err
	}
	s.loadWorkspaces() // un workspaces.json dañado no frena el arranque
	return s, nil
}

func (s *Store) load() error {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var f fileFormat
	if err := json.Unmarshal(b, &f); err != nil {
		return i18n.Err("data.invalidConnectionsJsonV", err)
	}
	// Primero los grupos, en su orden guardado; los de servidores que no
	// estén en la lista (archivos anteriores) quedan después.
	for _, g := range f.Groups {
		s.keepGroup(NormalizeGroup(g))
	}
	for _, c := range f.Connections {
		s.conns[c.ID] = c
		s.keepGroup(c.Group)
	}
	return nil
}

func (s *Store) saveLocked() error {
	f := fileFormat{Version: 1, Connections: s.listLocked(), Groups: s.groupsLocked()}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) listLocked() []Connection {
	out := make([]Connection, 0, len(s.conns))
	for _, c := range s.conns {
		out = append(out, c)
	}
	// Por el orden de su grupo y, dentro del grupo, por Order y nombre.
	pos := map[string]int{"": -1} // sin grupo: antes que todos
	for i, g := range s.groups {
		pos[g] = i
	}
	sort.Slice(out, func(i, j int) bool {
		if pi, pj := pos[out[i].Group], pos[out[j].Group]; pi != pj {
			return pi < pj
		}
		if out[i].Group != out[j].Group { // grupos fuera de la lista (no debería pasar)
			return out[i].Group < out[j].Group
		}
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func (s *Store) List() []Connection {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.listLocked()
}

func (s *Store) Get(id string) (Connection, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.conns[id]
	return c, ok
}

// envName: nombre válido de variable de entorno.
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// cleanEnv descarta las entradas vacías y verifica que el resto sea NOMBRE=valor.
func cleanEnv(env []string) ([]string, error) {
	var out []string
	for _, e := range env {
		if strings.TrimSpace(e) == "" {
			continue
		}
		name, _, ok := strings.Cut(e, "=")
		if !ok || !envName.MatchString(name) {
			return nil, i18n.Err("data.invalidEnvironmentVariableQUse", e)
		}
		out = append(out, e)
	}
	return out, nil
}

func validate(c *Connection) error {
	c.Name = strings.TrimSpace(c.Name)
	c.Host = strings.TrimSpace(c.Host)
	c.User = strings.TrimSpace(c.User)
	c.Group = NormalizeGroup(c.Group)
	c.Domain = strings.TrimSpace(c.Domain)
	c.Gateway = strings.TrimSpace(c.Gateway)
	if c.Host == "" {
		return i18n.Err("data.theHostIsRequired")
	}
	switch c.Protocol {
	case ProtoVNC:
		if c.Port == 0 {
			c.Port = 5900
		}
		c.User, c.Domain, c.Gateway = "", "", ""
		c.AuthType = AuthPassword // VNC: solo contraseña (o ninguna)
		c.KeyPath = ""
		c.ForwardAgent, c.InitialCommand, c.Env = false, "", nil
	case ProtoRDP:
		if c.Port == 0 {
			c.Port = 3389
		}
		if c.Gateway != "" && c.JumpID != "" {
			return i18n.Err("data.gatewayAndJump")
		}
		c.AuthType = AuthPassword // RDP: usuario + contraseña (el control maneja NLA)
		c.KeyPath = ""
		c.ForwardAgent, c.InitialCommand, c.Env = false, "", nil
	default:
		c.Protocol = ProtoSSH
		c.Domain, c.Gateway = "", ""
		if c.UseDefaultCreds { // la por defecto es una contraseña
			c.AuthType, c.KeyPath = AuthPassword, ""
		}
		if c.Port == 0 {
			c.Port = 22
		}
		if strings.ContainsAny(c.InitialCommand, "\r\n") {
			return i18n.Err("data.theInitialCommandMustBe")
		}
		c.InitialCommand = strings.TrimSpace(c.InitialCommand)
		env, err := cleanEnv(c.Env)
		if err != nil {
			return err
		}
		c.Env = env
	}
	if c.Port < 1 || c.Port > 65535 {
		return i18n.Err("app.invalidPort")
	}
	if c.Name == "" {
		if c.User == "" { // VNC
			c.Name = c.Host
		} else {
			c.Name = fmt.Sprintf("%s@%s", c.User, c.Host)
		}
	}
	switch c.AuthType {
	case AuthPassword, AuthAgent:
	case AuthKey:
		if strings.TrimSpace(c.KeyPath) == "" {
			return i18n.Err("data.enterThePathOfThe")
		}
	default:
		c.AuthType = AuthPassword
	}
	return nil
}

// Save crea o actualiza una conexión y gestiona su secreto en el keyring.
func (s *Store) Save(in ConnectionInput) (Connection, error) {
	c := in.Connection
	if err := validate(&c); err != nil {
		return Connection{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Sin usuario propio vale si hay uno por defecto (VNC solo tiene contraseña).
	if c.User == "" && c.Protocol != ProtoVNC && s.defaults.User == "" {
		return Connection{}, i18n.Err("data.theUsernameIsRequired")
	}
	if err := s.checkJumpLocked(c); err != nil {
		return Connection{}, err
	}

	now := time.Now().Unix()
	prev, exists := s.conns[c.ID]
	if c.ID == "" {
		c.ID = uuid.NewString()
		c.CreatedAt = now
	} else if exists {
		c.CreatedAt = prev.CreatedAt
		c.HasSecret = prev.HasSecret
	}
	// Carpetas fijadas y favoritos: las existentes conservan los suyos; las nuevas
	// traen solo Pins (el .osat) y nunca nacen favoritas.
	if exists {
		c.Pins, c.Favorite, c.FavOrder = prev.Pins, prev.Favorite, prev.FavOrder
	} else {
		c.Favorite, c.FavOrder = false, 0
	}
	// Las nuevas (y las que cambian de grupo) van al final de su grupo.
	if exists && prev.Group == c.Group {
		c.Order = prev.Order
	} else {
		c.Order = s.nextOrderLocked(c.Group)
	}
	c.UpdatedAt = now

	switch {
	case in.Secret != "":
		if err := secretSet(keyringService, c.ID, in.Secret); err != nil {
			return Connection{}, i18n.Err("data.couldnTSaveTheSecret", err)
		}
		c.HasSecret = true
	case !in.KeepSecret || c.AuthType == AuthAgent:
		deleteSecret(keyringService, c.ID)
		deleteSecret(legacyKeyringService, c.ID)
		c.HasSecret = false
	}

	s.conns[c.ID] = c
	s.keepGroup(c.Group)
	return c, s.saveLocked()
}

// MaxJumps es el máximo de saltos encadenados hasta llegar a una conexión.
const MaxJumps = 8

// checkJumpLocked: el salto de c existe, es SSH y no forma un ciclo.
func (s *Store) checkJumpLocked(c Connection) error {
	for id, n := c.JumpID, 0; id != ""; n++ {
		j, ok := s.conns[id]
		switch {
		case !ok:
			return i18n.Err("data.theJumpHostConnectionDoes")
		case id == c.ID:
			return i18n.Err("data.theConnectionCanTJump")
		case j.Protocol == ProtoRDP || j.Protocol == ProtoVNC:
			return i18n.Err("data.youCanOnlyJumpThrough")
		case n >= MaxJumps:
			return i18n.Err("data.tooManyChainedJumpHosts", MaxJumps)
		}
		id = j.JumpID
	}
	return nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		if c.JumpID == id {
			return i18n.Err("data.sJumpsThroughThisConnection", c.Name)
		}
	}
	delete(s.conns, id)
	deleteSecret(keyringService, id)
	deleteSecret(legacyKeyringService, id)
	if err := s.dropForwardsLocked(id); err != nil {
		return err
	}
	return s.saveLocked()
}

// Secret lee el secreto de una conexión desde el keyring ("" si no hay).
// Si solo existe con el nombre anterior de la app, lo migra.
func (s *Store) Secret(id string) string {
	v, err := secretGet(keyringService, id)
	if err == nil {
		return v
	}
	if !errors.Is(err, keyring.ErrNotFound) {
		slog.Warn("no se pudo leer del almacén de credenciales", "service", keyringService, "id", id, "err", err)
	}
	v, err = secretGet(legacyKeyringService, id)
	if err != nil {
		if !errors.Is(err, keyring.ErrNotFound) {
			slog.Warn("no se pudo leer del almacén de credenciales", "service", legacyKeyringService, "id", id, "err", err)
		}
		return ""
	}
	if err := secretSet(keyringService, id, v); err != nil {
		slog.Warn("no se pudo migrar la credencial al servicio nuevo", "id", id, "err", err)
	} else {
		deleteSecret(legacyKeyringService, id)
	}
	return v
}

// deleteSecret borra una credencial del keyring. Un error no frena a quien llama,
// pero se registra; que no exista (lo normal con el servicio anterior) no se registra.
func deleteSecret(service, id string) {
	if err := secretDelete(service, id); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		slog.Warn("no se pudo borrar del almacén de credenciales", "service", service, "id", id, "err", err)
	}
}

// SetSecret guarda un secreto (por ejemplo tras pedirlo al conectar).
func (s *Store) SetSecret(id, secret string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.conns[id]
	if !ok {
		return i18n.Err("app.connectionDoesNotExist")
	}
	if err := secretSet(keyringService, id, secret); err != nil {
		return err
	}
	c.HasSecret = true
	s.conns[id] = c
	return s.saveLocked()
}

func (s *Store) Dir() string { return s.dir }

// GatewayAddr separa el RD Gateway de una conexión en host y puerto (443 si no lo indica).
func GatewayAddr(gw string) (string, int) {
	if h, p, err := net.SplitHostPort(gw); err == nil {
		if n, err := strconv.Atoi(p); err == nil {
			return h, n
		}
	}
	return gw, 443
}

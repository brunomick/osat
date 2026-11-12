// Package osatfile arma y lee los archivos .osat: conexiones exportadas de un
// osat para importarlas en otro. El contenido va siempre cifrado con una
// contraseña (Argon2id + AES-256-GCM) porque puede llevar contraseñas y claves
// privadas completas.
package osatfile

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/argon2"

	"osat/internal/i18n"
	"osat/internal/store"
)

// Entry es una conexión exportada.
type Entry struct {
	Name     string         `json:"name"`
	Group    string         `json:"group"`
	Protocol string         `json:"protocol"`
	Host     string         `json:"host"`
	Port     int            `json:"port"`
	User     string         `json:"user"`
	Domain   string         `json:"domain,omitempty"`
	Gateway  string         `json:"gateway,omitempty"`
	AuthType store.AuthType `json:"authType"`
	KeyPath  string         `json:"keyPath,omitempty"` // la ruta en la PC que exportó (sin la clave adentro)
	Jump     string         `json:"jump,omitempty"`    // nombre de la conexión de salto (ProxyJump)
	Color    string         `json:"color,omitempty"`
	// Usa las credenciales por defecto del osat que importa.
	UseDefaultCreds bool `json:"useDefaultCreds,omitempty"`
	// Solo SSH.
	ForwardAgent   bool              `json:"forwardAgent,omitempty"`
	InitialCommand string            `json:"initialCommand,omitempty"`
	Env            []string          `json:"env,omitempty"`
	Pins           []store.PinnedDir `json:"pins,omitempty"`
	// Solo si se exportó con contraseñas y claves.
	Secret  string `json:"secret,omitempty"`  // contraseña o passphrase de la clave
	KeyName string `json:"keyName,omitempty"` // nombre del archivo de la clave (id_ed25519, server.ppk…)
	Key     []byte `json:"key,omitempty"`     // contenido completo de la clave privada
}

// SnippetEntry es un snippet exportado. Group vacío = global.
type SnippetEntry struct {
	Name    string `json:"name"`
	Folder  string `json:"folder,omitempty"`
	Command string `json:"command"`
	Mode    string `json:"mode"`
	Group   string `json:"group,omitempty"`
}

// DefaultsEntry son las credenciales por defecto exportadas (solo si se eligió incluirlas).
type DefaultsEntry struct {
	User   string `json:"user"`
	Domain string `json:"domain,omitempty"`
	Secret string `json:"secret,omitempty"`
}

// SiteEntry es un sitio web exportado.
type SiteEntry struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

const (
	magic   = "osat-connections"
	version = 1
)

// Parámetros de Argon2id (los recomendados por la RFC 9106 para poca memoria).
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 4
)

type envelope struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	KDF     string `json:"kdf"`
	Time    uint32 `json:"time"`
	Memory  uint32 `json:"memory"`
	Threads uint8  `json:"threads"`
	Salt    []byte `json:"salt"`
	Nonce   []byte `json:"nonce"`
	Data    []byte `json:"data"`
}

type payload struct {
	Connections []Entry        `json:"connections"`
	Snippets    []SnippetEntry `json:"snippets,omitempty"` // los archivos anteriores no lo tienen
	Sites       []SiteEntry    `json:"sites,omitempty"`    // ídem
	Defaults    *DefaultsEntry `json:"defaults,omitempty"` // ídem
}

// ErrBadPassword: la contraseña no es la del archivo (o el archivo se alteró).
var ErrBadPassword = i18n.Err("data.wrongPasswordOrDamagedFile")

func deriveKey(password string, salt []byte, t, m uint32, p uint8) []byte {
	return argon2.IDKey([]byte(password), salt, t, m, p, 32)
}

func gcm(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Seal cifra las conexiones, los snippets, los sitios y las credenciales por defecto (nil = no van) con la contraseña y devuelve el archivo .osat.
func Seal(entries []Entry, snippets []SnippetEntry, sites []SiteEntry, defaults *DefaultsEntry, password string) ([]byte, error) {
	if password == "" {
		return nil, i18n.Err("app.thePasswordIsRequired")
	}
	plain, err := json.Marshal(payload{Connections: entries, Snippets: snippets, Sites: sites, Defaults: defaults})
	if err != nil {
		return nil, err
	}
	env := envelope{Format: magic, Version: version, KDF: "argon2id", Time: argonTime, Memory: argonMemory, Threads: argonThreads,
		Salt: make([]byte, 16), Nonce: make([]byte, 12)}
	if _, err := rand.Read(env.Salt); err != nil {
		return nil, err
	}
	if _, err := rand.Read(env.Nonce); err != nil {
		return nil, err
	}
	aead, err := gcm(deriveKey(password, env.Salt, env.Time, env.Memory, env.Threads))
	if err != nil {
		return nil, err
	}
	env.Data = aead.Seal(nil, env.Nonce, plain, nil)
	return json.MarshalIndent(env, "", "  ")
}

// Open descifra un archivo .osat.
func Open(data []byte, password string) ([]Entry, []SnippetEntry, []SiteEntry, *DefaultsEntry, error) {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil || env.Format != magic {
		return nil, nil, nil, nil, i18n.Err("data.itIsNotAnOsat")
	}
	if env.Version != version || env.KDF != "argon2id" {
		return nil, nil, nil, nil, i18n.Err("data.theFileIsFromA", env.Version)
	}
	// Un archivo armado a mano no puede pedir gigas de memoria ni minutos de CPU.
	if env.Memory > 1024*1024 || env.Time > 16 || env.Threads == 0 || len(env.Nonce) != 12 {
		return nil, nil, nil, nil, i18n.Err("data.invalidConnectionsFile")
	}
	aead, err := gcm(deriveKey(password, env.Salt, env.Time, env.Memory, env.Threads))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	plain, err := aead.Open(nil, env.Nonce, env.Data, nil)
	if err != nil {
		return nil, nil, nil, nil, ErrBadPassword
	}
	var p payload
	if err := json.Unmarshal(plain, &p); err != nil {
		return nil, nil, nil, nil, i18n.Err("data.invalidConnectionsFileV", err)
	}
	return p.Connections, p.Snippets, p.Sites, p.Defaults, nil
}

// SaveKey guarda una clave importada en dir y devuelve su ruta. El nombre
// lleva un pedazo del hash del contenido: importar dos veces la misma clave
// reusa el archivo y dos claves distintas con el mismo nombre no se pisan.
func SaveKey(dir, name string, key []byte) (string, error) {
	base := filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	if base == "." || base == "/" || base == "" {
		base = "clave"
	}
	ext := filepath.Ext(base)
	sum := sha256.Sum256(key)
	p := filepath.Join(dir, strings.TrimSuffix(base, ext)+"-"+hex.EncodeToString(sum[:4])+ext)
	if b, err := os.ReadFile(p); err == nil && bytes.Equal(b, key) {
		return p, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(p, key, 0o600); err != nil {
		return "", err
	}
	return p, nil
}

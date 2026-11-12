// Package importer lee las sesiones guardadas de PuTTY, WinSCP, mRemoteNG y
// el config de OpenSSH (openssh.go) para darlas de alta como conexiones de osat, con las contraseñas que
// guardan WinSCP y mRemoteNG (secrets.go; PuTTY no guarda contraseñas) y la
// ruta de las claves privadas.
package importer

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"osat/internal/i18n"
	"osat/internal/store"
)

// Herramientas de las que se puede importar.
const (
	PuTTY     = "putty"
	WinSCP    = "winscp"
	MRemoteNG = "mremoteng"
)

// Dónde guardan sus sesiones (registro de Windows, debajo de HKCU).
const (
	puttyRegKey   = `Software\SimonTatham\PuTTY\Sessions`
	winscpRegKey  = `Software\Martin Prikryl\WinSCP 2\Sessions`
	winscpConfKey = `Software\Martin Prikryl\WinSCP 2\Configuration` // Security › UseMasterPassword
)

// Item es una sesión encontrada, ya traducida a una conexión de osat.
type Item struct {
	Name     string         `json:"name"`
	Group    string         `json:"group"`
	Protocol string         `json:"protocol"` // store.ProtoSSH o store.ProtoRDP
	Host     string         `json:"host"`
	Port     int            `json:"port"`
	User     string         `json:"user"`
	Domain   string         `json:"domain,omitempty"`
	Gateway  string         `json:"gateway,omitempty"`
	AuthType store.AuthType `json:"authType"`
	KeyPath  string         `json:"keyPath,omitempty"`
	// Solo SSH.
	ForwardAgent   bool     `json:"forwardAgent,omitempty"`
	InitialCommand string   `json:"initialCommand,omitempty"`
	Env            []string `json:"env,omitempty"`
	// Nombre de la conexión a través de la cual se llega (ProxyJump): al
	// importar se busca entre las importadas y, si no, entre las existentes.
	Jump string `json:"jump,omitempty"`
	// Ya hay una conexión con el mismo protocolo, host, puerto y usuario.
	Exists bool `json:"exists"`
	// Posición en el resultado (en un .osat, en el archivo) y si trae
	// contraseña y clave. La contraseña no viaja al frontend: el backend la
	// guarda hasta importar.
	Ref       int    `json:"ref"`
	HasSecret bool   `json:"hasSecret,omitempty"`
	HasKey    bool   `json:"hasKey,omitempty"`
	Secret    string `json:"-"`
}

// Result es lo encontrado en una fuente.
type Result struct {
	Source   string `json:"source"`
	Location string `json:"location"` // de dónde se leyó (registro o archivo)
	Items    []Item `json:"items"`
	// Sesiones que no se pueden importar, con el motivo (otro protocolo, sin host…).
	Skipped []string `json:"skipped"`
	// La herramienta no tiene sesiones en la ubicación habitual de este equipo
	// (se puede elegir un archivo exportado).
	NotFound bool `json:"notFound"`
	// Cuántos snippets trae el archivo (solo en un .osat).
	Snippets int `json:"snippets"`
	// Cuántos sitios web trae el archivo (solo en un .osat).
	Sites int `json:"sites"`
	// El archivo trae credenciales por defecto, y su usuario (solo en un .osat).
	HasDefaults  bool   `json:"hasDefaults,omitempty"`
	DefaultsUser string `json:"defaultsUser,omitempty"`
}

// ErrNotFound: la herramienta no tiene sesiones guardadas en este equipo.
var ErrNotFound = i18n.Err("data.noSavedSessionsWereFound")

// Scan lee las sesiones de la herramienta indicada. path vacío = la ubicación
// habitual en este equipo; si no, un archivo exportado (.reg de PuTTY,
// WinSCP.ini, confCons.xml de mRemoteNG o config de OpenSSH). master es la contraseña maestra de
// mRemoteNG o WinSCP, si tienen una propia (si falta, ErrMasterPassword).
// existing se usa para marcar las que ya están cargadas.
func Scan(source, path, master string, existing []store.Connection) (Result, error) {
	var (
		r   Result
		err error
	)
	switch source {
	case PuTTY:
		r, err = scanPuTTY(path)
	case WinSCP:
		r, err = scanWinSCP(path, master)
	case MRemoteNG:
		r, err = scanMRemoteNG(path, master)
	case OpenSSH:
		r, err = scanOpenSSH(path)
	default:
		return Result{}, i18n.Err("data.unknownImportSourceQ", source)
	}
	if errors.Is(err, ErrNotFound) {
		return Result{Source: source, Items: []Item{}, Skipped: []string{}, NotFound: true}, nil
	}
	if err != nil {
		return Result{}, err
	}
	r.Source = source
	if r.Items == nil {
		r.Items = []Item{}
	}
	if r.Skipped == nil {
		r.Skipped = []string{}
	}
	markExisting(r.Items, existing)
	sort.SliceStable(r.Items, func(i, j int) bool {
		a, b := r.Items[i], r.Items[j]
		if !strings.EqualFold(a.Group, b.Group) {
			return strings.ToLower(a.Group) < strings.ToLower(b.Group)
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	for i := range r.Items {
		it := &r.Items[i]
		it.Ref, it.HasSecret, it.HasKey = i, it.Secret != "", it.KeyPath != ""
	}
	sort.Strings(r.Skipped)
	return r, nil
}

func markExisting(items []Item, existing []store.Connection) {
	key := func(proto, host string, port int, user string) string {
		if proto == "" {
			proto = store.ProtoSSH
		}
		return fmt.Sprintf("%s|%s|%d|%s", proto, strings.ToLower(host), port, user)
	}
	have := map[string]bool{}
	for _, c := range existing {
		have[key(c.Protocol, c.Host, c.Port, c.User)] = true
	}
	for i := range items {
		it := &items[i]
		it.Exists = have[key(it.Protocol, it.Host, it.Port, it.User)]
	}
}

// ---------- PuTTY ----------

func scanPuTTY(path string) (Result, error) {
	var (
		raw map[string]map[string]string
		loc string
		err error
	)
	if path == "" {
		loc = `HKEY_CURRENT_USER\` + puttyRegKey
		raw, err = readRegistry(puttyRegKey)
	} else {
		loc = path
		raw, err = readRegFile(path, puttyRegKey)
	}
	if err != nil {
		return Result{}, err
	}
	items, skipped := puttyItems(raw)
	return Result{Location: loc, Items: items, Skipped: skipped}, nil
}

func puttyItems(raw map[string]map[string]string) ([]Item, []string) {
	var items []Item
	var skipped []string
	for key, v := range raw {
		name := unescape(key)
		host := strings.TrimSpace(v["HostName"])
		if host == "" {
			if name != "Default Settings" {
				skipped = append(skipped, name+": no tiene host")
			}
			continue
		}
		if p := v["Protocol"]; p != "" && p != "ssh" {
			skipped = append(skipped, fmt.Sprintf("%s: protocolo %s (osat solo importa SSH)", name, p))
			continue
		}
		user := v["UserName"]
		if at := strings.LastIndex(host, "@"); at >= 0 { // "usuario@host" en el campo host
			if user == "" {
				user = host[:at]
			}
			host = host[at+1:]
		}
		it := Item{
			Name:     name,
			Group:    "PuTTY", // PuTTY no tiene carpetas
			Protocol: store.ProtoSSH,
			Host:     host,
			Port:     atoi(v["PortNumber"], 22),
			User:     user,
			AuthType: store.AuthPassword,
		}
		if k := v["PublicKeyFile"]; k != "" {
			it.AuthType, it.KeyPath = store.AuthKey, k
		}
		items = append(items, it)
	}
	return items, skipped
}

// ---------- WinSCP ----------

// winscpResult arma el resultado. Si WinSCP usa contraseña maestra
// (usesMaster), sus contraseñas se descifran con master; si falta y hay
// alguna contraseña guardada, ErrMasterPassword.
func winscpResult(loc string, raw map[string]map[string]string, usesMaster bool, master string) (Result, error) {
	if usesMaster && master == "" {
		for _, v := range raw {
			if v["Password"] != "" {
				return Result{}, ErrMasterPassword
			}
		}
	}
	items, skipped, err := winscpItems(raw, usesMaster, master)
	if err != nil {
		return Result{}, err
	}
	return Result{Location: loc, Items: items, Skipped: skipped}, nil
}

func scanWinSCP(path, master string) (Result, error) {
	if path == "" {
		raw, err := readRegistry(winscpRegKey)
		if err == nil {
			conf, _ := readRegistry(winscpConfKey)
			return winscpResult(`HKEY_CURRENT_USER\`+winscpRegKey, raw, conf["Security"]["UseMasterPassword"] == "1", master)
		}
		if !errors.Is(err, ErrNotFound) {
			return Result{}, err
		}
		// Instalación portable o configurada para guardar en un .ini.
		dir, _ := os.UserConfigDir()
		path = filepath.Join(dir, "WinSCP.ini")
		if _, err := os.Stat(path); err != nil {
			return Result{}, ErrNotFound
		}
	}
	var raw map[string]map[string]string
	var err error
	usesMaster := false
	if strings.EqualFold(filepath.Ext(path), ".reg") {
		raw, err = readRegFile(path, winscpRegKey)
		conf, _ := readRegFile(path, winscpConfKey)
		usesMaster = conf["Security"]["UseMasterPassword"] == "1"
	} else {
		raw, usesMaster, err = readWinSCPIni(path)
	}
	if err != nil {
		return Result{}, err
	}
	return winscpResult(path, raw, usesMaster, master)
}

// readWinSCPIni devuelve las secciones [Sessions\<nombre>] del WinSCP.ini y
// si usa contraseña maestra ([Configuration\Security] UseMasterPassword=1).
func readWinSCPIni(path string) (map[string]map[string]string, bool, error) {
	b, err := readText(path)
	if err != nil {
		return nil, false, err
	}
	out := map[string]map[string]string{}
	security := map[string]string{}
	var cur map[string]string
	sc := bufio.NewScanner(strings.NewReader(b))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			cur = nil
			section := line[1 : len(line)-1]
			if name, ok := strings.CutPrefix(section, `Sessions\`); ok {
				cur = map[string]string{}
				out[name] = cur
			} else if section == `Configuration\Security` {
				cur = security
			}
			continue
		}
		if cur == nil || line == "" || line[0] == ';' {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			cur[k] = v
		}
	}
	if err := sc.Err(); err != nil {
		return nil, false, err
	}
	if len(out) == 0 {
		return nil, false, i18n.Err("data.theFileHasNoWinscp")
	}
	return out, security["UseMasterPassword"] == "1", nil
}

// winscpItems traduce las sesiones con sus contraseñas (con usesMaster,
// descifradas con la contraseña maestra master).
func winscpItems(raw map[string]map[string]string, usesMaster bool, master string) ([]Item, []string, error) {
	var items []Item
	var skipped []string
	for key, v := range raw {
		full := unescape(key) // "Carpeta/Sub/Nombre"
		if full == "Default Settings" {
			continue
		}
		group, name := "WinSCP", full
		if i := strings.LastIndex(full, "/"); i >= 0 {
			group, name = full[:i], full[i+1:]
		}
		host := strings.TrimSpace(unescape(v["HostName"]))
		if host == "" {
			skipped = append(skipped, full+": no tiene host")
			continue
		}
		// 0 SCP, 1 SFTP (con SCP de respaldo), 2 SFTP; 5 FTP, 6 WebDAV, 7 S3.
		switch p := v["FSProtocol"]; p {
		case "", "0", "1", "2":
		default:
			skipped = append(skipped, fmt.Sprintf("%s: protocolo %s (osat solo importa SFTP/SCP)", full, winscpProto(p)))
			continue
		}
		it := Item{
			Name:     name,
			Group:    group,
			Protocol: store.ProtoSSH,
			Host:     host,
			Port:     atoi(v["PortNumber"], 22),
			User:     unescape(v["UserName"]),
			AuthType: store.AuthPassword,
		}
		if k := unescape(v["PublicKeyFile"]); k != "" {
			it.AuthType, it.KeyPath = store.AuthKey, k
		}
		switch pw := v["Password"]; {
		case pw == "":
		case !usesMaster:
			it.Secret = winscpPassword(pw, it.User, host)
		default:
			s, err := winscpMasterPassword(pw, master)
			if errors.Is(err, ErrBadMasterPassword) {
				return nil, nil, err
			}
			if err != nil {
				skipped = append(skipped, full+": no se pudo descifrar la contraseña (se importa sin ella)")
			}
			it.Secret = s
		}
		items = append(items, it)
	}
	return items, skipped, nil
}

func winscpProto(p string) string {
	switch p {
	case "5":
		return "FTP"
	case "6":
		return "WebDAV"
	case "7":
		return "S3"
	}
	return p
}

// ---------- mRemoteNG ----------

func scanMRemoteNG(path, master string) (Result, error) {
	if path == "" {
		dir, _ := os.UserConfigDir()
		path = filepath.Join(dir, "mRemoteNG", "confCons.xml")
		if _, err := os.Stat(path); err != nil {
			return Result{}, ErrNotFound
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return Result{}, err
	}
	defer f.Close()
	items, skipped, err := mremotengItems(f, master)
	if err != nil {
		return Result{}, err
	}
	return Result{Location: path, Items: items, Skipped: skipped}, nil
}

type mrNode struct {
	Name            string `xml:"Name,attr"`
	Type            string `xml:"Type,attr"`
	Protocol        string `xml:"Protocol,attr"`
	Hostname        string `xml:"Hostname,attr"`
	Port            string `xml:"Port,attr"`
	Username        string `xml:"Username,attr"`
	Domain          string `xml:"Domain,attr"`
	Password        string `xml:"Password,attr"`
	PuttySession    string `xml:"PuttySession,attr"`
	InheritUsername string `xml:"InheritUsername,attr"`
	InheritDomain   string `xml:"InheritDomain,attr"`
	InheritPassword string `xml:"InheritPassword,attr"`
	// RD Gateway (solo RDP): Never/Always/Detect, host, y si usa las
	// credenciales de la conexión (Yes) u otras.
	RDGatewayUsageMethod                     string   `xml:"RDGatewayUsageMethod,attr"`
	RDGatewayHostname                        string   `xml:"RDGatewayHostname,attr"`
	RDGatewayUseConnectionCredentials        string   `xml:"RDGatewayUseConnectionCredentials,attr"`
	InheritRDGatewayUsageMethod              string   `xml:"InheritRDGatewayUsageMethod,attr"`
	InheritRDGatewayHostname                 string   `xml:"InheritRDGatewayHostname,attr"`
	InheritRDGatewayUseConnectionCredentials string   `xml:"InheritRDGatewayUseConnectionCredentials,attr"`
	Children                                 []mrNode `xml:"Node"`
}

type mrRoot struct {
	FullFileEncryption string   `xml:"FullFileEncryption,attr"`
	BlockCipherMode    string   `xml:"BlockCipherMode,attr"`
	KdfIterations      string   `xml:"KdfIterations,attr"`
	Protected          string   `xml:"Protected,attr"`
	Nodes              []mrNode `xml:"Node"`
}

// mrCreds: usuario, dominio y contraseña (ya descifrada) que una carpeta de
// mRemoteNG les pasa a las conexiones que los heredan, y lo mismo del RD Gateway.
type mrCreds struct{ user, domain, password, gwMethod, gwHost, gwCreds string }

// puttySessions: las sesiones guardadas de PuTTY en este equipo, para
// encontrar la clave de las conexiones SSH de mRemoteNG (que la toman de una
// sesión de PuTTY). Variable para los tests.
var puttySessions = func() map[string]map[string]string {
	raw, _ := readRegistry(puttyRegKey)
	return raw
}

// mremotengItems lee las conexiones del confCons.xml con sus contraseñas;
// master es la contraseña maestra, si el archivo tiene una propia.
func mremotengItems(r io.Reader, master string) ([]Item, []string, error) {
	var root mrRoot
	if err := xml.NewDecoder(r).Decode(&root); err != nil {
		return nil, nil, i18n.Err("data.invalidConfconsXmlV", err)
	}
	if strings.EqualFold(root.FullFileEncryption, "true") {
		return nil, nil, i18n.Err("data.mremotengFullEncryption")
	}
	crypto, err := mrUnlock(root, master)
	if err != nil {
		return nil, nil, err
	}
	inherit := func(flag, own, parent string) string {
		if strings.EqualFold(flag, "true") {
			return parent
		}
		return own
	}
	var putty map[string]map[string]string // se lee la primera vez que hace falta
	var items []Item
	var skipped []string
	var walk func(nodes []mrNode, path []string, parent mrCreds)
	walk = func(nodes []mrNode, path []string, parent mrCreds) {
		for _, n := range nodes {
			label := strings.Join(append(append([]string{}, path...), n.Name), "/")
			own := ""
			if n.Password != "" {
				if own, err = crypto.decrypt(n.Password); err != nil {
					own = ""
					skipped = append(skipped, label+": no se pudo descifrar la contraseña (se importa sin ella)")
				}
			}
			creds := mrCreds{
				user:     inherit(n.InheritUsername, n.Username, parent.user),
				domain:   inherit(n.InheritDomain, n.Domain, parent.domain),
				password: inherit(n.InheritPassword, own, parent.password),
				gwMethod: inherit(n.InheritRDGatewayUsageMethod, n.RDGatewayUsageMethod, parent.gwMethod),
				gwHost:   inherit(n.InheritRDGatewayHostname, n.RDGatewayHostname, parent.gwHost),
				gwCreds:  inherit(n.InheritRDGatewayUseConnectionCredentials, n.RDGatewayUseConnectionCredentials, parent.gwCreds),
			}
			if strings.EqualFold(n.Type, "Container") {
				walk(n.Children, append(path, n.Name), creds)
				continue
			}
			host := strings.TrimSpace(n.Hostname)
			if host == "" {
				skipped = append(skipped, label+": no tiene host")
				continue
			}
			it := Item{Name: n.Name, Group: strings.Join(path, "/"), Host: host, User: creds.user, AuthType: store.AuthPassword, Secret: creds.password}
			if it.Group == "" {
				it.Group = "mRemoteNG"
			}
			switch strings.ToUpper(n.Protocol) {
			case "RDP":
				it.Protocol, it.Port, it.Domain = store.ProtoRDP, atoi(n.Port, 3389), creds.domain
				// En osat el gateway se usa siempre y con las credenciales de la conexión.
				if gw := strings.TrimSpace(creds.gwHost); gw != "" && !strings.EqualFold(creds.gwMethod, "Never") && creds.gwMethod != "" {
					it.Gateway = gw
					if strings.EqualFold(creds.gwMethod, "Detect") {
						skipped = append(skipped, label+": el RD Gateway está en «detectar»; osat lo usa siempre")
					}
					if !strings.EqualFold(creds.gwCreds, "Yes") {
						skipped = append(skipped, label+": el RD Gateway usa otras credenciales; osat usa las de la conexión")
					}
				}
			case "SSH1", "SSH2":
				it.Protocol, it.Port = store.ProtoSSH, atoi(n.Port, 22)
				// La clave privada la indica la sesión de PuTTY que usa la conexión.
				if s := n.PuttySession; s != "" && s != "Default Settings" {
					if putty == nil {
						putty = puttySessions()
					}
					for key, v := range putty {
						if unescape(key) == s && v["PublicKeyFile"] != "" {
							it.AuthType, it.KeyPath = store.AuthKey, v["PublicKeyFile"]
						}
					}
				}
			default:
				skipped = append(skipped, fmt.Sprintf("%s: protocolo %s (osat solo importa SSH y RDP)", label, n.Protocol))
				continue
			}
			items = append(items, it)
		}
	}
	walk(root.Nodes, nil, mrCreds{})
	return items, skipped, nil
}

// ---------- utilidades ----------

// readRegFile lee un .reg exportado con regedit (o `reg export`) y devuelve
// los valores de texto y DWORD de las subclaves de prefix, por nombre.
func readRegFile(path, prefix string) (map[string]map[string]string, error) {
	b, err := readText(path)
	if err != nil {
		return nil, err
	}
	head := strings.ToLower(`[HKEY_CURRENT_USER\` + prefix + `\`)
	out := map[string]map[string]string{}
	var cur map[string]string
	for _, line := range strings.Split(b, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			cur = nil
			if l := strings.ToLower(line); strings.HasPrefix(l, head) && strings.HasSuffix(l, "]") {
				name := line[len(head) : len(line)-1]
				if !strings.Contains(name, `\`) { // solo las sesiones, no sus subclaves
					cur = map[string]string{}
					out[name] = cur
				}
			}
			continue
		}
		if cur == nil || !strings.HasPrefix(line, `"`) {
			continue
		}
		name, rest, ok := regQuoted(line)
		if !ok || !strings.HasPrefix(rest, "=") {
			continue
		}
		rest = rest[1:]
		switch {
		case strings.HasPrefix(rest, `"`):
			if v, _, ok := regQuoted(rest); ok {
				cur[name] = v
			}
		case strings.HasPrefix(strings.ToLower(rest), "dword:"):
			if n, err := strconv.ParseUint(rest[len("dword:"):], 16, 32); err == nil {
				cur[name] = strconv.FormatUint(n, 10)
			}
		}
	}
	if len(out) == 0 {
		return nil, i18n.Err("data.theFileHasNoSessions", prefix)
	}
	return out, nil
}

// regQuoted lee una cadena entre comillas de un .reg (con \\ y \" escapados)
// y devuelve lo que sigue.
func regQuoted(s string) (string, string, bool) {
	var sb strings.Builder
	for i := 1; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\':
			if i+1 < len(s) {
				i++
				sb.WriteByte(s[i])
			}
		case '"':
			return sb.String(), s[i+1:], true
		default:
			sb.WriteByte(c)
		}
	}
	return "", "", false
}

// readText lee un archivo de texto en UTF-16 (con BOM, como los .reg de
// regedit) o UTF-8.
func readText(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		u := make([]uint16, (len(b)-2)/2)
		for i := range u {
			u[i] = uint16(b[2+2*i]) | uint16(b[3+2*i])<<8
		}
		return string(utf16.Decode(u)), nil
	}
	return string(bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})), nil
}

// unescape deshace el %XX con que PuTTY y WinSCP guardan nombres y valores.
func unescape(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	if u, err := url.PathUnescape(s); err == nil {
		return u
	}
	return s
}

func atoi(s string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n > 0 && n < 65536 {
		return n
	}
	return def
}

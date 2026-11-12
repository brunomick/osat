package importer

import (
	"bufio"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"osat/internal/i18n"
	"osat/internal/store"
)

// OpenSSH: el ~/.ssh/config del cliente ssh (Linux, macOS y el ssh de Windows).
const OpenSSH = "openssh"

// sshBlock es un bloque Host (o Match) del config con sus opciones en orden.
// Las opciones de antes del primer Host valen para todos (patrón "*").
type sshBlock struct {
	patterns []string
	match    bool // bloque Match: osat no lo interpreta
	opts     [][2]string
}

func scanOpenSSH(path string) (Result, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Result{}, ErrNotFound
		}
		path = filepath.Join(home, ".ssh", "config")
		if _, err := os.Stat(path); err != nil {
			return Result{}, ErrNotFound
		}
	}
	blocks := []sshBlock{{patterns: []string{"*"}}}
	// Los Include relativos se buscan junto al config (~/.ssh, como hace ssh).
	if err := readSSHConfig(path, filepath.Dir(path), 0, &blocks); err != nil {
		return Result{}, err
	}
	items, skipped := opensshItems(blocks)
	if len(items) == 0 {
		return Result{}, i18n.Err("data.theFileHasNoOpenssh")
	}
	return Result{Location: path, Items: items, Skipped: skipped}, nil
}

// readSSHConfig agrega a blocks las líneas de path. Un Include mete las
// líneas de los archivos incluidos en ese lugar, como hace ssh.
func readSSHConfig(path, dir string, depth int, blocks *[]sshBlock) error {
	if depth > 16 {
		return i18n.Err("data.tooManyNestedIncludesIn")
	}
	b, err := readText(path)
	if err != nil {
		return err
	}
	sc := bufio.NewScanner(strings.NewReader(b))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		key, args := sshFields(sc.Text())
		if key == "" || len(args) == 0 {
			continue
		}
		switch key {
		case "host":
			*blocks = append(*blocks, sshBlock{patterns: args})
		case "match":
			*blocks = append(*blocks, sshBlock{patterns: args, match: true})
		case "include":
			for _, pat := range args {
				pat = expandHome(pat)
				if !filepath.IsAbs(pat) {
					pat = filepath.Join(dir, pat)
				}
				files, _ := filepath.Glob(pat) // un Include que no encuentra nada no es error
				for _, f := range files {
					if err := readSSHConfig(f, dir, depth+1, blocks); err != nil {
						return err
					}
				}
			}
		default:
			cur := &(*blocks)[len(*blocks)-1]
			cur.opts = append(cur.opts, [2]string{key, strings.Join(args, " ")})
		}
	}
	return sc.Err()
}

// opensshItems arma una conexión por cada nombre de Host sin comodines, con
// las opciones de todos los bloques que le corresponden (gana el primer valor,
// como en ssh).
func opensshItems(blocks []sshBlock) ([]Item, []string) {
	var items []Item
	var skipped []string
	seen := map[string]bool{}
	for _, bl := range blocks {
		if bl.match {
			skipped = append(skipped, fmt.Sprintf("Match %s: osat no interpreta los bloques Match, sus opciones se ignoran", strings.Join(bl.patterns, " ")))
			continue
		}
		for _, alias := range bl.patterns {
			if strings.ContainsAny(alias, "*?!") || seen[strings.ToLower(alias)] {
				continue
			}
			seen[strings.ToLower(alias)] = true
			vals := map[string]string{}
			for _, b := range blocks {
				if b.match || !sshHostMatches(alias, b.patterns) {
					continue
				}
				for _, o := range b.opts {
					if _, ok := vals[o[0]]; !ok {
						vals[o[0]] = o[1]
					}
				}
			}
			host := alias
			if h := vals["hostname"]; h != "" {
				host = strings.NewReplacer("%%", "%", "%h", alias).Replace(h)
			}
			it := Item{
				Name:     alias,
				Group:    "OpenSSH",
				Protocol: store.ProtoSSH,
				Host:     host,
				Port:     atoi(vals["port"], 22),
				User:     vals["user"],
				AuthType: store.AuthAgent, // sin IdentityFile, ssh prueba el agente
			}
			if it.User == "" {
				it.User = localUser()
			}
			it.ForwardAgent = strings.EqualFold(vals["forwardagent"], "yes")
			if k := vals["identityfile"]; k != "" && !strings.EqualFold(k, "none") {
				it.AuthType, it.KeyPath = store.AuthKey, expandHome(k)
			}
			// ProxyJump a otro Host (un nombre solo) se enlaza al importar. Una
			// cadena (a,b) o usuario@host:puerto no tienen una conexión a la que apuntar.
			switch v := vals["proxyjump"]; {
			case v == "" || strings.EqualFold(v, "none"):
			case !strings.ContainsAny(v, ",@:"):
				it.Jump = v
			default:
				skipped = append(skipped, fmt.Sprintf("%s: usa ProxyJump %s (osat solo enlaza un salto a otro Host: se importa sin salto, configuralo en la conexión)", alias, v))
			}
			if v := vals["proxycommand"]; v != "" && !strings.EqualFold(v, "none") {
				skipped = append(skipped, fmt.Sprintf("%s: usa ProxyCommand %s (osat no lo soporta: se importa sin él, puede no conectar)", alias, v))
			}
			items = append(items, it)
		}
	}
	return items, skipped
}

// sshFields separa una línea del config en palabra clave (en minúsculas) y
// argumentos. Acepta "Clave valor" y "Clave=valor", y argumentos entre comillas.
func sshFields(line string) (string, []string) {
	line = strings.TrimSpace(line)
	if line == "" || line[0] == '#' {
		return "", nil
	}
	i := strings.IndexAny(line, " \t=")
	if i < 0 {
		return strings.ToLower(line), nil
	}
	key, rest := strings.ToLower(line[:i]), strings.TrimLeft(line[i:], " \t")
	rest = strings.TrimLeft(strings.TrimPrefix(rest, "="), " \t")
	var args []string
	for rest != "" {
		var arg string
		if rest[0] == '"' {
			end := strings.IndexByte(rest[1:], '"')
			if end < 0 {
				arg, rest = rest[1:], ""
			} else {
				arg, rest = rest[1:end+1], rest[end+2:]
			}
		} else {
			end := strings.IndexAny(rest, " \t")
			if end < 0 {
				end = len(rest)
			}
			arg, rest = rest[:end], rest[end:]
		}
		args = append(args, arg)
		rest = strings.TrimLeft(rest, " \t")
	}
	return key, args
}

// sshHostMatches: el nombre coincide con algún patrón de la lista y con
// ninguno negado (!patrón). Sin distinguir mayúsculas.
func sshHostMatches(host string, patterns []string) bool {
	host = strings.ToLower(host)
	ok := false
	for _, p := range patterns {
		p = strings.ToLower(p)
		if neg, found := strings.CutPrefix(p, "!"); found {
			if wildMatch(neg, host) {
				return false
			}
		} else if wildMatch(p, host) {
			ok = true
		}
	}
	return ok
}

// wildMatch compara con los comodines de ssh: * (cualquier cosa) y ? (un carácter).
func wildMatch(p, s string) bool {
	for p != "" {
		switch p[0] {
		case '*':
			for i := 0; i <= len(s); i++ {
				if wildMatch(p[1:], s[i:]) {
					return true
				}
			}
			return false
		case '?':
			if s == "" {
				return false
			}
		default:
			if s == "" || s[0] != p[0] {
				return false
			}
		}
		p, s = p[1:], s[1:]
	}
	return s == ""
}

// expandHome reemplaza ~ y %d (la carpeta del usuario) al principio de una ruta.
func expandHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	for _, pre := range []string{"~", "%d"} {
		if rest, ok := strings.CutPrefix(p, pre); ok && (rest == "" || rest[0] == '/' || rest[0] == '\\') {
			return filepath.Join(home, rest)
		}
	}
	return p
}

// localUser: el usuario de este equipo, que ssh usa cuando el config no dice User.
func localUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		name := u.Username
		if i := strings.LastIndex(name, `\`); i >= 0 { // DOMINIO\usuario en Windows
			name = name[i+1:]
		}
		return name
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return os.Getenv("USERNAME")
}

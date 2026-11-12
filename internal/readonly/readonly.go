// Package readonly decide si una línea de comandos es de solo lectura, para que
// el puente con Claude Code pueda correrla sin pedir permiso. Es una lista
// blanca estricta: lo que no reconoce como seguro se rechaza, y quien llama
// lo trata como un comando común (que sí pide permiso). Ante la duda, rechaza.
//
// Reglas generales (shell estilo POSIX):
//   - un solo comando o una tubería (|) de comandos conocidos;
//   - nada de ; & && || redirecciones ( > < ), sustituciones ($ ` ) ni
//     paréntesis, llaves, barras invertidas ni "!" fuera de comillas simples;
//   - el comando va por su nombre (sin ruta ni VAR=valor delante);
//   - cada comando conocido tiene además sus opciones peligrosas vedadas
//     (find -delete, tail -f, git -c...);
//   - se rechaza cualquier cosa que nombre archivos con secretos (claves,
//     contraseñas, tokens...), porque leer no es lo mismo que ser inofensivo.
package readonly

import (
	"errors"
	"fmt"
	"strings"
)

const maxLen = 2000

// Check devuelve nil si command es de solo lectura y un error explicando por
// qué no, en caso contrario.
func Check(command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return errors.New("the command is empty")
	}
	if len(command) > maxLen {
		return errors.New("the command is too long to be checked")
	}
	segments, err := split(command)
	if err != nil {
		return err
	}
	for _, words := range segments {
		if err := checkSegment(words); err != nil {
			return err
		}
	}
	lower := strings.ToLower(command)
	for _, s := range sensitive {
		if strings.Contains(lower, s) {
			return fmt.Errorf("it mentions %q, which may expose secrets", s)
		}
	}
	return nil
}

// split separa una tubería en sus comandos y cada uno en palabras (ya sin
// comillas), rechazando todo lo que podría encadenar o ejecutar otra cosa.
func split(cmd string) ([][]string, error) {
	var segs [][]string
	var words []string
	var cur strings.Builder
	inWord := false
	var quote byte
	flush := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	bad := func(what string) error {
		return fmt.Errorf("it uses %s, which is not allowed in a read-only command", what)
	}

	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		if c < 0x20 && c != '\t' || c == 0x7f {
			return nil, bad("a control character or a line break")
		}
		switch quote {
		case '\'':
			if c == '\'' {
				quote = 0
			} else {
				cur.WriteByte(c)
			}
			continue
		case '"':
			switch c {
			case '"':
				quote = 0
			case '$', '`', '\\', '!':
				return nil, bad(fmt.Sprintf("%q inside double quotes", c))
			default:
				cur.WriteByte(c)
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote, inWord = c, true
		case ' ', '\t':
			flush()
		case '|':
			if i+1 < len(cmd) && cmd[i+1] == '|' {
				return nil, bad("||")
			}
			flush()
			if len(words) == 0 {
				return nil, errors.New("a pipe has no command before it")
			}
			segs, words = append(segs, words), nil
		case ';', '&', '>', '<', '(', ')', '{', '}', '\\', '!', '`', '$':
			return nil, bad(fmt.Sprintf("%q", c))
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, errors.New("a quote is not closed")
	}
	flush()
	if len(words) == 0 {
		return nil, errors.New("a pipe has no command after it")
	}
	return append(segs, words), nil
}

func checkSegment(words []string) error {
	name := words[0]
	if strings.ContainsAny(name, "/=") {
		return fmt.Errorf("%q: give the command by its bare name (no path and no VAR=value before it)", name)
	}
	rule, ok := rules[name]
	if !ok {
		return fmt.Errorf("%q is not recognized as a read-only command", name)
	}
	if rule == nil {
		return nil
	}
	if err := rule(words[1:]); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// sensitive: fragmentos de nombres de archivos o variables que suelen tener secretos.
var sensitive = []string{
	".ssh", "id_rsa", "id_dsa", "id_ecdsa", "id_ed25519", "shadow", ".pem", ".key", ".p12", ".pfx", ".kdbx",
	".aws", ".netrc", ".npmrc", ".pgpass", ".git-credentials", ".docker/config", ".kube", "kubeconfig",
	".env", "secret", "password", "token", "credential", "environ", ".bash_history", ".zsh_history",
	".mysql_history", ".psql_history", "private",
}

type rule func(args []string) error

// ---------- reglas por comando ----------

// flagArg dice si a es una opción (empieza con - y no es un "-" suelto ni un número).
func flagArg(a string) bool {
	return len(a) > 1 && a[0] == '-' && !(a[1] >= '0' && a[1] <= '9')
}

// deny veda opciones largas (también con =valor) y cortas sueltas.
func deny(flags ...string) rule {
	return func(args []string) error {
		for _, a := range args {
			for _, f := range flags {
				if a == f || strings.HasPrefix(a, f+"=") {
					return fmt.Errorf("the option %s is not allowed", f)
				}
			}
		}
		return nil
	}
}

// denyShort veda cualquier grupo de opciones cortas (-abc) que contenga alguna
// de esas letras.
func denyShort(letters string) rule {
	return func(args []string) error {
		for _, a := range args {
			if flagArg(a) && !strings.HasPrefix(a, "--") && strings.ContainsAny(a[1:], letters) {
				return fmt.Errorf("the option %s is not allowed", a)
			}
		}
		return nil
	}
}

// firstOneOf exige que el primer argumento que no es opción sea uno de allowed.
func firstOneOf(allowed ...string) rule {
	return func(args []string) error {
		for _, a := range args {
			if flagArg(a) {
				continue
			}
			for _, ok := range allowed {
				if a == ok {
					return nil
				}
			}
			return fmt.Errorf("%q is not a read-only subcommand", a)
		}
		return errors.New("it needs a read-only subcommand")
	}
}

// only exige que todos los argumentos estén en allowed (y admite ninguno).
func only(allowed ...string) rule {
	return func(args []string) error {
		for _, a := range args {
			ok := false
			for _, v := range allowed {
				if a == v {
					ok = true
				}
			}
			if !ok {
				return fmt.Errorf("the argument %q is not allowed", a)
			}
		}
		return nil
	}
}

func all(rs ...rule) rule {
	return func(args []string) error {
		for _, r := range rs {
			if err := r(args); err != nil {
				return err
			}
		}
		return nil
	}
}

// noPositional: como mucho n argumentos que no son opciones.
func maxPositional(n int) rule {
	return func(args []string) error {
		c := 0
		for _, a := range args {
			if !flagArg(a) {
				c++
			}
		}
		if c > n {
			return errors.New("too many arguments (the second file would be written to)")
		}
		return nil
	}
}

// subcommandAndNo exige un subcomando de solo lectura (el primero que no es
// opción) y veda opciones que cambian de comportamiento.
func subcommand(allowed []string, forbidden ...string) rule {
	return all(firstOneOf(allowed...), deny(forbidden...))
}

// withSub agrega una regla extra para cuando el subcomando es sub.
func withSub(allowed []string, extra map[string]rule, forbidden ...string) rule {
	return func(args []string) error {
		if err := subcommand(allowed, forbidden...)(args); err != nil {
			return err
		}
		for _, a := range args {
			if !flagArg(a) {
				if r, ok := extra[a]; ok {
					return r(args)
				}
				break
			}
		}
		return nil
	}
}

var noFollow = all(deny("--follow", "--retry"), denyShort("fF"))

// rules: comandos de solo lectura. nil = cualquier argumento.
var rules = map[string]rule{
	// Dónde estoy / qué hay
	"ls": nil, "dir": nil, "pwd": nil, "stat": nil, "readlink": nil, "realpath": nil,
	"tree":     all(deny("-o"), denyShort("o")), // tree -o archivo escribe
	"basename": nil, "dirname": nil, "lsattr": nil, "getfacl": nil, "namei": nil,
	"file":  deny("-C", "--compile"),
	"find":  deny("-exec", "-execdir", "-ok", "-okdir", "-delete", "-fprint", "-fprint0", "-fprintf", "-fls"),
	"which": nil, "whereis": nil,

	// Quién y qué máquina
	"whoami": nil, "id": nil, "groups": nil, "who": nil, "w": nil, "users": nil, "last": nil,
	"uname": nil, "uptime": nil, "nproc": nil, "arch": nil, "lsb_release": nil, "getent": nil,
	"hostname": only("-f", "-i", "-I", "-s", "-d", "-A", "-a", "-y", "--fqdn", "--ip-address", "--all-ip-addresses",
		"--short", "--domain", "--all-fqdns", "--alias", "--yp", "--nis"),
	"date": all(deny("-s", "--set"), denyShort("s")),

	// Recursos
	"df": nil, "du": nil, "free": nil, "lsblk": nil, "lscpu": nil, "lspci": nil, "lsusb": nil, "lsmod": nil,
	"lsof": nil, "lsns": nil, "findmnt": nil, "pgrep": nil, "pidof": nil, "mount": only(),
	"ps":      nil,
	"dmesg":   all(deny("-c", "-C", "--clear", "--read-clear", "-w", "--follow", "--follow-new"), denyShort("cCw")),
	"sysctl":  all(deny("-w", "--write", "-p", "--load", "--system"), denyShort("wp"), noAssign),
	"crontab": only("-l"),
	"ufw":     only("status", "verbose", "numbered"),

	// Leer texto
	"cat": nil, "head": nil, "tac": nil, "nl": nil, "rev": nil, "wc": nil, "echo": nil,
	"tail": noFollow, "zcat": nil, "bzcat": nil, "xzcat": nil, "zgrep": nil,
	"grep": nil, "egrep": nil, "fgrep": nil,
	"cut": nil, "tr": nil, "column": nil, "strings": nil, "od": nil, "hexdump": nil,
	"jq":   denySubstr("env"), // jq 'env' / $ENV imprimen las variables de entorno
	"sort": all(deny("-o", "--output", "--compress-program"), denyShort("o")),
	"uniq": maxPositional(1),
	"diff": nil, "cmp": nil,
	"md5sum": nil, "sha1sum": nil, "sha256sum": nil, "sha512sum": nil, "cksum": nil,

	// Red
	"ss":      all(deny("-K", "--kill"), denyShort("K")),
	"netstat": nil, "dig": nil, "nslookup": nil, "host": nil,
	"ip": all(firstOneOf("a", "addr", "address", "l", "link", "r", "route", "n", "neigh", "neighbor", "neighbour", "rule", "maddr"),
		denyVerbs("add", "del", "delete", "set", "change", "replace", "flush", "append", "up", "down", "save", "restore", "exec", "netns")),

	// Servicios y contenedores
	// --no-pager: sin él systemctl/journalctl abren un paginador que se queda con la terminal.
	"systemctl": all(needFlag("--no-pager"), firstOneOf("status", "is-active", "is-enabled", "is-failed", "list-units",
		"list-unit-files", "list-timers", "list-sockets", "list-dependencies", "list-jobs", "show", "cat")),
	"journalctl": all(needFlag("--no-pager"), deny("--vacuum-size", "--vacuum-time", "--vacuum-files", "--rotate",
		"--flush", "--sync", "--setup-keys", "--cursor-file", "--follow", "--relinquish-var", "--smart-relinquish-var",
		"--update-catalog"), denyShort("f")),
	"docker": withSub([]string{"ps", "images", "inspect", "logs", "version", "info", "stats", "top", "port", "diff", "history"},
		map[string]rule{"logs": noFollow, "stats": needFlag("--no-stream")}),
	"kubectl": withSub([]string{"get", "describe", "logs", "top", "version", "api-resources", "cluster-info", "explain"},
		map[string]rule{"logs": noFollow}, "--raw"),
	"git": gitRule,
}

// noAssign veda argumentos tipo nombre=valor (en sysctl cambian el valor).
func noAssign(args []string) error {
	for _, a := range args {
		if strings.Contains(a, "=") {
			return fmt.Errorf("%q would change a setting", a)
		}
	}
	return nil
}

func denyVerbs(verbs ...string) rule {
	return func(args []string) error {
		for _, a := range args {
			for _, v := range verbs {
				if a == v {
					return fmt.Errorf("%q changes the configuration", a)
				}
			}
		}
		return nil
	}
}

// denySubstr veda cualquier argumento que contenga alguno de esos textos.
func denySubstr(parts ...string) rule {
	return func(args []string) error {
		for _, a := range args {
			for _, p := range parts {
				if strings.Contains(strings.ToLower(a), p) {
					return fmt.Errorf("an argument containing %q could expose the environment", p)
				}
			}
		}
		return nil
	}
}

func needFlag(flag string) rule {
	return func(args []string) error {
		for _, a := range args {
			if a == flag {
				return nil
			}
		}
		return fmt.Errorf("it must include %s (otherwise it never finishes)", flag)
	}
}

var gitSubs = []string{"status", "log", "diff", "show", "rev-parse", "describe", "ls-files", "ls-tree", "blame",
	"shortlog", "branch", "remote", "tag", "reflog", "grep"}

// gitRule: solo subcomandos de lectura; sin -c ni --exec-path (corren cosas) y sin --output.
// Exige --no-pager (sin él git abre un paginador que se queda con la terminal).
func gitRule(args []string) error {
	// Opciones globales: solo -C <dir> y --no-pager.
	i, noPager := 0, false
	for i < len(args) && flagArg(args[i]) {
		switch {
		case args[i] == "--no-pager" || args[i] == "-P":
			noPager = true
			i++
		case args[i] == "-C" && i+1 < len(args):
			i += 2
		default:
			return fmt.Errorf("the global option %s is not allowed", args[i])
		}
	}
	if !noPager {
		return errors.New("use `git --no-pager <subcommand>` (otherwise git opens a pager that traps the terminal)")
	}
	if i >= len(args) {
		return errors.New("it needs a read-only subcommand")
	}
	sub, rest := args[i], args[i+1:]
	ok := false
	for _, s := range gitSubs {
		ok = ok || s == sub
	}
	if !ok {
		return fmt.Errorf("%q is not a read-only git subcommand", sub)
	}
	for _, a := range rest {
		if a == "--output" || strings.HasPrefix(a, "--output=") || a == "--ext-diff" || strings.HasPrefix(a, "--exec") {
			return fmt.Errorf("the option %s is not allowed", a)
		}
	}
	switch sub {
	case "branch", "tag": // solo listar
		for _, a := range rest {
			if !flagArg(a) {
				return fmt.Errorf("git %s with a name would create something", sub)
			}
			if strings.ContainsAny(a, "dDmMcCfu") && !strings.HasPrefix(a, "--") || strings.HasPrefix(a, "--delete") ||
				strings.HasPrefix(a, "--move") || strings.HasPrefix(a, "--copy") || strings.HasPrefix(a, "--set-upstream") {
				return fmt.Errorf("the option %s changes the repository", a)
			}
		}
	case "remote": // solo `git remote` o `git remote -v`
		return only("-v", "--verbose")(rest)
	case "reflog": // `git reflog expire` y `delete` borran entradas
		for _, a := range rest {
			if !flagArg(a) && a != "show" {
				return fmt.Errorf("git reflog %s changes the repository", a)
			}
		}
	case "grep": // -O / --open-files-in-pager ejecutan un programa
		for _, a := range rest {
			if strings.HasPrefix(a, "-O") || strings.HasPrefix(a, "--open-files-in-pager") {
				return fmt.Errorf("the option %s runs a program", a)
			}
		}
	}
	return nil
}

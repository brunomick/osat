// Package mongouri lee y arma cadenas de conexión de MongoDB
// (mongodb://usuario:contraseña@host1:27017,host2/base?opciones y
// mongodb+srv://). osat guarda la cadena sin la contraseña (va al llavero) y
// se la pasa así a mongosh, que la pide al conectar.
//
// No se usa net/url: con varios hosts ("h1:27017,h2:27017") no la acepta.
package mongouri

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"osat/internal/i18n"
)

// DefaultPort es el puerto de MongoDB cuando la cadena no lo dice.
const DefaultPort = 27017

// URI es una cadena de conexión separada en partes. User y Password ya vienen
// decodificados (sin %XX).
type URI struct {
	SRV      bool     // mongodb+srv://: los hosts se buscan en el DNS
	User     string   // "" = sin autenticación (o un mecanismo que no la usa, como X.509)
	Password string   // "" = no está en la cadena
	Hosts    []string // "host" o "host:puerto", tal como vienen
	Path     string   // "/base" o ""
	Query    string   // sin el "?"
}

// Parse separa una cadena de conexión.
func Parse(s string) (URI, error) {
	s = strings.TrimSpace(s)
	var u URI
	rest, ok := cutPrefixFold(s, "mongodb+srv://")
	if ok {
		u.SRV = true
	} else if rest, ok = cutPrefixFold(s, "mongodb://"); !ok {
		return URI{}, i18n.Err("mongo.badScheme")
	}
	// Lo que sigue al primer "/" o "?" es la base y las opciones.
	authority := rest
	if end := strings.IndexAny(rest, "/?"); end >= 0 {
		authority, rest = rest[:end], rest[end:]
	} else {
		rest = ""
	}
	if i := strings.LastIndex(authority, "@"); i >= 0 {
		info := authority[:i]
		authority = authority[i+1:]
		user, pass, hasPass := strings.Cut(info, ":")
		var err error
		if u.User, err = url.PathUnescape(user); err != nil {
			return URI{}, i18n.Err("mongo.badUserEncoding")
		}
		if hasPass {
			if u.Password, err = url.PathUnescape(pass); err != nil {
				return URI{}, i18n.Err("mongo.badPasswordEncoding")
			}
		}
	}
	for _, h := range strings.Split(authority, ",") {
		if h = strings.TrimSpace(h); h != "" {
			u.Hosts = append(u.Hosts, h)
		}
	}
	if len(u.Hosts) == 0 {
		return URI{}, i18n.Err("mongo.noHost")
	}
	if u.SRV && len(u.Hosts) > 1 {
		return URI{}, i18n.Err("mongo.srvOneHost")
	}
	for _, h := range u.Hosts {
		if strings.ContainsAny(h, " \t\r\n\"'`") {
			return URI{}, i18n.Err("mongo.badHost", h)
		}
	}
	if path, query, hasQuery := strings.Cut(rest, "?"); hasQuery {
		u.Path, u.Query = path, query
	} else {
		u.Path = rest
	}
	if strings.ContainsAny(u.Path+u.Query, " \t\r\n") {
		return URI{}, i18n.Err("mongo.badOptions")
	}
	return u, nil
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return s, false
}

// String arma la cadena completa, con la contraseña si la tiene.
func (u URI) String() string { return u.build(true) }

// Redacted arma la cadena sin la contraseña (con el usuario, si hay).
func (u URI) Redacted() string { return u.build(false) }

func (u URI) build(withPassword bool) string {
	var b strings.Builder
	if u.SRV {
		b.WriteString("mongodb+srv://")
	} else {
		b.WriteString("mongodb://")
	}
	if u.User != "" {
		b.WriteString(escape(u.User))
		if withPassword && u.Password != "" {
			b.WriteByte(':')
			b.WriteString(escape(u.Password))
		}
		b.WriteByte('@')
	}
	b.WriteString(strings.Join(u.Hosts, ","))
	b.WriteString(u.Path)
	if u.Query != "" {
		if u.Path == "" {
			b.WriteByte('/')
		}
		b.WriteByte('?')
		b.WriteString(u.Query)
	}
	return b.String()
}

// escape codifica usuario y contraseña como pide MongoDB: todo lo que no sea
// letra, número o -._~ va como %XX (incluidos : / ? # [ ] @ y %).
func escape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("-._~", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// First devuelve el host y el puerto del primer host de la lista (el puerto
// por defecto si no lo dice; en mongodb+srv:// también, aunque lo decida el DNS).
func (u URI) First() (string, int) {
	h := u.Hosts[0]
	if host, port, err := net.SplitHostPort(h); err == nil {
		if p, err := strconv.Atoi(port); err == nil && p > 0 && p < 65536 {
			return host, p
		}
		return host, DefaultPort
	}
	return strings.Trim(h, "[]"), DefaultPort
}

// Option devuelve el valor de una opción de la cadena (sin distinguir
// mayúsculas en el nombre, como MongoDB) y si está.
func (u URI) Option(name string) (string, bool) {
	for _, kv := range strings.Split(u.Query, "&") {
		k, v, _ := strings.Cut(kv, "=")
		if kv != "" && strings.EqualFold(k, name) {
			if dec, err := url.QueryUnescape(v); err == nil {
				v = dec
			}
			return v, true
		}
	}
	return "", false
}

// WithOption agrega (o reemplaza) una opción.
func (u URI) WithOption(name, value string) URI {
	var kept []string
	for _, kv := range strings.Split(u.Query, "&") {
		if k, _, _ := strings.Cut(kv, "="); kv == "" || strings.EqualFold(k, name) {
			continue
		}
		kept = append(kept, kv)
	}
	u.Query = strings.Join(append(kept, name+"="+url.QueryEscape(value)), "&")
	return u
}

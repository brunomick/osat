// Package vpn levanta un túnel WireGuard dentro de osat, en espacio de usuario
// (wireguard-go + netstack): no instala drivers ni toca la red del equipo. Con
// la VPN activa, las conexiones de osat salen por el túnel (Dial); el resto de
// la PC sigue como estaba.
package vpn

import (
	"bufio"
	"encoding/base64"
	"net/netip"
	"strconv"
	"strings"

	"osat/internal/i18n"
)

// Config es lo que osat usa de un archivo .conf de WireGuard. Los comandos de
// wg-quick (PostUp, Table, etc.) no aplican y se ignoran.
type Config struct {
	PrivateKey []byte // 32 bytes
	Addresses  []netip.Prefix
	DNS        []netip.Addr // vacío: los nombres se resuelven con el DNS del sistema
	MTU        int
	Peers      []Peer
}

// Peer es un servidor WireGuard del .conf.
type Peer struct {
	PublicKey    []byte
	PresharedKey []byte // nil si no hay
	Endpoint     string // host:puerto
	AllowedIPs   []netip.Prefix
	Keepalive    int // segundos; 0 = no indicado
}

const defaultMTU = 1420

// Parse lee el texto de un .conf de WireGuard.
func Parse(text string) (Config, error) {
	var c Config
	var peer *Peer
	section := ""
	sc := bufio.NewScanner(strings.NewReader(text))
	line := 0
	for sc.Scan() {
		line++
		l := sc.Text()
		if i := strings.IndexAny(l, "#;"); i >= 0 {
			l = l[:i]
		}
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "[") && strings.HasSuffix(l, "]") {
			section = strings.ToLower(strings.TrimSpace(l[1 : len(l)-1]))
			if section == "peer" {
				c.Peers = append(c.Peers, Peer{})
				peer = &c.Peers[len(c.Peers)-1]
			}
			continue
		}
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			return Config{}, i18n.Err("vpn.badLine", line)
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		var err error
		switch section {
		case "interface":
			switch k {
			case "privatekey":
				c.PrivateKey, err = parseKey(v)
			case "address":
				var p []netip.Prefix
				p, err = parsePrefixes(v)
				c.Addresses = append(c.Addresses, p...)
			case "dns":
				// Además de IPs, DNS puede traer dominios de búsqueda: esos no se usan.
				for _, s := range splitList(v) {
					if a, e := netip.ParseAddr(s); e == nil {
						c.DNS = append(c.DNS, a)
					}
				}
			case "mtu":
				c.MTU, err = strconv.Atoi(v)
				if err == nil && (c.MTU < 576 || c.MTU > 65535) {
					err = strconv.ErrRange
				}
			}
		case "peer":
			switch k {
			case "publickey":
				peer.PublicKey, err = parseKey(v)
			case "presharedkey":
				peer.PresharedKey, err = parseKey(v)
			case "endpoint":
				peer.Endpoint = v
			case "allowedips":
				var p []netip.Prefix
				p, err = parsePrefixes(v)
				peer.AllowedIPs = append(peer.AllowedIPs, p...)
			case "persistentkeepalive":
				if strings.EqualFold(v, "off") {
					break
				}
				peer.Keepalive, err = strconv.Atoi(v)
				if err == nil && (peer.Keepalive < 0 || peer.Keepalive > 65535) {
					err = strconv.ErrRange
				}
			}
		default:
			return Config{}, i18n.Err("vpn.badLine", line)
		}
		if err != nil {
			return Config{}, i18n.Err("vpn.badValue", line, k)
		}
	}
	if c.MTU == 0 {
		c.MTU = defaultMTU
	}
	return c, c.validate()
}

func (c Config) validate() error {
	switch {
	case c.PrivateKey == nil:
		return i18n.Err("vpn.noPrivateKey")
	case len(c.Addresses) == 0:
		return i18n.Err("vpn.noAddress")
	case len(c.Peers) == 0:
		return i18n.Err("vpn.noPeer")
	}
	for _, p := range c.Peers {
		if p.PublicKey == nil || p.Endpoint == "" {
			return i18n.Err("vpn.incompletePeer")
		}
		if _, port, err := splitHostPort(p.Endpoint); err != nil || port == 0 {
			return i18n.Err("vpn.badEndpoint", p.Endpoint)
		}
	}
	return nil
}

// StripPrivateKey devuelve el texto del .conf sin la línea de PrivateKey (para
// guardarlo en disco: la clave va al llavero del sistema).
func StripPrivateKey(text string) string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if k, _, ok := strings.Cut(l, "="); ok && strings.EqualFold(strings.TrimSpace(k), "privatekey") {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// WithPrivateKey vuelve a poner la clave en un texto sacado con StripPrivateKey.
func WithPrivateKey(text, key string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if strings.EqualFold(strings.TrimSpace(l), "[interface]") {
			out := append([]string{}, lines[:i+1]...)
			out = append(out, "PrivateKey = "+key)
			return strings.Join(append(out, lines[i+1:]...), "\n")
		}
	}
	return text
}

// PrivateKeyText devuelve el valor de PrivateKey tal como está en el texto ("" si no hay).
func PrivateKeyText(text string) string {
	for _, l := range strings.Split(text, "\n") {
		if k, v, ok := strings.Cut(l, "="); ok && strings.EqualFold(strings.TrimSpace(k), "privatekey") {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func parseKey(s string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) != 32 {
		return nil, strconv.ErrSyntax
	}
	return b, nil
}

func parsePrefixes(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, item := range splitList(s) {
		p, err := netip.ParsePrefix(item)
		if err != nil {
			a, err2 := netip.ParseAddr(item)
			if err2 != nil {
				return nil, err
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		out = append(out, p)
	}
	return out, nil
}

func splitList(s string) []string {
	var out []string
	for _, item := range strings.Split(s, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// splitHostPort separa "host:puerto" (también "[v6]:puerto").
func splitHostPort(s string) (string, uint16, error) {
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return "", 0, strconv.ErrSyntax
	}
	host := strings.TrimSuffix(strings.TrimPrefix(s[:i], "["), "]")
	port, err := strconv.ParseUint(s[i+1:], 10, 16)
	if err != nil || host == "" {
		return "", 0, strconv.ErrSyntax
	}
	return host, uint16(port), nil
}

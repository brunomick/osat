package importer

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"osat/internal/i18n"
	"osat/internal/mongouri"
	"osat/internal/store"
)

// Compass: archivo de conexiones exportado desde MongoDB Compass (Connections ›
// Export saved connections, o mongodb-compass --export-connections). Es EJSON:
//
//	{"type": "Compass Connections", "version": 1, "connections": [
//	  {"id": "…", "favorite": {"name": "…"}, "connectionOptions": {
//	    "connectionString": "mongodb://usuario:contraseña@host/…",
//	    "sshTunnel": {"host": "…", "port": 22, "username": "…", "password": "…"}}}]}
//
// Si se exportó con passphrase, las contraseñas no están en la cadena ni en el
// túnel sino cifradas en "connectionSecrets" (ver compassDecrypter).
const Compass = "compass"

const compassFileType = "Compass Connections"

type compassFile struct {
	Type        string        `json:"type"`
	Version     ejsonInt      `json:"version"`
	Connections []compassConn `json:"connections"`
}

type compassConn struct {
	Favorite *struct {
		Name string `json:"name"`
	} `json:"favorite"`
	ConnectionOptions struct {
		ConnectionString         string      `json:"connectionString"`
		SSHTunnel                *compassSSH `json:"sshTunnel"`
		UseApplicationLevelProxy bool        `json:"useApplicationLevelProxy"`
		FLEOptions               *struct {
			AutoEncryption *struct {
				KeyVaultNamespace string `json:"keyVaultNamespace"`
			} `json:"autoEncryption"`
		} `json:"fleOptions"`
	} `json:"connectionOptions"`
	ConnectionSecrets string `json:"connectionSecrets"` // base64, cifrado con la passphrase
}

type compassSSH struct {
	Host                  string   `json:"host"`
	Port                  ejsonInt `json:"port"`
	Username              string   `json:"username"`
	Password              string   `json:"password"`
	IdentityKeyFile       string   `json:"identityKeyFile"`
	IdentityKeyPassphrase string   `json:"identityKeyPassphrase"`
}

// compassSecrets es lo que va cifrado en connectionSecrets ({"secrets": {…}}).
type compassSecrets struct {
	Password                      string `json:"password"`
	SSHTunnelPassword             string `json:"sshTunnelPassword"`
	SSHTunnelPassphrase           string `json:"sshTunnelPassphrase"`
	AWSSessionToken               string `json:"awsSessionToken"`
	TLSCertificateKeyFilePassword string `json:"tlsCertificateKeyFilePassword"`
	ProxyPassword                 string `json:"proxyPassword"`
}

// ejsonInt es un número de EJSON: 22, "22" o {"$numberInt": "22"} (también
// $numberLong y $numberDouble). Compass exporta en modo canónico.
type ejsonInt int

func (n *ejsonInt) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if bytes.Equal(b, []byte("null")) {
		return nil
	}
	var wrapped map[string]json.RawMessage
	if len(b) > 0 && b[0] == '{' {
		if err := json.Unmarshal(b, &wrapped); err != nil {
			return err
		}
		for _, k := range []string{"$numberInt", "$numberLong", "$numberDouble"} {
			if v, ok := wrapped[k]; ok {
				b = v
				break
			}
		}
	}
	s := strings.Trim(string(b), `"`)
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("número inválido: %s", s)
	}
	*n = ejsonInt(f)
	return nil
}

func scanCompass(path, passphrase string, existing []store.Connection) (Result, error) {
	if path == "" {
		// Compass guarda sus conexiones cifradas con el llavero del sistema:
		// solo se puede leer un archivo exportado.
		return Result{}, ErrNotFound
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	items, skipped, err := compassItems(b, passphrase, existing)
	if err != nil {
		return Result{}, err
	}
	return Result{Location: path, Items: items, Skipped: skipped}, nil
}

// compassItems traduce las conexiones del archivo. Las que pasan por un túnel
// SSH llevan como salto una conexión SSH: la que ya existe en osat con el
// mismo host, puerto y usuario, o una nueva (una sola por túnel).
func compassItems(b []byte, passphrase string, existing []store.Connection) ([]Item, []string, error) {
	var f compassFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, nil, i18n.Err("mongo.compassInvalidJSON", err)
	}
	if f.Type != compassFileType {
		return nil, nil, i18n.Err("mongo.compassBadFile")
	}
	if f.Version != 1 {
		return nil, nil, i18n.Err("mongo.compassBadVersion", int(f.Version))
	}
	encrypted := false
	for _, c := range f.Connections {
		encrypted = encrypted || c.ConnectionSecrets != ""
	}
	if encrypted && passphrase == "" {
		return nil, nil, ErrMasterPassword
	}
	dec := &compassDecrypter{passphrase: passphrase, keys: map[string][]byte{}}

	// Saltos SSH que ya están en osat, por host|puerto|usuario.
	sshKey := func(host string, port int, user string) string {
		return fmt.Sprintf("%s|%d|%s", strings.ToLower(host), port, user)
	}
	have := map[string]string{} // → nombre
	for _, c := range existing {
		if store.IsSSH(c.Protocol) {
			have[sshKey(c.Host, c.Port, c.User)] = c.Name
		}
	}
	tunnels := map[string]string{} // túneles nuevos de este archivo → nombre de su Item

	var items []Item
	var skipped []string
	for i, c := range f.Connections {
		name := ""
		if c.Favorite != nil {
			name = strings.TrimSpace(c.Favorite.Name)
		}
		opts := c.ConnectionOptions
		if strings.TrimSpace(opts.ConnectionString) == "" {
			skipped = append(skipped, i18n.T("mongo.compassNoURI", orNumber(name, i)))
			continue
		}
		u, err := mongouri.Parse(opts.ConnectionString)
		if err != nil {
			skipped = append(skipped, i18n.T("mongo.compassBadURI", orNumber(name, i), err))
			continue
		}
		if name == "" {
			name, _ = u.First()
		}
		var sec compassSecrets
		if c.ConnectionSecrets != "" {
			if sec, err = dec.secrets(c.ConnectionSecrets); err != nil {
				return nil, nil, err
			}
		}
		password := u.Password
		if sec.Password != "" {
			password = sec.Password
		}
		u.Password = ""
		host, port := u.First()
		it := Item{
			Name: name, Group: "Compass", Protocol: store.ProtoMongo, URI: u.Redacted(),
			Host: host, Port: port, User: u.User, AuthType: store.AuthPassword, Secret: password,
		}

		switch mech, _ := u.Option("authMechanism"); strings.ToUpper(mech) {
		case "MONGODB-OIDC":
			skipped = append(skipped, i18n.T("mongo.compassOIDC", name))
		case "GSSAPI", "MONGODB-AWS":
			skipped = append(skipped, i18n.T("mongo.compassAuthMechanism", name, mech))
		}
		if fle := opts.FLEOptions; fle != nil && fle.AutoEncryption != nil && fle.AutoEncryption.KeyVaultNamespace != "" {
			skipped = append(skipped, i18n.T("mongo.compassFLE", name))
		}
		if opts.UseApplicationLevelProxy {
			skipped = append(skipped, i18n.T("mongo.compassProxy", name))
		}
		for what, v := range map[string]string{"TLS": sec.TLSCertificateKeyFilePassword, "proxy": sec.ProxyPassword, "AWS": sec.AWSSessionToken} {
			if v != "" {
				skipped = append(skipped, i18n.T("mongo.compassOtherSecret", name, what))
			}
		}

		if t := opts.SSHTunnel; t != nil {
			th := strings.TrimSpace(t.Host)
			if th == "" {
				skipped = append(skipped, i18n.T("mongo.compassSSHNoHost", name))
			} else {
				tp := int(t.Port)
				if tp <= 0 || tp > 65535 {
					tp = 22
				}
				key := sshKey(th, tp, t.Username)
				switch {
				case have[key] != "":
					it.Jump = have[key]
				case tunnels[key] != "":
					it.Jump = tunnels[key]
				default:
					ssh := Item{Name: th, Group: "Compass", Protocol: store.ProtoSSH, Host: th, Port: tp, User: t.Username, AuthType: store.AuthPassword}
					if t.Username != "" {
						ssh.Name = t.Username + "@" + th
					}
					ssh.Secret = firstNonEmpty(sec.SSHTunnelPassword, t.Password)
					if t.IdentityKeyFile != "" {
						ssh.AuthType, ssh.KeyPath = store.AuthKey, t.IdentityKeyFile
						ssh.Secret = firstNonEmpty(sec.SSHTunnelPassphrase, t.IdentityKeyPassphrase)
					}
					tunnels[key] = ssh.Name
					it.Jump = ssh.Name
					items = append(items, ssh)
				}
			}
		}
		items = append(items, it)
	}
	return items, skipped, nil
}

func orNumber(name string, i int) string {
	if name != "" {
		return name
	}
	return "#" + strconv.Itoa(i+1)
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// compassDecrypter descifra connectionSecrets como Compass
// (packages/connection-storage/src/encrypt.ts): versión (2 bytes, 0x0001) ||
// sal (16) || iv (16) || tag (16) || datos, en base64; la clave sale de la
// passphrase con PBKDF2-SHA512, 100.000 iteraciones, 32 bytes; AES-256-GCM con
// un nonce de 16 bytes. La clave se guarda por sal (cada exportación usa una).
type compassDecrypter struct {
	passphrase string
	keys       map[string][]byte
}

const (
	compassIterations = 100_000
	compassSaltLen    = 16
	compassIVLen      = 16
	compassTagLen     = 16
)

func (d *compassDecrypter) secrets(enc string) (compassSecrets, error) {
	plain, err := d.decrypt(enc)
	if err != nil {
		return compassSecrets{}, err
	}
	var wrap struct {
		Secrets *compassSecrets `json:"secrets"`
	}
	if err := json.Unmarshal(plain, &wrap); err != nil || wrap.Secrets == nil {
		return compassSecrets{}, i18n.Err("mongo.compassBadSecrets")
	}
	return *wrap.Secrets, nil
}

func (d *compassDecrypter) decrypt(enc string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(enc))
	head := 2 + compassSaltLen + compassIVLen + compassTagLen
	if err != nil || len(raw) < head || binary.BigEndian.Uint16(raw) != 1 {
		return nil, i18n.Err("mongo.compassBadSecrets")
	}
	salt := raw[2 : 2+compassSaltLen]
	iv := raw[2+compassSaltLen : 2+compassSaltLen+compassIVLen]
	tag := raw[2+compassSaltLen+compassIVLen : head]
	data := raw[head:]

	key, ok := d.keys[string(salt)]
	if !ok {
		if key, err = pbkdf2.Key(sha512.New, d.passphrase, salt, compassIterations, 32); err != nil {
			return nil, err
		}
		d.keys[string(salt)] = key
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCMWithNonceSize(block, compassIVLen)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, iv, append(append([]byte{}, data...), tag...), nil)
	if err != nil { // passphrase equivocada (o datos alterados)
		return nil, ErrBadMasterPassword
	}
	return plain, nil
}

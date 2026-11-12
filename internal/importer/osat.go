package importer

import (
	"os"
	"sort"
	"strings"

	"osat/internal/osatfile"
	"osat/internal/store"
)

// Osat: archivo .osat exportado desde otro osat (cifrado con contraseña).
const Osat = "osat"

// ScanOsat descifra un archivo .osat y lista sus conexiones. Devuelve también
// las entradas completas (con contraseñas y claves) para darlas de alta
// después: Item.Ref es la posición de cada una. Los snippets del archivo van
// aparte y solo se cuentan en Result.Snippets. Los sitios y las credenciales por defecto también van aparte.
func ScanOsat(path, password string, existing []store.Connection) (Result, []osatfile.Entry, []osatfile.SnippetEntry, []osatfile.SiteEntry, *osatfile.DefaultsEntry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Result{}, nil, nil, nil, nil, err
	}
	entries, snippets, sites, defaults, err := osatfile.Open(b, password)
	if err != nil {
		return Result{}, nil, nil, nil, nil, err
	}
	r := Result{Source: Osat, Location: path, Items: []Item{}, Skipped: []string{}, Snippets: len(snippets), Sites: len(sites)}
	if defaults != nil {
		r.HasDefaults, r.DefaultsUser = true, defaults.User
	}
	for i, e := range entries {
		r.Items = append(r.Items, Item{
			Name: e.Name, Group: e.Group, Protocol: e.Protocol,
			Host: e.Host, Port: e.Port, User: e.User, Domain: e.Domain,
			AuthType: e.AuthType, KeyPath: e.KeyPath, Jump: e.Jump,
			ForwardAgent: e.ForwardAgent, InitialCommand: e.InitialCommand, Env: e.Env,
			Ref: i, HasSecret: e.Secret != "", HasKey: len(e.Key) > 0,
		})
	}
	markExisting(r.Items, existing)
	sort.SliceStable(r.Items, func(i, j int) bool {
		a, b := r.Items[i], r.Items[j]
		if !strings.EqualFold(a.Group, b.Group) {
			return strings.ToLower(a.Group) < strings.ToLower(b.Group)
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return r, entries, snippets, sites, defaults, nil
}

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"osat/internal/i18n"
	"osat/internal/importer"
	"osat/internal/osatfile"
	"osat/internal/remote"
	"osat/internal/store"
)

// ---------- Exportar / importar archivos .osat ----------
// Para pasarle las conexiones a otro osat: un archivo cifrado con contraseña
// que puede llevar también las contraseñas y las claves privadas completas.

// osatPending: lo descifrado del último .osat abierto, hasta que se importa.
type osatPending struct {
	sync.Mutex
	entries  []osatfile.Entry
	snippets []osatfile.SnippetEntry
	sites    []osatfile.SiteEntry
	defaults *osatfile.DefaultsEntry
}

// ExportConnections pregunta dónde guardar y exporta las conexiones elegidas
// (ids). withSecrets agrega las contraseñas y el contenido de las claves;
// withSnippets y withSites, todos los snippets y sitios web; withDefaults, las
// credenciales por defecto con su contraseña. Puede no haber conexiones si va algo más. Devuelve la ruta del archivo ("" = se canceló).
func (a *App) ExportConnections(ids []string, password string, withSecrets, withSnippets, withSites, withDefaults bool) (string, error) {
	if password == "" {
		return "", i18n.Err("app.thePasswordIsRequired")
	}
	entries, err := a.exportEntries(ids, withSecrets)
	if err != nil {
		return "", err
	}
	var snippets []osatfile.SnippetEntry
	if withSnippets {
		snippets = a.exportSnippets()
	}
	var sites []osatfile.SiteEntry
	if withSites {
		sites = a.exportSites()
	}
	var defaults *osatfile.DefaultsEntry
	if d := a.store.Defaults(); withDefaults && (d.User != "" || d.HasSecret) {
		defaults = &osatfile.DefaultsEntry{User: d.User, Domain: d.Domain, Secret: a.store.DefaultSecret()}
	}
	if len(entries)+len(snippets)+len(sites) == 0 && defaults == nil {
		return "", i18n.Err("app.thereIsNothingToExport")
	}
	path, err := nativeSaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           i18n.T("native.exportTitle"),
		DefaultFilename: i18n.T("native.exportFile"),
		Filters:         []runtime.FileFilter{{DisplayName: i18n.T("native.filterOsat"), Pattern: "*.osat"}},
	})
	if err != nil || path == "" {
		return "", err
	}
	if filepath.Ext(path) == "" {
		path += ".osat"
	}
	data, err := osatfile.Seal(entries, snippets, sites, defaults, password)
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, data, 0o600)
}

// exportEntries arma lo que va en el archivo: las conexiones elegidas (ids)
// y, con withSecrets, sus contraseñas y claves.
func (a *App) exportEntries(ids []string, withSecrets bool) ([]osatfile.Entry, error) {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var entries []osatfile.Entry
	all := a.store.List()
	names := map[string]string{} // id → nombre, para el salto
	for _, c := range all {
		names[c.ID] = c.Name
	}
	for _, c := range all {
		if !want[c.ID] {
			continue
		}
		e := osatfile.Entry{
			Name: c.Name, Group: c.Group, Protocol: c.Protocol, Host: c.Host, Port: c.Port,
			User: c.User, Domain: c.Domain, Gateway: c.Gateway, AuthType: c.AuthType, KeyPath: c.KeyPath, Color: c.Color,
			Jump: names[c.JumpID], ForwardAgent: c.ForwardAgent, InitialCommand: c.InitialCommand, Env: c.Env, Pins: c.Pins,
			UseDefaultCreds: c.UseDefaultCreds,
		}
		if withSecrets {
			if c.HasSecret {
				e.Secret = a.store.Secret(c.ID)
			}
			if c.AuthType == store.AuthKey {
				key, err := os.ReadFile(remote.ExpandHome(c.KeyPath))
				if err != nil {
					return nil, i18n.Err("app.sCouldnTReadThe", c.Name, err)
				}
				e.Key, e.KeyName = key, filepath.Base(c.KeyPath)
			}
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// exportSnippets arma todos los snippets (los globales y los de cada grupo).
func (a *App) exportSnippets() []osatfile.SnippetEntry {
	var out []osatfile.SnippetEntry
	for _, sn := range a.store.Snippets() {
		out = append(out, osatfile.SnippetEntry{Name: sn.Name, Folder: sn.Folder, Command: sn.Command, Mode: sn.Mode, Group: sn.Group})
	}
	return out
}

// exportSites arma los sitios web guardados; van todos, no son de ninguna conexión.
func (a *App) exportSites() []osatfile.SiteEntry {
	var out []osatfile.SiteEntry
	for _, st := range a.store.Sites() {
		out = append(out, osatfile.SiteEntry{Name: st.Name, URL: st.URL})
	}
	return out
}

// ImportOsatScan descifra un .osat y lista sus conexiones para elegir cuáles importar.
func (a *App) ImportOsatScan(path, password string) (importer.Result, error) {
	r, entries, snippets, sites, defaults, err := importer.ScanOsat(path, password, a.store.List())
	if err != nil {
		return importer.Result{}, err
	}
	a.osat.Lock()
	a.osat.entries, a.osat.snippets, a.osat.sites, a.osat.defaults = entries, snippets, sites, defaults
	a.osat.Unlock()
	return r, nil
}

// ImportOsat da de alta las conexiones elegidas del último .osat abierto, con
// sus contraseñas (al keyring de este equipo) y sus claves (a la carpeta keys
// de la configuración de osat). También da de alta los snippets del archivo
// y los sitios web (los que ya existen no se duplican). Devuelve cuántas conexiones se guardaron.
func (a *App) ImportOsat(items []importer.Item) (int, error) {
	a.osat.Lock()
	entries, snippets, sites := a.osat.entries, a.osat.snippets, a.osat.sites
	a.osat.Unlock()
	keyDir := filepath.Join(a.store.Dir(), "keys")
	n := 0
	var errs []error
	var saved []importedConn
	for _, it := range items {
		if it.Ref < 0 || it.Ref >= len(entries) {
			errs = append(errs, i18n.Err("app.sItIsNotIn", it.Name))
			continue
		}
		e := entries[it.Ref]
		keyPath := e.KeyPath
		if len(e.Key) > 0 {
			p, err := osatfile.SaveKey(keyDir, e.KeyName, e.Key)
			if err != nil {
				errs = append(errs, i18n.Err("app.sCouldnTSaveThe", it.Name, err))
				continue
			}
			keyPath = p
		}
		c, err := a.store.Save(store.ConnectionInput{Connection: store.Connection{
			Name: it.Name, Group: it.Group, Protocol: e.Protocol,
			Host: e.Host, Port: e.Port, User: e.User, Domain: e.Domain, Gateway: e.Gateway,
			AuthType: e.AuthType, KeyPath: keyPath, Color: e.Color,
			ForwardAgent: e.ForwardAgent, InitialCommand: e.InitialCommand, Env: e.Env, Pins: e.Pins,
			UseDefaultCreds: e.UseDefaultCreds,
		}, Secret: e.Secret})
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", it.Name, err))
			continue
		}
		saved = append(saved, importedConn{c, e.Jump})
		n++
	}
	errs = append(errs, a.linkJumps(saved)...)
	imported := make([]store.Snippet, len(snippets))
	for i, sn := range snippets {
		imported[i] = store.Snippet{Name: sn.Name, Folder: sn.Folder, Command: sn.Command, Mode: sn.Mode, Group: sn.Group}
	}
	if _, err := a.store.ImportSnippets(imported); err != nil {
		errs = append(errs, err)
	}
	importedSites := make([]store.Site, len(sites))
	for i, st := range sites {
		importedSites[i] = store.Site{Name: st.Name, URL: st.URL}
	}
	if _, err := a.store.ImportSites(importedSites); err != nil {
		errs = append(errs, err)
	}
	return n, errors.Join(errs...)
}

// ImportOsatDefaults reemplaza las credenciales por defecto por las del último
// .osat abierto (si las trae).
func (a *App) ImportOsatDefaults() error {
	a.osat.Lock()
	d := a.osat.defaults
	a.osat.Unlock()
	if d == nil {
		return nil
	}
	_, err := a.store.SaveDefaults(store.DefaultCredsInput{DefaultCreds: store.DefaultCreds{User: d.User, Domain: d.Domain}, Secret: d.Secret})
	return err
}

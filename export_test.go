package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"osat/internal/osatfile"
	"osat/internal/store"
)

// newExportApp arma un osat con su configuración en una carpeta temporal (como
// si fuera otra PC) y el keyring en memoria.
func newExportApp(t *testing.T) *App {
	t.Helper()
	keyring.MockInit()
	tmp := t.TempDir()
	t.Setenv("AppData", tmp)         // os.UserConfigDir en Windows
	t.Setenv("XDG_CONFIG_HOME", tmp) // en Linux
	t.Setenv("HOME", tmp)            // en macOS ($HOME/Library/Application Support)
	st, err := store.New()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(st.Dir(), tmp) {
		t.Fatalf("la configuración quedó en %s y no en la carpeta temporal", st.Dir())
	}
	return &App{store: st}
}

func mustSave(t *testing.T, a *App, c store.Connection, secret string) {
	t.Helper()
	if _, err := a.store.Save(store.ConnectionInput{Connection: c, Secret: secret}); err != nil {
		t.Fatal(err)
	}
}

func byName(a *App) map[string]store.Connection {
	m := map[string]store.Connection{}
	for _, c := range a.store.List() {
		m[c.Name] = c
	}
	return m
}

func idsOf(a *App, names ...string) []string {
	m := byName(a)
	var ids []string
	for _, n := range names {
		ids = append(ids, m[n].ID)
	}
	return ids
}

// exportFile hace lo mismo que ExportConnections, sin el diálogo de guardar.
func exportFile(t *testing.T, a *App, ids []string, password string, withSecrets bool) string {
	t.Helper()
	entries, err := a.exportEntries(ids, withSecrets)
	if err != nil {
		t.Fatal(err)
	}
	data, err := osatfile.Seal(entries, a.exportSnippets(), a.exportSites(), nil, password)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "equipo.osat")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExportImportWithSecretsAndKeys(t *testing.T) {
	src := newExportApp(t)
	ppk := []byte("PuTTY-User-Key-File-3: ssh-ed25519\nEncryption: none\n...")
	ppkPath := filepath.Join(t.TempDir(), "web.ppk")
	if err := os.WriteFile(ppkPath, ppk, 0o600); err != nil {
		t.Fatal(err)
	}
	mustSave(t, src, store.Connection{Name: "web", Group: "Prod/Web", Host: "10.0.0.1", User: "root",
		AuthType: store.AuthKey, KeyPath: ppkPath, Color: "#e5534b"}, "frase de la clave")
	mustSave(t, src, store.Connection{Name: "dc", Group: "Prod", Protocol: store.ProtoRDP, Host: "dc01", User: "admin",
		Domain: "CORP", Gateway: "rds.corp"}, "pw-dc")
	mustSave(t, src, store.Connection{Name: "lab", Group: "Lab", Host: "lab", Port: 2222, User: "u", AuthType: store.AuthAgent,
		ForwardAgent: true, InitialCommand: "tmux a", Env: []string{"LANG=es_AR.UTF-8", "A=b=c"}}, "")

	path := exportFile(t, src, idsOf(src, "web", "dc", "lab"), "clave del equipo", true)
	os.Remove(ppkPath) // en la otra PC la clave original no existe

	dst := newExportApp(t)
	if _, err := dst.ImportOsatScan(path, "otra clave"); err == nil {
		t.Fatal("abrió el archivo con una contraseña incorrecta")
	}
	r, err := dst.ImportOsatScan(path, "clave del equipo")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Items) != 3 {
		t.Fatalf("items = %d, quería 3", len(r.Items))
	}
	n, err := dst.ImportOsat(r.Items)
	if err != nil || n != 3 {
		t.Fatalf("ImportOsat = %d, %v", n, err)
	}

	got := byName(dst)
	web := got["web"]
	if web.Group != "Prod/Web" || web.Host != "10.0.0.1" || web.AuthType != store.AuthKey || web.Color != "#e5534b" {
		t.Fatalf("web = %+v", web)
	}
	if want := filepath.Join(dst.store.Dir(), "keys"); filepath.Dir(web.KeyPath) != want || filepath.Ext(web.KeyPath) != ".ppk" {
		t.Fatalf("clave en %s, quería un .ppk en %s", web.KeyPath, want)
	}
	if b, err := os.ReadFile(web.KeyPath); err != nil || string(b) != string(ppk) {
		t.Fatalf("clave importada = %q, %v", b, err)
	}
	if s := dst.store.Secret(web.ID); s != "frase de la clave" {
		t.Fatalf("passphrase = %q", s)
	}
	dc := got["dc"]
	if dc.Protocol != store.ProtoRDP || dc.Domain != "CORP" || dc.Gateway != "rds.corp" || dc.Port != 3389 || dst.store.Secret(dc.ID) != "pw-dc" {
		t.Fatalf("dc = %+v, secreto %q", dc, dst.store.Secret(dc.ID))
	}
	lab := got["lab"]
	if lab.AuthType != store.AuthAgent || lab.Port != 2222 || lab.HasSecret {
		t.Fatalf("lab = %+v", lab)
	}
	if !lab.ForwardAgent || lab.InitialCommand != "tmux a" || !reflect.DeepEqual(lab.Env, []string{"LANG=es_AR.UTF-8", "A=b=c"}) {
		t.Fatalf("campos avanzados de lab = %+v", lab)
	}
	if web.ForwardAgent || web.InitialCommand != "" || web.Env != nil {
		t.Fatalf("web no debería tener campos avanzados: %+v", web)
	}

	// Abrirlo de nuevo las marca como ya existentes.
	r, err = dst.ImportOsatScan(path, "clave del equipo")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range r.Items {
		if !it.Exists {
			t.Fatalf("%s no figura como existente", it.Name)
		}
	}
}

func TestExportSelectedWithoutSecrets(t *testing.T) {
	src := newExportApp(t)
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	os.WriteFile(keyPath, []byte("-----BEGIN OPENSSH PRIVATE KEY-----"), 0o600)
	mustSave(t, src, store.Connection{Name: "web", Group: "Prod/Web", Host: "w", User: "root", AuthType: store.AuthKey, KeyPath: keyPath}, "frase")
	mustSave(t, src, store.Connection{Name: "db", Group: "Lab", Host: "d", User: "root"}, "pw")
	mustSave(t, src, store.Connection{Name: "otro", Group: "Prod/Web", Host: "o", User: "root"}, "pw")
	mustSave(t, src, store.Connection{Name: "suelta", Host: "l", User: "u"}, "pw")

	// Solo las elegidas, de grupos distintos.
	entries, err := src.exportEntries(idsOf(src, "web", "db"), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("exportó %d, quería web y db", len(entries))
	}
	for _, e := range entries {
		if e.Secret != "" || len(e.Key) > 0 {
			t.Fatalf("%s lleva secretos sin la casilla", e.Name)
		}
	}
	// Sin conexiones elegidas no hay entradas (el archivo puede llevar solo snippets y sitios).
	if e, err := src.exportEntries(nil, true); err != nil || len(e) != 0 {
		t.Fatalf("sin nada elegido: %d, %v", len(e), err)
	}

	path := exportFile(t, src, idsOf(src, "web", "db"), "clave del equipo", false)
	dst := newExportApp(t)
	r, err := dst.ImportOsatScan(path, "clave del equipo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dst.ImportOsat(r.Items); err != nil {
		t.Fatal(err)
	}
	got := byName(dst)
	if len(got) != 2 {
		t.Fatalf("importó %d, quería 2", len(got))
	}
	if got["web"].KeyPath != keyPath || got["web"].HasSecret || got["db"].HasSecret {
		t.Fatalf("web = %+v, db = %+v", got["web"], got["db"])
	}
	if _, err := os.Stat(filepath.Join(dst.store.Dir(), "keys")); err == nil {
		t.Fatal("guardó claves sin que el archivo las traiga")
	}
}

func TestExportImportPinsButNotFavorite(t *testing.T) {
	src := newExportApp(t)
	mustSave(t, src, store.Connection{Name: "web", Host: "w", User: "root"}, "pw")
	id := idsOf(src, "web")[0]
	if err := src.store.PinDir(id, "/var/log"); err != nil {
		t.Fatal(err)
	}
	if err := src.store.RenamePin(id, "/var/log", "Logs"); err != nil {
		t.Fatal(err)
	}
	if err := src.store.SetFavorite(id, true); err != nil {
		t.Fatal(err)
	}

	path := exportFile(t, src, []string{id}, "clave del equipo", false)
	dst := newExportApp(t)
	r, err := dst.ImportOsatScan(path, "clave del equipo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dst.ImportOsat(r.Items); err != nil {
		t.Fatal(err)
	}
	got := byName(dst)["web"]
	if len(got.Pins) != 1 || got.Pins[0] != (store.PinnedDir{Path: "/var/log", Name: "Logs"}) {
		t.Fatalf("pins = %+v", got.Pins)
	}
	if got.Favorite {
		t.Fatal("la marca de favorito viajó en el .osat")
	}
}

func TestExportFailsOnMissingKey(t *testing.T) {
	a := newExportApp(t)
	mustSave(t, a, store.Connection{Name: "web", Host: "w", User: "root", AuthType: store.AuthKey,
		KeyPath: filepath.Join(t.TempDir(), "no-existe.ppk")}, "")
	_, err := a.exportEntries(idsOf(a, "web"), true)
	if err == nil || !strings.Contains(err.Error(), "web") {
		t.Fatalf("esperaba un error que nombre a web, fue %v", err)
	}
}

func mustSaveSnippet(t *testing.T, a *App, sn store.Snippet) {
	t.Helper()
	if _, err := a.store.SaveSnippet(sn); err != nil {
		t.Fatal(err)
	}
}

func snippetKeys(a *App) []string {
	var out []string
	for _, sn := range a.store.Snippets() {
		out = append(out, sn.Group+"|"+sn.Folder+"|"+sn.Name+"|"+sn.Mode+"|"+sn.Command)
	}
	return out
}

// Los snippets viajan todos en el .osat, aunque no vaya ninguna conexión de su grupo
// (o ninguna conexión). Al importar no se duplican.
func TestExportImportSnippets(t *testing.T) {
	src := newExportApp(t)
	mustSave(t, src, store.Connection{Name: "web", Group: "Prod/Web", Host: "w", User: "root"}, "pw")
	mustSaveSnippet(t, src, store.Snippet{Name: "uptime", Command: "uptime", Mode: store.SnippetPaste})
	mustSaveSnippet(t, src, store.Snippet{Name: "reiniciar", Folder: "Web/Nginx", Command: "sudo systemctl restart nginx\nsystemctl status nginx",
		Mode: store.SnippetRun, Group: "Prod"})
	mustSaveSnippet(t, src, store.Snippet{Name: "lab", Command: "make", Mode: store.SnippetRun, Group: "Lab"})
	want := snippetKeys(src)

	for _, ids := range [][]string{idsOf(src, "web"), nil} {
		path := exportFile(t, src, ids, "pw", false)
		dst := newExportApp(t)
		r, err := dst.ImportOsatScan(path, "pw")
		if err != nil {
			t.Fatal(err)
		}
		if r.Snippets != len(want) {
			t.Fatalf("%d conexiones: el archivo trae %d snippets, quería %d", len(ids), r.Snippets, len(want))
		}
		if n, err := dst.ImportOsat(r.Items); err != nil || n != len(ids) {
			t.Fatalf("ImportOsat = %d, %v", n, err)
		}
		if got := snippetKeys(dst); !reflect.DeepEqual(got, want) {
			t.Fatalf("importó %q, quería %q", got, want)
		}
		// Importar el mismo archivo otra vez no duplica nada.
		if _, err := dst.ImportOsat(r.Items); err != nil {
			t.Fatal(err)
		}
		if got := snippetKeys(dst); !reflect.DeepEqual(got, want) {
			t.Fatalf("la segunda importación cambió los snippets: %q", got)
		}
	}
}

// Los sitios web viajan en el .osat. Al importar se agregan al final y uno con
// la misma URL que uno existente no se duplica, ni importando dos veces.
func TestExportImportSites(t *testing.T) {
	src := newExportApp(t)
	mustSave(t, src, store.Connection{Name: "web", Host: "w", User: "root"}, "pw")
	for _, st := range []store.Site{{Name: "Hosting", URL: "https://panel.example.com"}, {Name: "Nube", URL: "http://nube.local:8080/c"}} {
		if _, err := src.SiteSave(st); err != nil {
			t.Fatal(err)
		}
	}
	path := exportFile(t, src, idsOf(src, "web"), "pw", false)

	siteKeys := func(a *App) []string {
		var out []string
		for _, st := range a.SitesList() {
			out = append(out, st.Name+"|"+st.URL)
		}
		return out
	}
	dst := newExportApp(t)
	if _, err := dst.SiteSave(store.Site{Name: "Ya estaba", URL: "https://panel.example.com"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		r, err := dst.ImportOsatScan(path, "pw")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := dst.ImportOsat(r.Items); err != nil {
			t.Fatal(err)
		}
		want := []string{"Ya estaba|https://panel.example.com", "Nube|http://nube.local:8080/c"}
		if got := siteKeys(dst); !reflect.DeepEqual(got, want) {
			t.Fatalf("importación %d: sitios = %q, quería %q", i+1, got, want)
		}
	}
}

// El flag de las credenciales por defecto viaja en el .osat; las credenciales
// en sí, solo si se eligió incluirlas, y se aplican aparte (ImportOsatDefaults).
func TestExportImportDefaultCreds(t *testing.T) {
	src := newExportApp(t)
	if _, err := src.store.SaveDefaults(store.DefaultCredsInput{DefaultCreds: store.DefaultCreds{User: "admin", Domain: "CORP"}, Secret: "pw-default"}); err != nil {
		t.Fatal(err)
	}
	mustSave(t, src, store.Connection{Name: "web", Host: "h", AuthType: store.AuthPassword, UseDefaultCreds: true}, "")
	entries, err := src.exportEntries(idsOf(src, "web"), false)
	if err != nil {
		t.Fatal(err)
	}
	d := src.store.Defaults()
	data, err := osatfile.Seal(entries, nil, nil, &osatfile.DefaultsEntry{User: d.User, Domain: d.Domain, Secret: src.store.DefaultSecret()}, "pw")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "equipo.osat")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	dst := newExportApp(t)
	r, err := dst.ImportOsatScan(path, "pw")
	if err != nil || !r.HasDefaults || r.DefaultsUser != "admin" {
		t.Fatalf("scan = %+v, %v", r, err)
	}
	// Sin usuario propio y sin credenciales por defecto todavía: no se puede guardar.
	if n, err := dst.ImportOsat(r.Items); err == nil || n != 0 {
		t.Fatalf("antes de las credenciales por defecto: %d, %v", n, err)
	}
	if dst.store.Defaults().User != "" {
		t.Fatal("ImportOsat no debe tocar las credenciales por defecto")
	}
	if err := dst.ImportOsatDefaults(); err != nil {
		t.Fatal(err)
	}
	if got := dst.store.Defaults(); got.User != "admin" || got.Domain != "CORP" || dst.store.DefaultSecret() != "pw-default" {
		t.Fatalf("defaults importadas = %+v %q", got, dst.store.DefaultSecret())
	}
	if n, err := dst.ImportOsat(r.Items); err != nil || n != 1 || !byName(dst)["web"].UseDefaultCreds {
		t.Fatalf("import = %d, %v, %+v", n, err, byName(dst)["web"])
	}
}

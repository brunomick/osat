package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"github.com/zalando/go-keyring"

	"osat/internal/applock"
	"osat/internal/applog"
	"osat/internal/browser"
	"osat/internal/i18n"
	"osat/internal/importer"
	"osat/internal/localfs"
	"osat/internal/localterm"
	"osat/internal/osdrag"
	"osat/internal/portfwd"
	"osat/internal/rdgateway"
	"osat/internal/rdpproxy"
	"osat/internal/remote"
	"osat/internal/settings"
	"osat/internal/store"
	"osat/internal/vncproxy"
	"osat/internal/web"
)

// App expone al frontend todos los métodos (bindings de Wails).
type App struct {
	ctx   context.Context
	store *store.Store
	hk    *remote.HostKeys
	mgr   *remote.Manager
	rdp   *rdpproxy.Proxy
	vnc   *vncproxy.Proxy
	lock  *applock.Lock // bloqueo con contraseña (app_lock.go)

	browser browser.Engine // navegador nativo de los sitios web (app_sites.go); nil si nunca se creó
	bstate  browserState

	bridge *bridgeState // puente con Claude Code (bridge.go)
	osat   osatPending  // .osat abierto para importar (export.go)
	feLog  frontendLog  // errores de JavaScript que llegan al log (frontendlog.go)

	settingsMu sync.Mutex
	settings   settings.Settings // ajustes en uso (settings.json)

	// Contraseñas del último ImportScan (por Item.Ref), hasta importar.
	importSecrets struct {
		sync.Mutex
		list []string
	}
}

// NewApp arma la app con los ajustes que main ya leyó de settings.json.
func NewApp(cfg settings.Settings) (*App, error) {
	st, err := store.New()
	if err != nil {
		return nil, err
	}
	hk, err := remote.NewHostKeys(filepath.Join(st.Dir(), "known_hosts"))
	if err != nil {
		return nil, err
	}
	rp, err := rdpproxy.New(filepath.Join(st.Dir(), "rdp_known_certs"))
	if err != nil {
		return nil, err
	}
	a := &App{store: st, hk: hk, rdp: rp, vnc: vncproxy.New(), settings: cfg, lock: applock.New(keyring.Get, keyring.Set, keyring.Delete)}
	emit := func(event string, data ...interface{}) {
		if a.ctx != nil {
			nativeEventsEmit(a.ctx, event, data...)
		}
	}
	a.mgr = remote.NewManager(st, hk, emit)
	a.initBrowser(st.Dir(), emit)
	remote.SetOptions(termOptions(cfg))
	a.initBridge(st.Dir())
	return a, nil
}

// termOptions arma los ajustes que usa el backend de las terminales (valen para las sesiones nuevas).
func termOptions(s settings.Settings) remote.Options {
	o := remote.Options{KeepAlive: time.Duration(s.Terminal.KeepAlive) * time.Second, TermType: s.Terminal.TermType}
	if s.Terminal.SessionLog {
		o.LogDir = s.Terminal.LogDir
	}
	return o
}

func (a *App) startup(ctx context.Context) { a.ctx = ctx }
func (a *App) shutdown(ctx context.Context) {
	a.bridge.srv.Stop()
	a.closeBrowser()
	a.mgr.CloseAll()
	a.rdp.CloseAll()
	a.vnc.CloseAll()
	a.feLog.flush()
	slog.Info("cierre")
}

// ---------- Ajustes ----------

// SettingsGet devuelve los ajustes en uso.
func (a *App) SettingsGet() settings.Settings {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	return a.settings
}

// SettingsDefaults devuelve los valores por defecto (para "Restaurar valores por defecto").
func (a *App) SettingsDefaults() settings.Settings { return settings.Defaults() }

// SettingsSave guarda los ajustes en settings.json y devuelve lo que quedó guardado
// (normalizado: lo que estaba fuera de rango vuelve a su valor por defecto).
func (a *App) SettingsSave(s settings.Settings) (settings.Settings, error) {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	saved, err := settings.Save(a.store.Dir(), s)
	if err != nil {
		slog.Warn("no se pudieron guardar los ajustes", "err", err)
		return a.settings, err
	}
	if saved.General.VerboseLog != a.settings.General.VerboseLog {
		applog.SetVerbose(saved.General.VerboseLog)
		slog.Info("registro detallado", "enabled", saved.General.VerboseLog)
	}
	remote.SetOptions(termOptions(saved))
	webBrowserChanged := saved.General.WebBrowser != a.settings.General.WebBrowser
	a.settings = saved
	if webBrowserChanged {
		a.setBrowser(saved.General.WebBrowser)
	}
	return saved, nil
}

// SetLanguage cambia el idioma de los textos que arma Go ("es" o "en"; el frontend ya resolvió "system").
func (a *App) SetLanguage(lang string) { i18n.SetLanguage(lang) }

// LogFrontendError registra en el log un error de JavaScript que nadie atrapó.
func (a *App) LogFrontendError(message, stack string) { a.feLog.record(message, stack) }

// ---------- Conexiones ----------

func (a *App) ListConnections() []store.Connection { return a.store.List() }

func (a *App) SaveConnection(in store.ConnectionInput) (store.Connection, error) {
	return a.store.Save(in)
}

func (a *App) DeleteConnection(id string) error {
	for _, f := range a.store.Forwards(id) {
		a.mgr.StopForward(f.ID)
	}
	return a.store.Delete(id)
}

// ---------- Grupos (carpetas anidadas: "Producción/Web") ----------

// ListGroups devuelve los grupos creados a mano; los que tienen servidores
// ya salen de ListConnections.
func (a *App) ListGroups() []string { return a.store.Groups() }

func (a *App) AddGroup(path string) error { return a.store.AddGroup(path) }

// MoveGroup mueve o renombra el grupo from a la ruta to, con todo su contenido.
func (a *App) MoveGroup(from, to string) error { return a.store.MoveGroup(from, to) }

// DeleteConnectionsIn elimina el grupo group con sus subgrupos y servidores
// (con sus contraseñas y reenvíos); group "" = todas las conexiones y grupos.
// Devuelve cuántas conexiones eliminó.
func (a *App) DeleteConnectionsIn(group string) (int, error) {
	g := store.NormalizeGroup(group)
	for _, c := range a.store.List() {
		if g == "" || c.Group == g || strings.HasPrefix(c.Group, g+"/") {
			for _, f := range a.store.Forwards(c.ID) {
				a.mgr.StopForward(f.ID)
			}
		}
	}
	return a.store.DeleteWithin(g)
}

// MoveConnection pasa una conexión a otro grupo ("" = sin grupo).
func (a *App) MoveConnection(id, group string) error { return a.store.MoveConnection(id, group) }

// PlaceGroup mueve el grupo from a to y lo deja antes de su hermano before ("" = al final).
func (a *App) PlaceGroup(from, to, before string) error { return a.store.PlaceGroup(from, to, before) }

// PlaceConnection pasa una conexión a group y la deja antes de before ("" = al final).
func (a *App) PlaceConnection(id, group, before string) error {
	return a.store.PlaceConnection(id, group, before)
}

// PinDir fija una carpeta del servidor en la barra lateral.
func (a *App) PinDir(connID, path string) error { return a.store.PinDir(connID, path) }

// UnpinDir suelta una carpeta fijada.
func (a *App) UnpinDir(connID, path string) error { return a.store.UnpinDir(connID, path) }

// RenamePin le pone nombre a una carpeta fijada ("" = se muestra la ruta).
func (a *App) RenamePin(connID, path, name string) error {
	return a.store.RenamePin(connID, path, name)
}

// SetFavorite agrega o quita una conexión de Favoritos (al agregar, va al final).
func (a *App) SetFavorite(connID string, fav bool) error { return a.store.SetFavorite(connID, fav) }

// PlaceFavorite deja la favorita antes de beforeID ("" = al final de Favoritos).
func (a *App) PlaceFavorite(connID, beforeID string) error {
	return a.store.PlaceFavorite(connID, beforeID)
}

// ---------- Credenciales por defecto ----------

func (a *App) DefaultCredsGet() store.DefaultCreds { return a.store.Defaults() }

func (a *App) DefaultCredsSave(in store.DefaultCredsInput) (store.DefaultCreds, error) {
	return a.store.SaveDefaults(in)
}

// SetUseDefaultCreds marca o desmarca las conexiones de un grupo ("" = todas)
// para que usen las credenciales por defecto. Devuelve cuántas cambiaron.
func (a *App) SetUseDefaultCreds(group string, on bool) (int, error) {
	return a.store.SetUseDefaultCreds(group, on)
}

// ---------- Snippets (comandos guardados) ----------

func (a *App) SnippetsList() []store.Snippet { return a.store.Snippets() }

// SnippetSave crea (id vacío) o actualiza un snippet.
func (a *App) SnippetSave(sn store.Snippet) (store.Snippet, error) { return a.store.SaveSnippet(sn) }

func (a *App) SnippetDelete(id string) error { return a.store.DeleteSnippet(id) }

// ---------- Espacios de trabajo (pestañas y mosaico; los datos los define el frontend) ----------

// WorkspaceLastGet devuelve la última sesión guardada (null si no hay).
func (a *App) WorkspaceLastGet() json.RawMessage { return a.store.WorkspaceLast() }

// WorkspaceLastSet guarda la sesión actual; data null la borra.
func (a *App) WorkspaceLastSet(data json.RawMessage) error { return a.store.SetWorkspaceLast(data) }

func (a *App) WorkspacesList() []store.Workspace { return a.store.Workspaces() }

// WorkspaceSave crea (id vacío) o actualiza un espacio con nombre.
func (a *App) WorkspaceSave(id, name string, data json.RawMessage) (store.Workspace, error) {
	return a.store.SaveWorkspace(id, name, data)
}

func (a *App) WorkspaceRename(id, name string) error { return a.store.RenameWorkspace(id, name) }

func (a *App) WorkspaceDelete(id string) error { return a.store.DeleteWorkspace(id) }

// ---------- Importar de PuTTY / WinSCP / mRemoteNG ----------

// ImportScan busca las sesiones guardadas de source ("putty", "winscp",
// "mremoteng" u "openssh"). path vacío = la ubicación habitual en este equipo;
// si no, un archivo exportado (.reg, WinSCP.ini, confCons.xml o ~/.ssh/config). master es la
// contraseña maestra de mRemoteNG o WinSCP (si hace falta y falta, error
// NEED_SECRET con kind "master"). Las contraseñas encontradas quedan acá
// hasta importar.
func (a *App) ImportScan(source, path, master string) (importer.Result, error) {
	r, err := importer.Scan(source, path, master, a.store.List())
	if errors.Is(err, importer.ErrMasterPassword) || errors.Is(err, importer.ErrBadMasterPassword) {
		reason := "missing"
		if errors.Is(err, importer.ErrBadMasterPassword) {
			reason = "bad"
		}
		return r, remote.NewError(remote.CodeNeedSecret, err.Error()+".", map[string]string{"kind": "master", "reason": reason})
	}
	if err != nil {
		return r, err
	}
	secrets := make([]string, len(r.Items))
	for i, it := range r.Items {
		secrets[i] = it.Secret
	}
	a.importSecrets.Lock()
	a.importSecrets.list = secrets
	a.importSecrets.Unlock()
	return r, nil
}

// ImportConnections da de alta las sesiones elegidas, con las contraseñas que
// encontró el último ImportScan. Devuelve cuántas se guardaron.
func (a *App) ImportConnections(items []importer.Item) (int, error) {
	a.importSecrets.Lock()
	secrets := a.importSecrets.list
	a.importSecrets.Unlock()
	n := 0
	var errs []error
	var saved []importedConn
	for _, it := range items {
		secret := ""
		if it.HasSecret && it.Ref >= 0 && it.Ref < len(secrets) {
			secret = secrets[it.Ref]
		}
		c, err := a.store.Save(store.ConnectionInput{Connection: store.Connection{
			Name: it.Name, Group: it.Group, Protocol: it.Protocol,
			Host: it.Host, Port: it.Port, User: it.User, Domain: it.Domain, Gateway: it.Gateway,
			AuthType: it.AuthType, KeyPath: it.KeyPath,
			ForwardAgent: it.ForwardAgent, InitialCommand: it.InitialCommand, Env: it.Env,
		}, Secret: secret})
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", it.Name, err))
			continue
		}
		saved = append(saved, importedConn{c, it.Jump})
		n++
	}
	errs = append(errs, a.linkJumps(saved)...)
	return n, errors.Join(errs...)
}

// importedConn es una conexión recién importada y el nombre de su salto.
type importedConn struct {
	conn store.Connection
	jump string
}

// linkJumps les pone el salto a las conexiones recién importadas: se busca
// por nombre, primero entre las importadas juntas y si no entre las que ya
// estaban. Si no aparece, la conexión queda sin salto y se avisa.
func (a *App) linkJumps(saved []importedConn) []error {
	byName := map[string]string{}
	for _, c := range a.store.List() {
		if c.Protocol != store.ProtoRDP && c.Protocol != store.ProtoVNC {
			byName[strings.ToLower(c.Name)] = c.ID
		}
	}
	for _, s := range saved { // las importadas ganan sobre las que ya estaban
		if s.conn.Protocol != store.ProtoRDP && s.conn.Protocol != store.ProtoVNC {
			byName[strings.ToLower(s.conn.Name)] = s.conn.ID
		}
	}
	var errs []error
	for _, s := range saved {
		if s.jump == "" {
			continue
		}
		id, ok := byName[strings.ToLower(s.jump)]
		if !ok || id == s.conn.ID {
			errs = append(errs, i18n.Err("app.sJumpHostConnectionS", s.conn.Name, s.jump))
			continue
		}
		c := s.conn
		c.JumpID = id
		if _, err := a.store.Save(store.ConnectionInput{Connection: c, KeepSecret: true}); err != nil {
			errs = append(errs, i18n.Err("app.sCouldnTLinkThe", c.Name, s.jump, err))
		}
	}
	return errs
}

// PickImportFile elige un archivo exportado de la herramienta indicada.
func (a *App) PickImportFile(source string) (string, error) {
	filters := map[string]runtime.FileFilter{
		importer.PuTTY:     {DisplayName: i18n.T("native.filterPutty"), Pattern: "*.reg"},
		importer.WinSCP:    {DisplayName: i18n.T("native.filterWinscp"), Pattern: "*.ini;*.reg"},
		importer.MRemoteNG: {DisplayName: i18n.T("native.filterMremoteng"), Pattern: "*.xml"},
		importer.OpenSSH:   {DisplayName: i18n.T("native.filterOpenssh"), Pattern: "config;*.conf"},
		importer.Osat:      {DisplayName: i18n.T("native.filterOsat"), Pattern: "*.osat"},
	}
	opts := runtime.OpenDialogOptions{Title: i18n.T("native.pickImport")}
	if f, ok := filters[source]; ok {
		opts.Filters = []runtime.FileFilter{f, {DisplayName: i18n.T("native.allFiles"), Pattern: "*.*"}}
	}
	return nativeOpenFileDialog(a.ctx, opts)
}

// ---------- Host keys ----------

func (a *App) TrustHostKey(host string) error  { return a.hk.Trust(host) }
func (a *App) ForgetHostKey(host string) error { return a.hk.Forget(host) }

// AnswerAuthPrompt responde una pregunta keyboard-interactive (nil = cancelar).
func (a *App) AnswerAuthPrompt(id string, answers []string) { a.mgr.AnswerPrompt(id, answers) }

// ---------- Sesiones ----------

func (a *App) OpenTerminal(req remote.ConnectRequest) (remote.SessionInfo, error) {
	return a.mgr.OpenTerminal(req)
}

func (a *App) OpenSFTP(req remote.ConnectRequest) (remote.SessionInfo, error) {
	return a.mgr.OpenSFTP(req)
}

func (a *App) OpenSFTPFrom(sessionID string) (remote.SessionInfo, error) {
	return a.mgr.OpenSFTPFrom(sessionID)
}

func (a *App) OpenTerminalFrom(sessionID string, cols, rows int) (remote.SessionInfo, error) {
	return a.mgr.OpenTerminalFrom(sessionID, cols, rows)
}

// LocalShells lista las terminales que se pueden abrir en este equipo.
func (a *App) LocalShells() []localterm.Shell { return a.mgr.LocalShells() }

// OpenLocalTerminal abre una terminal de este equipo (shell "" = PowerShell) en dir.
func (a *App) OpenLocalTerminal(shell, dir string, cols, rows int) (remote.SessionInfo, error) {
	return a.mgr.OpenLocalTerminal(shell, dir, cols, rows)
}

func (a *App) CloseSession(id string)                     { a.mgr.Close(id) }
func (a *App) TermAttach(id string) error                 { return a.mgr.Attach(id) }
func (a *App) TermWrite(id, data string) error            { return a.mgr.Write(id, data) }
func (a *App) TermResize(id string, cols, rows int) error { return a.mgr.Resize(id, cols, rows) }

// ---------- SFTP ----------

func (a *App) SftpList(id, dir string) (remote.Listing, error) { return a.mgr.List(id, dir) }
func (a *App) SftpMkdir(id, p string) error                    { return a.mgr.Mkdir(id, p) }
func (a *App) SftpRename(id, from, to string) error            { return a.mgr.Rename(id, from, to) }
func (a *App) SftpMove(id string, paths []string, dir string) error {
	return a.mgr.Move(id, paths, dir)
}

// SftpCopy copia (en el mismo servidor) y devuelve las rutas de las copias.
func (a *App) SftpCopy(id string, paths []string, dir string) ([]string, error) {
	return a.mgr.Copy(id, paths, dir)
}

// SftpCompress crea en el servidor un .tar, .tar.gz o .zip con los elementos (ver Manager.Compress) y devuelve su ruta.
func (a *App) SftpCompress(id string, paths []string, name string) (string, error) {
	return a.mgr.Compress(id, paths, name)
}

// SftpExtract extrae un archivo comprimido del servidor, sin pisar nada (ver Manager.Extract), y devuelve lo que creó.
func (a *App) SftpExtract(id, archive string) (string, error) { return a.mgr.Extract(id, archive) }

func (a *App) SftpRemove(id string, paths []string) error { return a.mgr.Remove(id, paths) }
func (a *App) SftpChmod(id, p string, mode uint32) error  { return a.mgr.Chmod(id, p, mode) }
func (a *App) SftpCreateFile(id, p string) error          { return a.mgr.CreateFile(id, p) }
func (a *App) SftpReadText(id, p string) (string, error)  { return a.mgr.ReadText(id, p) }
func (a *App) SftpWriteText(id, p, content string) error  { return a.mgr.WriteText(id, p, content) }

// SftpSudoReadText y SftpSudoWriteText leen y guardan como root con sudo (ver internal/remote/sudo.go).
// password vacío = la que se recuerda de esa conexión, si hay.
func (a *App) SftpSudoReadText(id, p, password string) (string, error) {
	return a.mgr.SudoReadText(id, p, password)
}
func (a *App) SftpSudoWriteText(id, p, content, password string) error {
	return a.mgr.SudoWriteText(id, p, content, password)
}

// SftpDragOut baja los archivos a una carpeta temporal y arranca un arrastre
// nativo del sistema con ellos (para soltarlos en el Explorador o el
// escritorio). Bloquea hasta que se suelta. Devuelve false si se canceló.
func (a *App) SftpDragOut(id string, paths []string) (bool, error) {
	if !osdrag.Supported {
		return false, osdrag.Files(nil) // el motivo, sin bajar nada a un temporal
	}
	local, err := a.mgr.DownloadToTemp(id, paths)
	if err != nil {
		return false, err
	}
	if err := osdrag.Files(local); err != nil {
		if errors.Is(err, osdrag.ErrCancelled) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// SftpUpload sube rutas locales a remoteDir. policy: "ask" | "overwrite" |
// "skip"; con "ask" devuelve un error CONFLICT si algo ya existe.
func (a *App) SftpUpload(id string, localPaths []string, remoteDir, policy string) ([]string, error) {
	return a.mgr.Upload(id, localPaths, remoteDir, policy)
}

// SftpDownload baja rutas remotas a localDir (misma política que SftpUpload).
func (a *App) SftpDownload(id string, remotePaths []string, localDir, policy string) ([]string, error) {
	return a.mgr.Download(id, remotePaths, localDir, policy)
}

// Los selectores nativos solo eligen rutas: la transferencia la arranca el
// frontend, que así puede preguntar qué hacer con los conflictos.

func (a *App) PickUploadFiles() ([]string, error) {
	return nativeOpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{
		Title: i18n.T("native.pickUploadFiles"),
	})
}

func (a *App) PickUploadFolder() (string, error) {
	return nativeOpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: i18n.T("native.pickUploadFolder"),
	})
}

// PickDownloadDir arranca en la carpeta de descargas de los ajustes (o en ~/Downloads si esa no existe:
// el diálogo de Windows falla si la carpeta inicial no existe).
func (a *App) PickDownloadDir() (string, error) {
	home, _ := os.UserHomeDir()
	dir := a.SettingsGet().Files.DownloadDir
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		dir = filepath.Join(home, "Downloads")
	}
	return nativeOpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            i18n.T("native.saveIn"),
		DefaultDirectory: dir,
	})
}

// PickLogDir elige la carpeta de los registros de sesión. Arranca en la de los ajustes si existe.
func (a *App) PickLogDir() (string, error) {
	o := runtime.OpenDialogOptions{Title: i18n.T("native.logsIn")}
	if fi, err := os.Stat(a.SettingsGet().Terminal.LogDir); err == nil && fi.IsDir() {
		o.DefaultDirectory = a.SettingsGet().Terminal.LogDir
	}
	return nativeOpenDirectoryDialog(a.ctx, o)
}

func (a *App) CancelTransfer(id string) { a.mgr.CancelTransfer(id) }
func (a *App) ClearTransfers()          { a.mgr.ClearTransfers() }

// SearchStart, SearchStop y SearchCaps buscan archivos en el servidor (ver internal/remote/search.go).
// SearchStart devuelve la carpeta real donde busca; los resultados llegan como eventos search:results:<id> y search:done:<id>.
func (a *App) SearchStart(searchID, sessionID, dir, namePattern, contains string) (string, error) {
	return a.mgr.SearchStart(searchID, sessionID, dir, namePattern, contains)
}
func (a *App) SearchStop(searchID string) { a.mgr.SearchStop(searchID) }
func (a *App) SearchCaps(sessionID string) (remote.SearchCaps, error) {
	return a.mgr.SearchCaps(sessionID)
}

// RetryTransfer vuelve a correr una transferencia que falló (ver internal/remote/resume.go).
func (a *App) RetryTransfer(id string) error { return a.mgr.RetryTransfer(id) }

// SyncCompare y SyncRun sincronizan una carpeta del servidor con una de esta PC (ver internal/remote/sync.go).
// SyncCompare devuelve la vista previa; SyncRun ejecuta lo marcado y devuelve el id de la transferencia.
func (a *App) SyncCompare(sessionID string, req remote.SyncRequest) (remote.SyncPreview, error) {
	return a.mgr.SyncCompare(sessionID, req)
}
func (a *App) SyncRun(sessionID, planID string, selected []string) (string, error) {
	return a.mgr.SyncRun(sessionID, planID, selected)
}

// PickFolder elige una carpeta de esta PC. Arranca en start si existe y si no en la carpeta personal (el
// diálogo de Windows falla si la carpeta inicial no existe).
func (a *App) PickFolder(title, start string) (string, error) {
	dir := start
	if fi, err := os.Stat(dir); dir == "" || err != nil || !fi.IsDir() {
		dir, _ = os.UserHomeDir()
	}
	return nativeOpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: title, DefaultDirectory: dir})
}

// PickKeyFile abre un selector para elegir la clave privada. Arranca en la
// carpeta de la clave actual, o en ~/.ssh, o en la carpeta personal: el
// diálogo de Windows falla si la carpeta inicial no existe.
func (a *App) PickKeyFile(current string) (string, error) {
	home, _ := os.UserHomeDir()
	candidates := []string{filepath.Join(home, ".ssh"), home}
	if current != "" {
		candidates = append([]string{filepath.Dir(remote.ExpandHome(current))}, candidates...)
	}
	dir := ""
	for _, d := range candidates {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			dir = d
			break
		}
	}
	return nativeOpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            i18n.T("native.pickKey"),
		DefaultDirectory: dir,
		Filters: []runtime.FileFilter{
			{DisplayName: i18n.T("native.filterKeys"), Pattern: "*.ppk;*.pem;*.key;id_*"},
			{DisplayName: i18n.T("native.allFiles"), Pattern: "*.*"},
		},
	})
}

// ---------- Navegador integrado ----------
// Cada sitio es un servidor local (127.0.0.1) que muestra una carpeta del
// servidor o uno de sus puertos; owner es la pestaña que lo pidió y los cierra.

// WebServeFolder sirve la carpeta root del servidor de la sesión.
func (a *App) WebServeFolder(owner, sessionID, root string) (*web.Site, error) {
	return a.mgr.ServeFolder(owner, sessionID, root)
}

// WebTunnel muestra un puerto del servidor de la sesión (scheme "" = http).
func (a *App) WebTunnel(owner, sessionID, scheme, host string, port int) (*web.Site, error) {
	return a.mgr.Tunnel(owner, sessionID, web.Target{Scheme: scheme, Host: host, Port: port})
}

// WebLocal muestra un servicio de esta PC (solo localhost).
func (a *App) WebLocal(owner, scheme, host string, port int) (*web.Site, error) {
	return a.mgr.LocalProxy(owner, web.Target{Scheme: scheme, Host: host, Port: port})
}

func (a *App) WebClose(owner string) { a.mgr.CloseWeb(owner) }

// WebOpenExternal abre una dirección http(s) en el navegador del sistema.
func (a *App) WebOpenExternal(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return i18n.Err("app.onlyHttpOrHttpsAddresses")
	}
	nativeBrowserOpenURL(a.ctx, rawURL)
	return nil
}

// ---------- Reenvío de puertos ----------
// Un puerto de esta PC (solo loopback) que llega, por la conexión SSH, a un
// puerto del servidor o a un pod/servicio de Kubernetes (kubectl del servidor).
// Las reglas se guardan por conexión; owner es la pestaña que las arranca y las detiene.

// OpenTunnel abre una sesión que solo sostiene la conexión SSH.
func (a *App) OpenTunnel(req remote.ConnectRequest) (remote.SessionInfo, error) {
	return a.mgr.OpenTunnel(req)
}

func (a *App) OpenTunnelFrom(sessionID string) (remote.SessionInfo, error) {
	return a.mgr.OpenTunnelFrom(sessionID)
}

func (a *App) ForwardList(connID string) []store.Forward { return a.store.Forwards(connID) }

func (a *App) ForwardSave(f store.Forward) (store.Forward, error) {
	saved, err := a.store.SaveForward(f)
	if err == nil && f.ID != "" {
		a.mgr.StopForward(f.ID) // si estaba en marcha, con los datos viejos
	}
	return saved, err
}

func (a *App) ForwardDelete(id string) error {
	a.mgr.StopForward(id)
	return a.store.DeleteForward(id)
}

// ForwardStart arranca un reenvío guardado sobre la conexión de la sesión.
func (a *App) ForwardStart(owner, sessionID, id string) error {
	f, ok := a.store.GetForward(id)
	if !ok {
		return i18n.Err("app.thatForwardNoLongerExists")
	}
	return a.mgr.StartForward(owner, sessionID, f)
}

func (a *App) ForwardStop(id string)                             { a.mgr.StopForward(id) }
func (a *App) ForwardStopAll(owner string)                       { a.mgr.StopForwardsOf(owner) }
func (a *App) ForwardStates(connID string) []remote.ForwardState { return a.mgr.ForwardStates(connID) }

// ForwardFreePort devuelve preferred si ese puerto de esta PC está libre y, si no, el siguiente libre.
func (a *App) ForwardFreePort(preferred int) int { return portfwd.FreePort(preferred) }

func (a *App) K8sNamespaces(sessionID string) (remote.K8sNamespaces, error) {
	return a.mgr.K8sNamespaces(sessionID)
}

func (a *App) K8sResources(sessionID, namespace string) ([]remote.K8sItem, error) {
	return a.mgr.K8sResources(sessionID, namespace)
}

// ---------- Control de versiones (git del servidor, pestaña Repositorio) ----------

func (a *App) GitStatus(sessionID, dir string) (remote.GitStatus, error) {
	return a.mgr.GitStatus(sessionID, dir)
}
func (a *App) GitStage(sessionID, root string, paths []string) error {
	return a.mgr.GitStage(sessionID, root, paths)
}
func (a *App) GitUnstage(sessionID, root string, paths []string) error {
	return a.mgr.GitUnstage(sessionID, root, paths)
}
func (a *App) GitDiscard(sessionID, root string, tracked, untracked []string) error {
	return a.mgr.GitDiscard(sessionID, root, tracked, untracked)
}
func (a *App) GitCommit(sessionID, root, message string) error {
	return a.mgr.GitCommit(sessionID, root, message)
}
func (a *App) GitSetIdentity(sessionID, root, name, email string) error {
	return a.mgr.GitSetIdentity(sessionID, root, name, email)
}
func (a *App) GitPull(sessionID, root string) error  { return a.mgr.GitPull(sessionID, root) }
func (a *App) GitPush(sessionID, root string) error  { return a.mgr.GitPush(sessionID, root) }
func (a *App) GitFetch(sessionID, root string) error { return a.mgr.GitFetch(sessionID, root) }
func (a *App) GitBranches(sessionID, root string) ([]string, error) {
	return a.mgr.GitBranches(sessionID, root)
}
func (a *App) GitCheckout(sessionID, root, name string, create bool) error {
	return a.mgr.GitCheckout(sessionID, root, name, create)
}

// GitFile es el contenido de un archivo en el último commit o en lo preparado (para el diff).
type GitFile struct {
	Text   string `json:"text"`
	Exists bool   `json:"exists"`
}

func (a *App) GitFileAt(sessionID, root, ref, path string) (GitFile, error) {
	text, exists, err := a.mgr.GitFileAt(sessionID, root, ref, path)
	return GitFile{Text: text, Exists: exists}, err
}

// ---------- Archivos de esta PC (panel "Este equipo") ----------

func (a *App) LocalHome() string                             { return localfs.Home() }
func (a *App) LocalList(dir string) (localfs.Listing, error) { return localfs.List(dir) }
func (a *App) LocalMkdir(p string) error                     { return localfs.Mkdir(p) }
func (a *App) LocalRename(from, to string) error             { return localfs.Rename(from, to) }
func (a *App) LocalMove(paths []string, dir string) error    { return localfs.Move(paths, dir) }
func (a *App) LocalRemove(paths []string) error              { return localfs.Remove(paths) }
func (a *App) LocalDeleteForever(paths []string) error       { return localfs.DeleteForever(paths) }
func (a *App) LocalOpen(p string) error                      { return localfs.Open(p) }
func (a *App) LocalReveal(p string) error                    { return localfs.Reveal(p) }
func (a *App) LocalReadText(p string) (string, error)        { return localfs.ReadText(p) }
func (a *App) LocalWriteText(p, content string) error        { return localfs.WriteText(p, content) }

// LocalCopy copia a dir y devuelve las rutas de las copias.
func (a *App) LocalCopy(paths []string, dir string) ([]string, error) {
	return localfs.Copy(paths, dir)
}

// LocalDragOut arrastra archivos locales al Explorador / escritorio (copia).
// Devuelve false si se canceló.
func (a *App) LocalDragOut(paths []string) (bool, error) {
	if err := osdrag.Files(paths); err != nil {
		if errors.Is(err, osdrag.ErrCancelled) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ---------- Escritorio remoto (RDP) ----------

// RdpOpenRequest: Secret es opcional (si se pidió al usuario en el momento;
// si hay que recordarlo, el frontend llama a RdpSaveSecret cuando el servidor
// lo acepta).
type RdpOpenRequest struct {
	ConnectionID string `json:"connectionId"`
	Secret       string `json:"secret"`
	// Contraseñas de los saltos (ProxyJump) pedidas al conectar, por id de conexión.
	JumpSecrets map[string]remote.JumpSecret `json:"jumpSecrets,omitempty"`
}

// RdpSession: lo que el cliente RDP del frontend (IronRDP) necesita para
// conectarse a través del proxy local. La contraseña viaja al WebView porque
// la autenticación (NLA/CredSSP) la hace el cliente.
type RdpSession struct {
	rdpproxy.Ticket
	User     string `json:"user"`
	Domain   string `json:"domain"`
	Password string `json:"password"`
}

// RdpOpen verifica el servidor (RDP sobre TLS, certificado de confianza) y
// prepara el proxy para una sesión.
func (a *App) RdpOpen(req RdpOpenRequest) (RdpSession, error) {
	c, ok := a.store.Get(req.ConnectionID)
	if !ok {
		return RdpSession{}, i18n.Err("app.connectionDoesNotExist")
	}
	if c.Protocol != store.ProtoRDP {
		return RdpSession{}, i18n.Err("app.theConnectionIsNotA")
	}
	c = a.store.WithDefaults(c)
	secret := req.Secret
	if secret == "" {
		secret = a.store.SecretFor(c)
	}
	if secret == "" {
		return RdpSession{}, remote.NewError(remote.CodeNeedSecret, "Ingresá la contraseña.", map[string]string{"kind": "password"})
	}
	addr := fmt.Sprintf("%s@%s:%d", c.User, c.Host, c.Port)
	slog.Debug("escritorio remoto: abriendo", "conn", c.Name, "addr", addr)
	var via rdpproxy.Via // nil = directo
	if c.Gateway != "" {
		tun, err := a.dialGateway(c, secret)
		if err != nil {
			remote.LogError("escritorio remoto: el gateway no autorizó", err, "conn", c.Name, "gateway", c.Gateway)
			return RdpSession{}, err
		}
		via = tun // la sesión RDP lo cierra al terminar
	} else if c.JumpID != "" {
		client, err := a.mgr.DialJump(c, req.JumpSecrets)
		if err != nil {
			return RdpSession{}, err
		}
		via = client // la sesión RDP lo cierra al terminar
	}
	t, err := a.rdp.OpenVia(c.Host, c.Port, via)
	if err != nil {
		err = rdpError(err, c)
		remote.LogError("escritorio remoto: no se pudo abrir", err, "conn", c.Name, "addr", addr)
		return RdpSession{}, err
	}
	slog.Info("escritorio remoto: abierto", "conn", c.Name, "addr", addr, "id", t.ID)
	return RdpSession{Ticket: t, User: c.User, Domain: c.Domain, Password: secret}, nil
}

// gatewayWait: cuánto se espera la autorización del RD Gateway, que puede
// estar esperando la aprobación del doble factor en el celular.
const gatewayWait = 90 * time.Second

// dialGateway abre el túnel del RD Gateway de c, con sus mismas credenciales.
func (a *App) dialGateway(c store.Connection, secret string) (*rdgateway.Tunnel, error) {
	host, port := store.GatewayAddr(c.Gateway)
	gw := net.JoinHostPort(host, strconv.Itoa(port))
	slog.Info("escritorio remoto: por gateway", "conn", c.Name, "gateway", gw)
	name, _ := os.Hostname()
	ctx, cancel := context.WithTimeout(context.Background(), gatewayWait)
	defer cancel()
	tun, err := rdgateway.Connect(ctx, rdgateway.Config{
		Host: host, Port: port, Domain: c.Domain, User: c.User, Password: secret, ClientName: name,
		VerifyConnection: func(cs tls.ConnectionState) error {
			return a.rdp.Certs.CheckGateway(gw, host, cs.PeerCertificates)
		},
	})
	if err != nil {
		return nil, gatewayError(err)
	}
	return tun, nil
}

// gatewayError prepara un error del gateway para el frontend: el del
// certificado tal cual (pregunta), usuario o contraseña rechazados como
// AUTH_FAILED (vuelve a pedir la contraseña) y el resto como GATEWAY, con el
// paso, el código y el detalle.
func gatewayError(err error) error {
	var re *remote.Error
	if errors.As(err, &re) {
		return re
	}
	var ge *rdgateway.Error
	if !errors.As(err, &ge) {
		return err
	}
	if ge.Kind == rdgateway.KindAuth {
		d := ge.Data()
		d["kind"] = "gateway"
		return remote.NewError(remote.CodeAuthFailed, ge.Error(), d)
	}
	return remote.NewError(remote.CodeGateway, ge.Error(), ge.Data())
}

// rdpError prepara para el frontend un error del sondeo del servidor RDP:
// los del gateway (el canal al equipo) con su código, y el paso con el
// servidor que falló después de abrir el TCP.
func rdpError(err error, c store.Connection) error {
	target := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	var ge *rdgateway.Error
	var se *rdpproxy.ServerError
	switch {
	case errors.As(err, &ge):
		return gatewayError(err)
	case c.Gateway != "" && errors.Is(err, rdpproxy.ErrViaTimeout):
		host, port := store.GatewayAddr(c.Gateway)
		e := &rdgateway.Error{Kind: rdgateway.KindTargetUnreachable, Step: rdgateway.StepChannel, Reason: "timeout",
			Gateway: net.JoinHostPort(host, strconv.Itoa(port)), Target: target, Err: err}
		slog.Warn("rd gateway: falló el canal al equipo", e.LogAttrs()...)
		return remote.NewError(remote.CodeGateway, e.Error(), e.Data())
	case errors.As(err, &se): // el del certificado ya llega sin envolver (pregunta)
		d := map[string]string{"kind": se.Step, "step": se.Step, "host": target, "detail": se.Err.Error()}
		if c.Gateway != "" {
			d["gateway"] = c.Gateway
		}
		return remote.NewError(remote.CodeRdpServer, se.Error(), d)
	}
	return err
}

// RdpTrustCert acepta para host:puerto el certificado con la huella que se le mostró al usuario.
func (a *App) RdpTrustCert(hostPort, fingerprint string) error {
	return a.rdp.Certs.Trust(hostPort, fingerprint)
}

// RdpSaveSecret guarda la contraseña recién cuando el servidor la aceptó.
func (a *App) RdpSaveSecret(connectionID, secret string) error {
	return a.store.SetSecret(connectionID, secret)
}

func (a *App) RdpClose(id string) { a.rdp.Close(id) }

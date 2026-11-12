package remote

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/pkg/sftp"

	"osat/internal/i18n"
	"osat/internal/syncplan"
)

// Sincronizar una carpeta de esta PC con una del servidor (como el "Sincronizar" de WinSCP). SyncCompare
// recorre las dos carpetas, arma el plan con internal/syncplan y lo guarda en memoria (uno por sesión) para
// la vista previa; SyncRun ejecuta lo que el usuario dejó marcado, como una sola transferencia del panel.
// Al copiar, el destino queda con la fecha de modificación del origen: así una segunda comparación no
// encuentra diferencias.

const (
	syncHashBatch  = 200      // rutas por comando sha256sum
	syncHashCmdMax = 64 << 10 // largo máximo (aproximado) de ese comando
)

var errNoSha256 = i18n.Err("remote.canTCompareByContent")

// SyncRequest es lo que se eligió en el diálogo. Exclude: patrones ya separados (los vacíos se ignoran).
type SyncRequest struct {
	RemoteDir string   `json:"remoteDir"`
	LocalDir  string   `json:"localDir"`
	Direction string   `json:"direction"` // up | down | both
	Mode      string   `json:"mode"`      // sync | mirror
	Compare   string   `json:"compare"`   // time+size | size | content
	Delete    bool     `json:"delete"`
	Exclude   []string `json:"exclude"`
}

// SyncPreview es la vista previa: las acciones del plan y las carpetas reales que se compararon.
type SyncPreview struct {
	PlanID    string            `json:"planId"`
	RemoteDir string            `json:"remoteDir"`
	LocalDir  string            `json:"localDir"`
	Actions   []syncplan.Action `json:"actions"`
}

type syncPlan struct {
	id, direction         string
	localRoot, remoteRoot string
	actions               []syncplan.Action
}

// syncOptions valida lo que manda el frontend y lo pasa a las opciones del plan.
func syncOptions(req SyncRequest) (syncplan.Options, error) {
	o := syncplan.Options{Direction: req.Direction, Mode: req.Mode, Compare: req.Compare, Delete: req.Delete}
	switch o.Direction {
	case syncplan.DirUp, syncplan.DirDown, syncplan.DirBoth:
	default:
		return o, i18n.Err("remote.invalidSyncDirectionQ", req.Direction)
	}
	switch o.Mode {
	case syncplan.ModeSync, syncplan.ModeMirror:
	default:
		return o, i18n.Err("remote.invalidSyncModeQ", req.Mode)
	}
	switch o.Compare {
	case syncplan.CompareTimeSize, syncplan.CompareSize, syncplan.CompareContent:
	default:
		return o, i18n.Err("remote.invalidComparisonCriterionQ", req.Compare)
	}
	if o.Direction == syncplan.DirBoth && o.Mode == syncplan.ModeMirror {
		return o, i18n.Err("remote.mirrorModeIsForOne")
	}
	if o.Direction == syncplan.DirBoth && o.Delete {
		return o, i18n.Err("remote.deletingAtTheDestinationIs")
	}
	for _, p := range req.Exclude {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, err := path.Match(p, ""); err != nil {
			return o, i18n.Err("remote.invalidExclusionPatternQ", p)
		}
		o.Exclude = append(o.Exclude, p)
	}
	return o, nil
}

// SyncCompare compara las dos carpetas y devuelve la vista previa. El plan queda guardado para SyncRun
// (una comparación nueva de la misma sesión reemplaza la anterior).
func (m *Manager) SyncCompare(sessionID string, req SyncRequest) (SyncPreview, error) {
	opts, err := syncOptions(req)
	if err != nil {
		return SyncPreview{}, err
	}
	s, err := m.get(sessionID)
	if err != nil {
		return SyncPreview{}, err
	}
	if s.sftp == nil {
		return SyncPreview{}, i18n.Err("remote.theSessionIsNotSftp")
	}
	localRoot, err := syncLocalRoot(req.LocalDir)
	if err != nil {
		return SyncPreview{}, err
	}
	remoteRoot, err := syncRemoteRoot(s.sftp, req.RemoteDir)
	if err != nil {
		return SyncPreview{}, err
	}
	ctx := context.Background()
	sh := s.shellOrNil()
	if opts.Compare == syncplan.CompareContent {
		ok, err := hasSha256sum(ctx, sh)
		if err != nil {
			return SyncPreview{}, err
		}
		if !ok {
			return SyncPreview{}, errNoSha256
		}
	}
	local, err := walkLocal(localRoot, opts.Exclude)
	if err != nil {
		return SyncPreview{}, err
	}
	remote, err := walkRemote(s.sftp, remoteRoot, opts.Exclude)
	if err != nil {
		return SyncPreview{}, err
	}
	if opts.Compare == syncplan.CompareContent {
		if err := hashPairs(ctx, sh, localRoot, remoteRoot, local, remote); err != nil {
			return SyncPreview{}, err
		}
	}
	acts := syncplan.Plan(local, remote, opts)
	if acts == nil {
		acts = []syncplan.Action{}
	}
	p := &syncPlan{id: uuid.NewString(), direction: opts.Direction, localRoot: localRoot, remoteRoot: remoteRoot, actions: acts}
	m.syncMu.Lock()
	m.syncPlans[sessionID] = p
	m.syncMu.Unlock()
	slog.Info("sincronización comparada", "local", localRoot, "remote", remoteRoot, "dir", opts.Direction, "mode", opts.Mode,
		"compare", opts.Compare, "actions", len(acts), "conn", s.name)
	return SyncPreview{PlanID: p.id, RemoteDir: remoteRoot, LocalDir: localRoot, Actions: acts}, nil
}

// SyncRun ejecuta las acciones marcadas (por ruta relativa) del plan planID, como una transferencia.
// Devuelve su id. El plan se usa una sola vez.
func (m *Manager) SyncRun(sessionID, planID string, selected []string) (string, error) {
	cl, err := m.sftpOf(sessionID)
	if err != nil {
		return "", err
	}
	m.syncMu.Lock()
	p := m.syncPlans[sessionID]
	m.syncMu.Unlock()
	if p == nil || p.id != planID {
		return "", i18n.Err("remote.theComparisonIsNoLonger")
	}
	want := make(map[string]bool, len(selected))
	for _, r := range selected {
		want[r] = true
	}
	var acts []syncplan.Action
	var total int64
	for _, a := range p.actions {
		if !want[a.Rel] {
			continue
		}
		delete(want, a.Rel)
		if a.Op == syncplan.OpSkip {
			continue
		}
		acts = append(acts, a)
		if a.Op == syncplan.OpUpload || a.Op == syncplan.OpDownload {
			total += a.Size
		}
	}
	if len(want) > 0 {
		return "", i18n.Err("remote.whatIsSelectedDoesnT")
	}
	if len(acts) == 0 {
		return "", i18n.Err("remote.nothingIsSelectedToSync")
	}
	m.syncMu.Lock()
	current := m.syncPlans[sessionID] == p
	if current {
		delete(m.syncPlans, sessionID)
	}
	m.syncMu.Unlock()
	if !current { // otra comparación lo reemplazó, o ya se ejecutó
		return "", i18n.Err("remote.theComparisonIsNoLonger")
	}

	info := Transfer{SessionID: sessionID, Direction: p.direction, Name: syncName(len(acts)), Total: total,
		Source: p.localRoot, Target: p.remoteRoot}
	if p.direction == syncplan.DirDown {
		info.Source, info.Target = p.remoteRoot, p.localRoot
	}
	slog.Info("sincronización pedida", "actions", len(acts), "bytes", total, "local", p.localRoot, "remote", p.remoteRoot, "conn", m.sessionConn(sessionID))
	return m.transfers.start(info, cl, ConflictOverwrite, func(ctx context.Context, cl *sftp.Client, tr *transfer, _ bool) error {
		return runSync(ctx, cl, p.localRoot, p.remoteRoot, acts, tr)
	}), nil
}

// dropSyncPlan olvida el plan de una sesión que se cierra.
func (m *Manager) dropSyncPlan(sessionID string) {
	m.syncMu.Lock()
	delete(m.syncPlans, sessionID)
	m.syncMu.Unlock()
}

func syncName(n int) string {
	if n == 1 {
		return i18n.T("remote.syncNameOne")
	}
	return i18n.T("remote.syncNameMany", n)
}

// ---- carpetas y recorridos ----

// syncLocalRoot es la carpeta de esta PC, con los enlaces resueltos (WalkDir no entra a una raíz que sea un enlace).
func syncLocalRoot(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", i18n.Err("remote.chooseTheFolderOnThis")
	}
	if !filepath.IsAbs(dir) {
		return "", i18n.Err("remote.theFolderOnThisPc", dir)
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		return "", i18n.Err("remote.qIsNotAFolder", dir)
	}
	return root, nil
}

// syncRemoteRoot es la carpeta del servidor resuelta con RealPath (si es un enlace, como /var/www, Walk no entra).
func syncRemoteRoot(cl *sftp.Client, dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", i18n.Err("remote.theServerFolderIsMissing")
	}
	root, err := cl.RealPath(dir)
	if err != nil {
		return "", err
	}
	if !path.IsAbs(root) {
		return "", i18n.Err("remote.couldnTResolveTheFolder", dir)
	}
	fi, err := cl.Stat(root)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		return "", i18n.Err("remote.qIsNotAFolder", root)
	}
	return root, nil
}

// walkLocal lista la carpeta de esta PC sin entrar a lo excluido. Una carpeta que no se puede leer es un
// error: con borrado, lo que falta del origen se borraría del destino.
func walkLocal(root string, exclude []string) ([]syncplan.Entry, error) {
	var out []syncplan.Entry
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if syncplan.Excluded(rel, exclude) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		e := syncplan.Entry{Rel: rel, IsDir: d.IsDir(), IsLink: d.Type()&fs.ModeSymlink != 0}
		if !e.IsDir && !e.IsLink {
			if !d.Type().IsRegular() {
				return nil // dispositivos, tuberías…: no se sincronizan
			}
			fi, err := d.Info()
			if err != nil {
				return err
			}
			e.Size, e.ModTime = fi.Size(), fi.ModTime().Unix()
		}
		out = append(out, e)
		return nil
	})
	if err != nil {
		return nil, i18n.Err("remote.couldnTReadTheFolder", err)
	}
	return out, nil
}

// walkRemote lista la carpeta del servidor (sin seguir enlaces) sin entrar a lo excluido. Como en
// walkLocal, una carpeta que no se puede leer es un error. Los nombres vienen del servidor: uno con "/" o
// ".." podría escaparse de la carpeta al borrar o bajar, así que se rechaza.
func walkRemote(cl *sftp.Client, root string, exclude []string) ([]syncplan.Entry, error) {
	var out []syncplan.Entry
	prefix := strings.TrimSuffix(root, "/") + "/"
	w := cl.Walk(root)
	for w.Step() {
		if err := w.Err(); err != nil {
			return nil, i18n.Err("remote.couldnTReadSOn", w.Path(), err)
		}
		if w.Path() == root {
			continue
		}
		fi := w.Stat()
		if n := fi.Name(); n == "" || n == "." || n == ".." || strings.ContainsAny(n, "/\x00") {
			return nil, i18n.Err("remote.invalidRemoteFileNameQ", n)
		}
		rel := strings.TrimPrefix(w.Path(), prefix)
		if syncplan.Excluded(rel, exclude) {
			w.SkipDir()
			continue
		}
		e := syncplan.Entry{Rel: rel, IsDir: fi.IsDir(), IsLink: fi.Mode()&os.ModeSymlink != 0}
		if !e.IsDir && !e.IsLink {
			if !fi.Mode().IsRegular() {
				continue
			}
			e.Size, e.ModTime = fi.Size(), fi.ModTime().Unix()
		}
		out = append(out, e)
	}
	return out, nil
}

// ---- hashes (comparar por contenido) ----

func hasSha256sum(ctx context.Context, sh *shell) (bool, error) {
	if !sh.usable() {
		return false, nil
	}
	status, _, err := sh.run(ctx, "command -v sha256sum")
	if errors.Is(err, errNoShell) {
		return false, nil
	}
	return err == nil && status == 0, err
}

// hashPairs calcula el sha256 de los archivos que están de los dos lados con el mismo tamaño (con
// tamaños distintos ya se sabe que difieren). El que no se pudo calcular queda vacío y el plan lo omite.
func hashPairs(ctx context.Context, sh *shell, localRoot, remoteRoot string, local, remote []syncplan.Entry) error {
	byRel := map[string]int{}
	for i, e := range remote {
		if !e.IsDir && !e.IsLink {
			byRel[e.Rel] = i
		}
	}
	var li, ri []int
	var paths []string
	for i, l := range local {
		if j, ok := byRel[l.Rel]; ok && !l.IsDir && !l.IsLink && l.Size == remote[j].Size {
			li, ri = append(li, i), append(ri, j)
			paths = append(paths, path.Join(remoteRoot, l.Rel))
		}
	}
	if len(paths) == 0 {
		return nil
	}
	hashes, err := remoteHashes(ctx, sh, paths)
	if err != nil {
		return err
	}
	for k := range paths {
		remote[ri[k]].Hash = hashes[paths[k]]
		if h, err := localHash(filepath.Join(localRoot, filepath.FromSlash(local[li[k]].Rel))); err == nil {
			local[li[k]].Hash = h
		}
	}
	return nil
}

// sha256Command arma un comando sha256sum para paths (todos entre comillas).
func sha256Command(paths []string) string {
	var b strings.Builder
	b.WriteString("LC_ALL=C sha256sum --")
	for _, p := range paths {
		b.WriteString(" " + shellQuote(p))
	}
	return b.String()
}

// remoteHashes calcula en el servidor el sha256 de paths, en tandas. Devuelve hash por ruta.
func remoteHashes(ctx context.Context, sh *shell, paths []string) (map[string]string, error) {
	out := make(map[string]string, len(paths))
	for start := 0; start < len(paths); {
		end, n := start, 0
		for end < len(paths) && end-start < syncHashBatch && (end == start || n < syncHashCmdMax) {
			n += len(paths[end]) + 3
			end++
		}
		stdout, status, _, err := sh.runOut(ctx, sha256Command(paths[start:end]))
		switch {
		case errors.Is(err, errNoShell):
			return nil, errNoSha256
		case err != nil:
			return nil, err
		case status == 126 || status == 127:
			return nil, errNoSha256
		}
		// Con 1 algún archivo no se pudo leer: los demás igual tienen su hash.
		for _, line := range strings.Split(stdout, "\n") {
			if h, p, ok := parseSha256Line(line); ok {
				out[p] = h
			}
		}
		start = end
	}
	return out, nil
}

var sha256Unescape = strings.NewReplacer(`\\`, `\`, `\n`, "\n", `\r`, "\r")

// parseSha256Line lee una línea de sha256sum: "hash  ruta" ("hash *ruta" en modo binario). Si la ruta
// tiene "\" o un salto de línea, GNU antepone "\" a la línea y los escapa.
func parseSha256Line(line string) (hash, p string, ok bool) {
	escaped := strings.HasPrefix(line, `\`)
	if escaped {
		line = line[1:]
	}
	if len(line) < 67 || line[64] != ' ' || (line[65] != ' ' && line[65] != '*') {
		return "", "", false
	}
	hash, p = line[:64], line[66:]
	for _, c := range hash {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return "", "", false
		}
	}
	if escaped {
		p = sha256Unescape.Replace(p)
	}
	return hash, p, true
}

func localHash(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ---- ejecutar ----

// runSync ejecuta las acciones en el orden del plan (carpetas antes que su contenido; borrados al final,
// archivos antes que su carpeta). Cada copia termina fijando la fecha de modificación del origen.
func runSync(ctx context.Context, cl *sftp.Client, localRoot, remoteRoot string, acts []syncplan.Action, tr *transfer) error {
	noTime, kept := 0, 0
	for _, a := range acts {
		if err := ctx.Err(); err != nil {
			return err
		}
		rp := path.Join(remoteRoot, a.Rel)
		lp := filepath.Join(localRoot, filepath.FromSlash(a.Rel)) // solo para lo que viene del listado de esta PC
		var err error
		timeOK, keptOne := true, false
		switch a.Op {
		case syncplan.OpMkdirRemote:
			err = cl.MkdirAll(rp)
		case syncplan.OpUpload:
			timeOK, err = syncUpload(ctx, cl, lp, rp, &tr.done)
		case syncplan.OpDeleteRemote:
			keptOne, err = removeRemote(cl, rp)
		case syncplan.OpDeleteLocal:
			keptOne, err = removeLocal(lp)
		case syncplan.OpMkdirLocal, syncplan.OpDownload:
			// La ruta viene del servidor: cada parte pasa por localName (no sale de la carpeta).
			if lp, err = localPathUnder(localRoot, remoteRoot, rp); err != nil {
				break
			}
			if a.Op == syncplan.OpMkdirLocal {
				err = os.MkdirAll(lp, 0o755)
			} else {
				timeOK, err = syncDownload(ctx, cl, rp, lp, &tr.done)
			}
		}
		if err != nil {
			return fmt.Errorf("%s: %w", a.Rel, err)
		}
		if !timeOK {
			noTime++
		}
		if keptOne {
			kept++
		}
	}
	var warn []string
	switch {
	case noTime == 1:
		warn = append(warn, i18n.T("remote.noTimeOne"))
	case noTime > 1:
		warn = append(warn, i18n.T("remote.noTimeMany", noTime))
	}
	switch {
	case kept == 1:
		warn = append(warn, i18n.T("remote.keptDirOne"))
	case kept > 1:
		warn = append(warn, i18n.T("remote.keptDirMany", kept))
	}
	if len(warn) > 0 {
		tr.warn(strings.Join(warn, " "))
	}
	return nil
}

// syncUpload sube local a remote y le pone la fecha de modificación del original. false = no se pudo fijar la fecha.
func syncUpload(ctx context.Context, cl *sftp.Client, local, remote string, ctr *atomic.Int64) (bool, error) {
	st, err := os.Stat(local)
	if err != nil {
		return false, err
	}
	if err := cl.MkdirAll(path.Dir(remote)); err != nil { // por si se desmarcó la carpeta y no el archivo
		return false, err
	}
	if err := uploadFile(ctx, cl, local, remote, ctr); err != nil {
		return false, err
	}
	return cl.Chtimes(remote, time.Now(), st.ModTime()) == nil, nil
}

// syncDownload baja remote a local y le pone la fecha de modificación del original.
func syncDownload(ctx context.Context, cl *sftp.Client, remote, local string, ctr *atomic.Int64) (bool, error) {
	st, err := cl.Stat(remote)
	if err != nil {
		return false, err
	}
	if err := downloadFile(ctx, cl, remote, local, ctr); err != nil {
		return false, err
	}
	return os.Chtimes(local, time.Now(), st.ModTime()) == nil, nil
}

// removeRemote borra un archivo o una carpeta vacía del servidor. Una carpeta que todavía tiene algo
// (se desmarcó algo de adentro) se deja: kept = true. Lo que ya no existe no es un error (un reintento).
func removeRemote(cl *sftp.Client, p string) (kept bool, err error) {
	fi, err := cl.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !fi.IsDir() {
		return false, cl.Remove(p)
	}
	if list, err := cl.ReadDir(p); err == nil && len(list) > 0 {
		return true, nil
	}
	return false, cl.RemoveDirectory(p)
}

// removeLocal es removeRemote en esta PC. Borra definitivamente (sin papelera), como WinSCP.
func removeLocal(p string) (kept bool, err error) {
	fi, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if fi.IsDir() {
		if list, err := os.ReadDir(p); err == nil && len(list) > 0 {
			return true, nil
		}
	}
	return false, os.Remove(p)
}

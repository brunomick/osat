package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"osat/internal/i18n"
	"osat/internal/updater"
)

// ---------- Actualizaciones ----------
// Al abrir osat (si el ajuste está activo) el frontend pregunta si hay una versión nueva en las Releases
// de GitHub; si el usuario acepta, se baja la de este sistema, se instala al cerrar y osat se vuelve a abrir.
// La primera vez que abre una versión nueva, osat muestra lo que cambió (del CHANGELOG que trae adentro).

// UpdateInfo es lo que se sabe de la última versión publicada.
type UpdateInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"` // Latest es más nueva que la que corre
	Notes     string `json:"notes"`     // qué trae (markdown)
	PageURL   string `json:"pageUrl"`   // página de la Release, para bajarla a mano
	CanApply  bool   `json:"canApply"`  // osat se puede actualizar solo en este sistema
	Manual    string `json:"manual"`    // si no se puede, por qué
}

// WhatsNew es el aviso de "Novedades" después de actualizar (vacío si no hay que mostrar nada).
type WhatsNew struct {
	Version string `json:"version"`
	Notes   string `json:"notes"`
}

type updateState struct {
	mu       sync.Mutex
	rel      *updater.Release // la última que encontró UpdateCheck
	applying bool
}

var upd updateState

func userAgent() string { return "osat/" + appVersion() }

// UpdateCheck consulta la última versión publicada. lang ("es" o "en") elige el idioma de las notas.
func (a *App) UpdateCheck(lang string) (UpdateInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cur := appVersion()
	rel, err := updater.Latest(ctx, updater.LatestURL(), userAgent())
	if err != nil {
		slog.Info("no se pudo buscar actualizaciones", "err", err)
		return UpdateInfo{}, err
	}
	upd.mu.Lock()
	upd.rel = &rel
	upd.mu.Unlock()
	info := UpdateInfo{Current: cur, Latest: rel.Version, Available: updater.Newer(rel.Version, cur), Notes: updater.NotesFor(rel.Body, lang), PageURL: rel.PageURL}
	if _, err := updater.Detect(); err != nil {
		info.Manual = err.Error()
	} else {
		info.CanApply = true
	}
	slog.Info("actualizaciones", "actual", cur, "publicada", rel.Version, "nueva", info.Available, "automatica", info.CanApply)
	return info, nil
}

// UpdateApply baja e instala la versión que encontró UpdateCheck. Avisa el avance con el evento
// update:progress ({done, total}); si sale bien, osat se cierra y se vuelve a abrir con la nueva.
func (a *App) UpdateApply() error {
	upd.mu.Lock()
	rel := upd.rel
	if rel == nil || upd.applying {
		upd.mu.Unlock()
		return i18n.Err("update.nothingToDo")
	}
	upd.applying = true
	upd.mu.Unlock()
	defer func() {
		upd.mu.Lock()
		upd.applying = false
		upd.mu.Unlock()
	}()

	slog.Info("actualizando", "de", appVersion(), "a", rel.Version)
	progress := func(done, total int64) {
		nativeEventsEmit(a.ctx, "update:progress", map[string]int64{"done": done, "total": total})
	}
	relaunch, err := updater.Apply(context.Background(), *rel, userAgent(), progress)
	if err != nil {
		slog.Warn("no se pudo actualizar", "err", err)
		return err
	}
	if err := relaunch(); err != nil {
		slog.Warn("no se pudo preparar el reinicio", "err", err)
		return err
	}
	slog.Info("actualización lista: osat se reinicia")
	go func() {
		time.Sleep(400 * time.Millisecond) // que la respuesta llegue al frontend antes de cerrar
		nativeQuit(a.ctx)
	}()
	return nil
}

// UpdateOpenPage abre la página de la última versión (para bajarla a mano donde osat no se actualiza solo).
func (a *App) UpdateOpenPage() {
	url := "https://github.com/brunomick/osat/releases/latest"
	upd.mu.Lock()
	if upd.rel != nil && upd.rel.PageURL != "" {
		url = upd.rel.PageURL
	}
	upd.mu.Unlock()
	nativeBrowserOpenURL(a.ctx, url)
}

// WhatsNew devuelve, una sola vez, lo que trae esta versión si es la primera vez que se abre después
// de actualizar, en ese idioma ("es" o "en"). En una instalación nueva no muestra nada.
func (a *App) WhatsNew(lang string) WhatsNew {
	return whatsNew(a.store.Dir(), appVersion(), changelogFor(lang))
}

// changelogFor elige el CHANGELOG del idioma. El de inglés solo tiene las versiones desde 0.4.0:
// whatsNew usa el de español si al de inglés le falta la versión.
func changelogFor(lang string) [2]string {
	if lang == i18n.EN {
		return [2]string{changelogENMD, changelogMD}
	}
	return [2]string{changelogMD, changelogMD}
}

func whatsNew(dir, cur string, changelogs [2]string) WhatsNew {
	file := filepath.Join(dir, "version")
	b, err := os.ReadFile(file)
	last := strings.TrimSpace(string(b))
	if err := os.WriteFile(file, []byte(cur+"\n"), 0o600); err != nil {
		slog.Warn("no se pudo guardar la versión", "err", err)
	}
	if err != nil {
		// Sin el archivo: o es una instalación nueva, o vino de una versión anterior a 0.5.0 (que no lo guardaba).
		if !exists(filepath.Join(dir, "settings.json")) && !exists(filepath.Join(dir, "connections.json")) {
			return WhatsNew{}
		}
		last = "0.0.0"
	}
	if !updater.Newer(cur, last) {
		return WhatsNew{}
	}
	notes := updater.ChangelogSection(changelogs[0], cur)
	if notes == "" {
		notes = updater.ChangelogSection(changelogs[1], cur)
	}
	if notes == "" {
		return WhatsNew{}
	}
	return WhatsNew{Version: cur, Notes: notes}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

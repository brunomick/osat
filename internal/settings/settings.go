// Package settings guarda los ajustes de osat en settings.json, dentro de la
// carpeta de datos. Un valor que falta, desconocido o fuera de rango vuelve a su
// valor por defecto; un archivo dañado se aparta como settings.json.bak.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const fileName = "settings.json"

// Valores de los ajustes que son una opción entre varias.
const (
	CursorBar       = "bar"
	CursorBlock     = "block"
	CursorUnderline = "underline"

	RightClickPaste = "paste" // copia si hay selección y si no pega (como PuTTY)
	RightClickMenu  = "menu"

	BellNone   = "none"
	BellVisual = "visual"
	BellSound  = "sound"

	ThemeAuto            = "auto" // sigue el tema de la interfaz
	ThemeOsatDark        = "osat-dark"
	ThemeOsatLight       = "osat-light"
	ThemeSolarizedDark   = "solarized-dark"
	ThemeSolarizedLight  = "solarized-light"
	ThemeDracula         = "dracula"
	ThemeOneDark         = "one-dark"
	ThemeGruvboxDark     = "gruvbox-dark"
	ThemeNord            = "nord"
	ThemeTokyoNight      = "tokyo-night"
	ThemeCatppuccinMocha = "catppuccin-mocha"
	ThemeMonokai         = "monokai"
	ThemeGitHubDark      = "github-dark"
	ThemeRosePine        = "rose-pine"
	ThemeKanagawa        = "kanagawa"
	ThemeEverforest      = "everforest"
	ThemeGitHubLight     = "github-light"
	ThemeCatppuccinLatte = "catppuccin-latte"
	ThemeRosePineDawn    = "rose-pine-dawn"

	AppearanceDark   = "dark"
	AppearanceLight  = "light"
	AppearanceSystem = "system" // sigue el modo del sistema operativo

	LanguageSystem = "system" // español si el sistema está en español, si no inglés
	LanguageES     = "es"
	LanguageEN     = "en"

	ConflictAsk       = "ask"
	ConflictOverwrite = "overwrite"
	ConflictSkip      = "skip"

	CursorInactiveOutline   = "outline"
	CursorInactiveBlock     = "block"
	CursorInactiveBar       = "bar"
	CursorInactiveUnderline = "underline"
	CursorInactiveNone      = "none"

	LinkClick     = "click"
	LinkCtrlClick = "ctrl"

	TermXterm256 = "xterm-256color"
	TermXterm    = "xterm"
	TermVT100    = "vt100"
	TermLinux    = "linux"

	// DefaultWordSeparators son los de xterm.js: lo que corta una palabra al seleccionarla con doble clic.
	DefaultWordSeparators = " ()[]{}',\"`"
)

// ColorThemes son los temas con nombre propio: valen para la terminal (Terminal.Theme) y para toda la
// app (General.Appearance), con los mismos colores.
var ColorThemes = []string{
	ThemeSolarizedDark, ThemeSolarizedLight, ThemeDracula, ThemeOneDark, ThemeGruvboxDark,
	ThemeNord, ThemeTokyoNight, ThemeCatppuccinMocha, ThemeMonokai, ThemeGitHubDark, ThemeRosePine,
	ThemeKanagawa, ThemeEverforest, ThemeGitHubLight, ThemeCatppuccinLatte, ThemeRosePineDawn,
}

// Límites de los valores numéricos.
const (
	MinFontSize    = 9
	MaxFontSize    = 28
	MinScrollback  = 1000
	MaxScrollback  = 100000
	MinLineHeight  = 1.0
	MaxLineHeight  = 2.0
	MaxLetterSpace = 5
	MaxPadding     = 40
	MaxScrollSpeed = 10
	MinKeepAlive   = 5 // segundos
	MaxKeepAlive   = 300

	MaxLockIdleMinutes = 1440 // un día

	maxFontLen       = 500
	maxSeparatorsLen = 100
)

// MinContrasts son los valores de contraste mínimo que se ofrecen (1 = sin corregir los colores).
var MinContrasts = []float64{1, 3, 4.5, 7}

// ErrCorrupt indica que settings.json no se pudo interpretar. Load ya devolvió
// los valores por defecto y apartó el archivo como settings.json.bak.
var ErrCorrupt = errors.New("settings.json dañado")

type Settings struct {
	Terminal Terminal `json:"terminal"`
	Files    Files    `json:"files"`
	General  General  `json:"general"`
}

type Terminal struct {
	Font        string `json:"font"` // pila CSS de fuentes
	FontSize    int    `json:"fontSize"`
	Scrollback  int    `json:"scrollback"` // líneas de historial
	Cursor      string `json:"cursor"`
	CursorBlink bool   `json:"cursorBlink"`
	RightClick  string `json:"rightClick"`
	Bell        string `json:"bell"`
	Theme       string `json:"theme"` // colores de la terminal
	// Lo que se selecciona con el mouse se copia solo al portapapeles (como PuTTY).
	CopyOnSelect bool `json:"copyOnSelect"`

	LineHeight     float64 `json:"lineHeight"`     // alto de cada línea, en veces el tamaño de letra
	LetterSpacing  int     `json:"letterSpacing"`  // píxeles extra entre letras
	FontWeight     int     `json:"fontWeight"`     // 100 a 900
	FontWeightBold int     `json:"fontWeightBold"` // 100 a 900
	Padding        int     `json:"padding"`        // margen entre el texto y el borde, en píxeles
	// Los colores del servidor que quedarían ilegibles sobre el fondo se ajustan hasta este contraste (uno de MinContrasts).
	MinContrast    float64 `json:"minContrast"`
	BoldBright     bool    `json:"boldBright"`     // la negrita usa la versión brillante del color (como PuTTY)
	CursorInactive string  `json:"cursorInactive"` // cómo se ve el cursor cuando la terminal no tiene el foco
	// Pegar texto de varias líneas pide confirmación (cada línea puede ejecutarse como un comando).
	PasteConfirm   bool   `json:"pasteConfirm"`
	ScrollSpeed    int    `json:"scrollSpeed"`    // líneas por paso de la rueda (con Alt, 5 veces más)
	ScrollOnInput  bool   `json:"scrollOnInput"`  // al escribir, vuelve al final
	ScrollOnOutput bool   `json:"scrollOnOutput"` // cuando llega salida nueva, vuelve al final
	WordSeparators string `json:"wordSeparators"` // lo que corta una palabra al seleccionarla con doble clic
	LinkClick      string `json:"linkClick"`      // los links se abren con un clic o con Ctrl+clic
	KeepAlive      int    `json:"keepAlive"`      // cada cuántos segundos se le pregunta al servidor si sigue ahí
	TermType       string `json:"termType"`       // TERM que se le pide al servidor
	// Lo que muestra cada terminal se guarda, sin colores, en un archivo de LogDir.
	SessionLog bool   `json:"sessionLog"`
	LogDir     string `json:"logDir"`
}

type Files struct {
	AskDownloadDir bool   `json:"askDownloadDir"` // preguntar dónde descargar
	DownloadDir    string `json:"downloadDir"`
	OnConflict     string `json:"onConflict"` // si el archivo ya existe en el destino
}

type General struct {
	DefaultShell string `json:"defaultShell"` // terminal local por defecto; vacío = todavía no elegida
	VerboseLog   bool   `json:"verboseLog"`
	// Al abrir osat vuelven las pestañas y el mosaico de la última vez.
	RestoreSession bool `json:"restoreSession"`
	// Tema de la interfaz: AppearanceDark, AppearanceLight, AppearanceSystem o uno de ColorThemes.
	Appearance string `json:"appearance"`
	// Idioma de la interfaz: LanguageSystem, LanguageES o LanguageEN.
	Language string `json:"language"`
	// Minutos sin uso para bloquear osat (0 = no bloquear solo). El bloqueo en sí no es un ajuste: ver internal/applock.
	LockIdleMinutes int `json:"lockIdleMinutes"`
	// Desbloquear también con Windows Hello (solo cuenta si la PC lo tiene).
	LockUseHello bool `json:"lockUseHello"`
	// Sección Sitios y pestañas de navegador nativo (donde el sistema lo soporta).
	WebBrowser bool `json:"webBrowser"`
	// Al abrir osat, buscar si hay una versión nueva (y ofrecer instalarla).
	AutoUpdate bool `json:"autoUpdate"`
	// Ofrecer también las versiones beta (pre-releases de GitHub), no solo las estables.
	BetaUpdates bool `json:"betaUpdates"`
}

// DefaultFont es la fuente de la terminal (la misma que --mono de app.css).
const DefaultFont = "'JetBrains Mono Variable', Consolas, monospace"

// Defaults devuelve los valores por defecto.
func Defaults() Settings {
	dir, logDir := "", ""
	if home, err := os.UserHomeDir(); err == nil {
		dir = filepath.Join(home, "Downloads")
		logDir = filepath.Join(home, "Documents", "osat-logs")
	}
	return Settings{
		Terminal: Terminal{
			Font:         DefaultFont,
			FontSize:     13,
			Scrollback:   10000,
			Cursor:       CursorBar,
			CursorBlink:  true,
			RightClick:   RightClickPaste,
			Bell:         BellNone,
			Theme:        ThemeAuto,
			CopyOnSelect: true,
			// Los que siguen son los valores que la terminal tenía fijos antes de que fueran ajustes.
			LineHeight:     1.2,
			FontWeight:     400,
			FontWeightBold: 600,
			Padding:        10,
			MinContrast:    4.5,
			CursorInactive: CursorInactiveOutline,
			ScrollSpeed:    1,
			ScrollOnInput:  true,
			WordSeparators: DefaultWordSeparators,
			LinkClick:      LinkClick,
			KeepAlive:      30,
			TermType:       TermXterm256,
			LogDir:         logDir,
		},
		Files: Files{
			AskDownloadDir: true,
			DownloadDir:    dir,
			OnConflict:     ConflictAsk,
		},
		General: General{RestoreSession: true, Appearance: AppearanceDark, Language: LanguageSystem, LockUseHello: true, WebBrowser: true, AutoUpdate: true},
	}
}

func oneOf(v string, valid ...string) bool {
	for _, x := range valid {
		if v == x {
			return true
		}
	}
	return false
}

func validWeight(w int) bool { return w >= 100 && w <= 900 && w%100 == 0 }

// normalize reemplaza por su valor por defecto lo que esté fuera de rango o sea desconocido.
func normalize(s Settings) Settings {
	d := Defaults()
	t := &s.Terminal
	t.Font = strings.TrimSpace(t.Font)
	if t.Font == "" || len(t.Font) > maxFontLen {
		t.Font = d.Terminal.Font
	}
	if t.FontSize < MinFontSize || t.FontSize > MaxFontSize {
		t.FontSize = d.Terminal.FontSize
	}
	if t.Scrollback < MinScrollback || t.Scrollback > MaxScrollback {
		t.Scrollback = d.Terminal.Scrollback
	}
	if !oneOf(t.Cursor, CursorBar, CursorBlock, CursorUnderline) {
		t.Cursor = d.Terminal.Cursor
	}
	if !oneOf(t.RightClick, RightClickPaste, RightClickMenu) {
		t.RightClick = d.Terminal.RightClick
	}
	if !oneOf(t.Bell, BellNone, BellVisual, BellSound) {
		t.Bell = d.Terminal.Bell
	}
	if !oneOf(t.Theme, append([]string{ThemeAuto, ThemeOsatDark, ThemeOsatLight}, ColorThemes...)...) {
		t.Theme = d.Terminal.Theme
	}
	if t.LineHeight < MinLineHeight || t.LineHeight > MaxLineHeight {
		t.LineHeight = d.Terminal.LineHeight
	}
	t.LineHeight = math.Round(t.LineHeight*100) / 100
	if t.LetterSpacing < 0 || t.LetterSpacing > MaxLetterSpace {
		t.LetterSpacing = d.Terminal.LetterSpacing
	}
	if !validWeight(t.FontWeight) {
		t.FontWeight = d.Terminal.FontWeight
	}
	if !validWeight(t.FontWeightBold) {
		t.FontWeightBold = d.Terminal.FontWeightBold
	}
	if t.Padding < 0 || t.Padding > MaxPadding {
		t.Padding = d.Terminal.Padding
	}
	if !slices.Contains(MinContrasts, t.MinContrast) {
		t.MinContrast = d.Terminal.MinContrast
	}
	if !oneOf(t.CursorInactive, CursorInactiveOutline, CursorInactiveBlock, CursorInactiveBar, CursorInactiveUnderline, CursorInactiveNone) {
		t.CursorInactive = d.Terminal.CursorInactive
	}
	if t.ScrollSpeed < 1 || t.ScrollSpeed > MaxScrollSpeed {
		t.ScrollSpeed = d.Terminal.ScrollSpeed
	}
	if t.WordSeparators == "" || len(t.WordSeparators) > maxSeparatorsLen {
		t.WordSeparators = d.Terminal.WordSeparators
	}
	if !oneOf(t.LinkClick, LinkClick, LinkCtrlClick) {
		t.LinkClick = d.Terminal.LinkClick
	}
	if t.KeepAlive < MinKeepAlive || t.KeepAlive > MaxKeepAlive {
		t.KeepAlive = d.Terminal.KeepAlive
	}
	if !oneOf(t.TermType, TermXterm256, TermXterm, TermVT100, TermLinux) {
		t.TermType = d.Terminal.TermType
	}
	t.LogDir = strings.TrimSpace(t.LogDir)
	if t.LogDir == "" || !filepath.IsAbs(t.LogDir) {
		t.LogDir = d.Terminal.LogDir
	} else {
		t.LogDir = filepath.Clean(t.LogDir)
	}

	f := &s.Files
	f.DownloadDir = strings.TrimSpace(f.DownloadDir)
	if f.DownloadDir == "" || !filepath.IsAbs(f.DownloadDir) {
		f.DownloadDir = d.Files.DownloadDir
	} else {
		f.DownloadDir = filepath.Clean(f.DownloadDir)
	}
	if !oneOf(f.OnConflict, ConflictAsk, ConflictOverwrite, ConflictSkip) {
		f.OnConflict = d.Files.OnConflict
	}

	s.General.DefaultShell = strings.TrimSpace(s.General.DefaultShell)
	if !oneOf(s.General.Appearance, append([]string{AppearanceDark, AppearanceLight, AppearanceSystem}, ColorThemes...)...) {
		s.General.Appearance = d.General.Appearance
	}
	if !oneOf(s.General.Language, LanguageSystem, LanguageES, LanguageEN) {
		s.General.Language = d.General.Language
	}
	if s.General.LockIdleMinutes < 0 || s.General.LockIdleMinutes > MaxLockIdleMinutes {
		s.General.LockIdleMinutes = d.General.LockIdleMinutes
	}
	return s
}

// Load lee dir/settings.json. Si no existe devuelve los valores por defecto. Si está
// dañado, devuelve los valores por defecto junto con un error que envuelve a ErrCorrupt,
// después de renombrarlo a settings.json.bak. No registra nada en el log: quien llama
// lo hace cuando el log ya está abierto.
func Load(dir string) (Settings, error) {
	path := filepath.Join(dir, fileName)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Defaults(), nil
	}
	if err != nil {
		return Defaults(), err
	}
	s := Defaults() // lo que falte en el archivo queda con su valor por defecto
	if err := json.Unmarshal(b, &s); err != nil {
		if rerr := os.Rename(path, path+".bak"); rerr != nil {
			return Defaults(), fmt.Errorf("%w (%v) y no se pudo apartar: %v", ErrCorrupt, err, rerr)
		}
		return Defaults(), fmt.Errorf("%w (%v): se apartó como %s.bak", ErrCorrupt, err, fileName)
	}
	return normalize(s), nil
}

// Save normaliza s, la guarda en dir/settings.json (escribiendo un temporal y
// renombrándolo, con permisos solo del usuario) y devuelve lo que quedó guardado.
func Save(dir string, s Settings) (Settings, error) {
	s = normalize(s)
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return Settings{}, err
	}
	path := filepath.Join(dir, fileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return Settings{}, err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return Settings{}, err
	}
	return s, nil
}

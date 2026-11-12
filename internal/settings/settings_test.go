package settings

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	d := Defaults()
	want := Settings{
		Terminal: Terminal{
			Font: "'JetBrains Mono Variable', Consolas, monospace", FontSize: 13, Scrollback: 10000,
			Cursor: "bar", CursorBlink: true, RightClick: "paste", Bell: "none", Theme: "auto", CopyOnSelect: true,
			LineHeight: 1.2, FontWeight: 400, FontWeightBold: 600, Padding: 10, MinContrast: 4.5, CursorInactive: "outline",
			ScrollSpeed: 1, ScrollOnInput: true, WordSeparators: " ()[]{}',\"`", LinkClick: "click", KeepAlive: 30,
			TermType: "xterm-256color", LogDir: filepath.Join(home, "Documents", "osat-logs"),
		},
		Files:   Files{AskDownloadDir: true, DownloadDir: filepath.Join(home, "Downloads"), OnConflict: "ask"},
		General: General{DefaultShell: "", VerboseLog: false, RestoreSession: true, Appearance: "dark", Language: "system", LockUseHello: true, WebBrowser: true, AutoUpdate: true},
	}
	if d != want {
		t.Fatalf("Defaults() = %+v\nquería    %+v", d, want)
	}
	if normalize(d) != d {
		t.Fatal("los valores por defecto no sobreviven a normalize")
	}
}

func TestLoadMissingFileGivesDefaults(t *testing.T) {
	dir := t.TempDir()
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s != Defaults() {
		t.Fatalf("Load = %+v", s)
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.json")); !os.IsNotExist(err) {
		t.Fatal("Load no debería crear el archivo")
	}
}

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := Defaults()
	in.Terminal.Font = "Fira Code, monospace"
	in.Terminal.FontSize = 20
	in.Terminal.Scrollback = 50000
	in.Terminal.Cursor = CursorBlock
	in.Terminal.CursorBlink = false
	in.Terminal.RightClick = RightClickMenu
	in.Terminal.Bell = BellSound
	in.Terminal.Theme = ThemeDracula
	in.Terminal.CopyOnSelect = false
	in.Files.AskDownloadDir = false
	in.Files.DownloadDir = filepath.Join(dir, "bajadas")
	in.Files.OnConflict = ConflictSkip
	in.General.DefaultShell = "wsl"
	in.General.VerboseLog = true
	in.General.RestoreSession = false
	in.General.Appearance = AppearanceSystem

	saved, err := Save(dir, in)
	if err != nil {
		t.Fatal(err)
	}
	if saved != in {
		t.Fatalf("Save devolvió %+v", saved)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != in {
		t.Fatalf("Load = %+v\nquería %+v", got, in)
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.json.tmp")); !os.IsNotExist(err) {
		t.Fatal("quedó el temporal")
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(filepath.Join(dir, "settings.json"))
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("permisos = %v", fi.Mode().Perm())
		}
	}
}

// Lo que no está en el archivo queda con su valor por defecto (también los booleanos que son true).
func TestLoadPartialFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"terminal":{"fontSize":18}}`), 0o600)
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := Defaults()
	want.Terminal.FontSize = 18
	if s != want {
		t.Fatalf("Load = %+v", s)
	}
	if !s.Terminal.CursorBlink || !s.Terminal.CopyOnSelect || !s.Files.AskDownloadDir || !s.General.RestoreSession {
		t.Fatal("los booleanos ausentes tienen que quedar en su valor por defecto")
	}
}

// Restaurar la sesión viene activado, y apagarlo se respeta al volver a cargar.
func TestRestoreSessionDefaultsOnAndCanBeTurnedOff(t *testing.T) {
	dir := t.TempDir()
	if !Defaults().General.RestoreSession {
		t.Fatal("restaurar la sesión tiene que venir activado")
	}
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"general":{"restoreSession":false}}`), 0o600)
	s, err := Load(dir)
	if err != nil || s.General.RestoreSession {
		t.Fatalf("Load = %+v, %v", s.General, err)
	}
}

// El navegador de sitios viene activado, también con un settings.json anterior
// que no tiene el campo, y apagarlo se respeta.
func TestDefaultsWebBrowserOn(t *testing.T) {
	if !Defaults().General.WebBrowser {
		t.Fatal("el navegador de sitios tiene que venir activado")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"general":{"restoreSession":false}}`), 0o600)
	s, err := Load(dir)
	if err != nil || !s.General.WebBrowser {
		t.Fatalf("un settings.json viejo cargó WebBrowser=%v, %v", s.General.WebBrowser, err)
	}
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"general":{"webBrowser":false}}`), 0o600)
	s, err = Load(dir)
	if err != nil || s.General.WebBrowser {
		t.Fatalf("apagado explícito: WebBrowser=%v, %v", s.General.WebBrowser, err)
	}
}

// La búsqueda de actualizaciones viene activada, también con un settings.json anterior a 0.5.0,
// y apagarla se respeta.
func TestDefaultsAutoUpdateOn(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"general":{"restoreSession":false}}`), 0o600)
	s, err := Load(dir)
	if err != nil || !s.General.AutoUpdate {
		t.Fatalf("un settings.json viejo cargó AutoUpdate=%v, %v", s.General.AutoUpdate, err)
	}
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"general":{"autoUpdate":false}}`), 0o600)
	s, err = Load(dir)
	if err != nil || s.General.AutoUpdate {
		t.Fatalf("apagado explícito: AutoUpdate=%v, %v", s.General.AutoUpdate, err)
	}
}

func TestNormalizeOutOfRange(t *testing.T) {
	d := Defaults()
	bad := Settings{
		Terminal: Terminal{Font: "  ", FontSize: 40, Scrollback: 5, Cursor: "raro", RightClick: "x", Bell: "y", Theme: "z"},
		Files:    Files{DownloadDir: "relativa/ruta", OnConflict: "z"},
		General:  General{Appearance: "violeta"},
	}
	got := normalize(bad)
	if got.Terminal.Font != d.Terminal.Font || got.Terminal.FontSize != 13 || got.Terminal.Scrollback != 10000 ||
		got.Terminal.Cursor != "bar" || got.Terminal.RightClick != "paste" || got.Terminal.Bell != "none" || got.Terminal.Theme != "auto" || got.General.Appearance != "dark" ||
		got.Files.DownloadDir != d.Files.DownloadDir || got.Files.OnConflict != "ask" {
		t.Fatalf("normalize = %+v", got)
	}
	// AskDownloadDir, CursorBlink y VerboseLog son sí/no: cualquier valor es válido.
	if got.Files.AskDownloadDir || got.Terminal.CursorBlink {
		t.Fatal("un booleano en false tiene que quedarse en false")
	}
}

func TestNormalizeBoundaries(t *testing.T) {
	for _, tc := range []struct{ in, want int }{{8, 13}, {9, 9}, {28, 28}, {29, 13}} {
		s := Defaults()
		s.Terminal.FontSize = tc.in
		if got := normalize(s).Terminal.FontSize; got != tc.want {
			t.Errorf("fontSize %d → %d, quería %d", tc.in, got, tc.want)
		}
	}
	for _, tc := range []struct{ in, want int }{{999, 10000}, {1000, 1000}, {100000, 100000}, {100001, 10000}} {
		s := Defaults()
		s.Terminal.Scrollback = tc.in
		if got := normalize(s).Terminal.Scrollback; got != tc.want {
			t.Errorf("scrollback %d → %d, quería %d", tc.in, got, tc.want)
		}
	}
}

func TestLoadNormalizesFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"terminal":{"fontSize":99,"cursor":"zzz","scrollback":20000}}`), 0o600)
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Terminal.FontSize != 13 || s.Terminal.Cursor != "bar" || s.Terminal.Scrollback != 20000 {
		t.Fatalf("Load = %+v", s.Terminal)
	}
}

// Un archivo dañado da los valores por defecto, se aparta como .bak y no se vuelve a quejar.
func TestLoadCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	os.WriteFile(path, []byte(`{"terminal": {oops`), 0o600)

	s, err := Load(dir)
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("err = %v", err)
	}
	if s != Defaults() {
		t.Fatalf("Load = %+v", s)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("settings.json tendría que haberse renombrado")
	}
	if b, err := os.ReadFile(path + ".bak"); err != nil || string(b) != `{"terminal": {oops` {
		t.Fatalf("settings.json.bak = %q, %v", b, err)
	}
	if _, err := Load(dir); err != nil {
		t.Fatalf("la segunda carga no debería fallar: %v", err)
	}
}

// Un valor del tipo equivocado también cuenta como archivo dañado.
func TestLoadWrongTypeIsCorrupt(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"terminal":{"fontSize":"grande"}}`), 0o600)
	if _, err := Load(dir); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.json.bak")); err != nil {
		t.Fatal(err)
	}
}

func TestSaveNormalizes(t *testing.T) {
	dir := t.TempDir()
	s := Defaults()
	s.Terminal.FontSize = 1000
	saved, err := Save(dir, s)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Terminal.FontSize != 13 {
		t.Fatalf("Save devolvió fontSize %d", saved.Terminal.FontSize)
	}
	if got, _ := Load(dir); got.Terminal.FontSize != 13 {
		t.Fatalf("quedó guardado fontSize %d", got.Terminal.FontSize)
	}
}

func TestSaveFailsInMissingDir(t *testing.T) {
	if _, err := Save(filepath.Join(t.TempDir(), "no-existe"), Defaults()); err == nil {
		t.Fatal("esperaba un error")
	}
}

func TestNormalizeLanguage(t *testing.T) {
	for _, ok := range []string{"system", "es", "en"} {
		s := Defaults()
		s.General.Language = ok
		if got := normalize(s).General.Language; got != ok {
			t.Errorf("%q se corrigió a %q", ok, got)
		}
	}
	s := Defaults()
	s.General.Language = "fr"
	if got := normalize(s).General.Language; got != "system" {
		t.Errorf("un idioma desconocido quedó como %q", got)
	}
}

func TestNormalizeTerminalExtras(t *testing.T) {
	d := Defaults()
	bad := d
	bad.Terminal.LineHeight = 3
	bad.Terminal.LetterSpacing = -1
	bad.Terminal.FontWeight = 450
	bad.Terminal.FontWeightBold = 1000
	bad.Terminal.Padding = 41
	bad.Terminal.MinContrast = 5
	bad.Terminal.CursorInactive = "blink"
	bad.Terminal.ScrollSpeed = 0
	bad.Terminal.WordSeparators = ""
	bad.Terminal.LinkClick = "double"
	bad.Terminal.KeepAlive = 1
	bad.Terminal.TermType = "ansi"
	bad.Terminal.LogDir = "relativa"
	if got := normalize(bad); got != d {
		t.Fatalf("normalize = %+v\nquería      %+v", got.Terminal, d.Terminal)
	}

	ok := d
	ok.Terminal.LineHeight = 1.5
	ok.Terminal.LetterSpacing = 5
	ok.Terminal.FontWeight = 300
	ok.Terminal.FontWeightBold = 900
	ok.Terminal.Padding = 0
	ok.Terminal.MinContrast = 1
	ok.Terminal.CursorInactive = CursorInactiveNone
	ok.Terminal.ScrollSpeed = 10
	ok.Terminal.WordSeparators = " /"
	ok.Terminal.LinkClick = LinkCtrlClick
	ok.Terminal.KeepAlive = 300
	ok.Terminal.TermType = TermVT100
	ok.Terminal.LogDir = filepath.Join(t.TempDir(), "logs")
	if got := normalize(ok); got != ok {
		t.Fatalf("normalize cambió valores válidos: %+v", got.Terminal)
	}
}

func TestNormalizeLockIdleMinutes(t *testing.T) {
	for _, ok := range []int{0, 1, 1440} {
		s := Defaults()
		s.General.LockIdleMinutes = ok
		if got := normalize(s).General.LockIdleMinutes; got != ok {
			t.Errorf("%d se corrigió a %d", ok, got)
		}
	}
	for _, bad := range []int{-1, 1441} {
		s := Defaults()
		s.General.LockIdleMinutes = bad
		if got := normalize(s).General.LockIdleMinutes; got != 0 {
			t.Errorf("%d quedó como %d", bad, got)
		}
	}
}

func TestNormalizeAppearanceColorThemes(t *testing.T) {
	for _, ok := range []string{"dark", "light", "system", "dracula", "solarized-dark", "solarized-light", "gruvbox-dark", "one-dark"} {
		s := Defaults()
		s.General.Appearance = ok
		if got := normalize(s).General.Appearance; got != ok {
			t.Errorf("%q se corrigió a %q", ok, got)
		}
	}
	s := Defaults()
	s.General.Appearance = "osat-dark" // es un tema de terminal, no de la app
	if got := normalize(s).General.Appearance; got != "dark" {
		t.Errorf("osat-dark quedó como %q", got)
	}
}

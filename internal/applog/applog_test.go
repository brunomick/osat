package applog

import (
	"bytes"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Al pasar el límite, osat.log pasa a osat.1.log y los viejos se corren un lugar.
func TestRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "osat.log")
	w, err := newRotWriter(path, 100, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	line := strings.Repeat("x", 59) + "\n" // 60 bytes: dos no entran en 100
	for _, tag := range []string{"A", "B", "C"} {
		if _, err := w.Write([]byte(tag + line[1:])); err != nil {
			t.Fatal(err)
		}
	}
	if got := readFile(t, path); !strings.HasPrefix(got, "C") {
		t.Fatalf("osat.log debería tener solo C: %q", got)
	}
	if got := readFile(t, oldName(path, 1)); !strings.HasPrefix(got, "B") {
		t.Fatalf("osat.1.log debería tener B: %q", got)
	}
	if got := readFile(t, oldName(path, 2)); !strings.HasPrefix(got, "A") {
		t.Fatalf("osat.2.log debería tener A: %q", got)
	}
}

// Nunca quedan más de 3 viejos.
func TestRotationKeepsThreeOld(t *testing.T) {
	path := filepath.Join(t.TempDir(), "osat.log")
	w, err := newRotWriter(path, 10, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	for i := 0; i < 10; i++ {
		if _, err := w.Write([]byte(fmt.Sprintf("linea %d\n", i))); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 3; i++ {
		if _, err := os.Stat(oldName(path, i)); err != nil {
			t.Fatalf("falta osat.%d.log: %v", i, err)
		}
	}
	if _, err := os.Stat(oldName(path, 4)); !os.IsNotExist(err) {
		t.Fatalf("no debería existir osat.4.log: %v", err)
	}
	// Lo más viejo se perdió: osat.log tiene la última línea y osat.3.log una de las anteriores.
	if got := readFile(t, path); got != "linea 9\n" {
		t.Fatalf("osat.log = %q", got)
	}
	if got := readFile(t, oldName(path, 3)); got != "linea 6\n" {
		t.Fatalf("osat.3.log = %q", got)
	}
}

// Si osat.log ya existe se sigue escribiendo a continuación y cuenta su tamaño.
func TestReopenAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "osat.log")
	os.WriteFile(path, []byte("previo\n"), 0o600)
	w, err := newRotWriter(path, 100, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.Write([]byte("nuevo\n"))
	if got := readFile(t, path); got != "previo\nnuevo\n" {
		t.Fatalf("osat.log = %q", got)
	}
	if w.size != int64(len("previo\nnuevo\n")) {
		t.Fatalf("size = %d", w.size)
	}
}

// Varias goroutines escribiendo a la vez: ninguna línea se corta ni se pierde entre archivos.
func TestConcurrentWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "osat.log")
	w, err := newRotWriter(path, 2000, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	logger := slog.New(slog.NewTextHandler(w, nil))

	const goroutines, perG = 8, 50
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				logger.Info("evento", "g", g, "i", i)
			}
		}(g)
	}
	wg.Wait()

	re := regexp.MustCompile(`^time=\S+ level=INFO msg=evento g=\d i=\d+$`)
	total := 0
	for _, p := range []string{path, oldName(path, 1), oldName(path, 2), oldName(path, 3)} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
			if !re.MatchString(l) {
				t.Fatalf("línea cortada o mezclada en %s: %q", p, l)
			}
			total++
		}
	}
	if total == 0 {
		t.Fatal("no se escribió nada")
	}
}

func TestSecretFilter(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(secretHandler{slog.NewTextHandler(&buf, nil)})

	logger.Info("conexión",
		"conn", "prod",
		"password", "hunter2",
		"Passphrase", "frase-secreta",
		"jump_pass", "otra",
		"bridgeToken", "tok123",
		"clientSecret", "sec456",
		slog.Group("auth", slog.String("password", "enGrupo"), slog.String("method", "password")),
	)
	logger.With("token", "enWith").WithGroup("g").Info("otro", "x", 1)

	out := buf.String()
	for _, s := range []string{"hunter2", "frase-secreta", "otra", "tok123", "sec456", "enGrupo", "enWith"} {
		if strings.Contains(out, s) {
			t.Errorf("el log contiene %q:\n%s", s, out)
		}
	}
	for _, s := range []string{"conn=prod", "auth.method=password", "password=[oculto]", "auth.password=[oculto]", "token=[oculto]", "g.x=1"} {
		if !strings.Contains(out, s) {
			t.Errorf("falta %q en:\n%s", s, out)
		}
	}
}

// Setup crea logs/osat.log, filtra secretos, respeta el nivel y manda el paquete log por el mismo handler.
func TestSetup(t *testing.T) {
	dir := t.TempDir()
	prev := slog.Default()
	defer slog.SetDefault(prev)
	defer func() {
		mu.Lock()
		if writer != nil {
			writer.Close()
		}
		writer, logPath = nil, ""
		mu.Unlock()
	}()

	logger, err := Setup(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	logger.Debug("detalle oculto")
	slog.Info("hola", "conn", "prod", "password", "hunter2")
	SetVerbose(true)
	slog.Debug("detalle visible")
	log.Printf("desde el paquete log")

	got := readFile(t, filepath.Join(dir, "logs", "osat.log"))
	if strings.Contains(got, "detalle oculto") {
		t.Errorf("el nivel normal escribió un debug:\n%s", got)
	}
	for _, s := range []string{"msg=hola", "conn=prod", "password=[oculto]", "detalle visible", "desde el paquete log"} {
		if !strings.Contains(got, s) {
			t.Errorf("falta %q en:\n%s", s, got)
		}
	}
	if strings.Contains(got, "hunter2") {
		t.Errorf("el log contiene la contraseña:\n%s", got)
	}
	// fecha y hora local con milisegundos
	if !regexp.MustCompile(`^time=\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}`).MatchString(got) {
		t.Errorf("formato de fecha inesperado: %q", strings.SplitN(got, "\n", 2)[0])
	}
}

func TestTail(t *testing.T) {
	dir := t.TempDir()
	defer func() {
		mu.Lock()
		if writer != nil {
			writer.Close()
		}
		writer, logPath = nil, ""
		mu.Unlock()
	}()
	prev := slog.Default()
	defer slog.SetDefault(prev)

	if _, err := Setup(dir, false); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 5; i++ {
		slog.Info("linea", "n", i)
	}
	got := Tail(3)
	if len(got) != 3 || !strings.Contains(got[0], "n=3") || !strings.Contains(got[2], "n=5") {
		t.Fatalf("Tail(3) = %q", got)
	}
	if got := Tail(100); len(got) != 5 {
		t.Fatalf("Tail(100) devolvió %d líneas", len(got))
	}

	// Si osat.log quedó corto después de rotar, completa con osat.1.log.
	logs := filepath.Join(dir, "logs")
	os.WriteFile(filepath.Join(logs, "osat.log"), []byte("c\nd\n"), 0o600)
	os.WriteFile(filepath.Join(logs, "osat.1.log"), []byte("a\nb\n"), 0o600)
	if got := Tail(3); strings.Join(got, ",") != "b,c,d" {
		t.Fatalf("Tail(3) con rotación = %q", got)
	}
}

func TestTailWithoutSetup(t *testing.T) {
	mu.Lock()
	logPath = ""
	mu.Unlock()
	if got := Tail(10); got != nil {
		t.Fatalf("Tail sin Setup = %q", got)
	}
}

// Recover registra el panic con su traza y lo vuelve a lanzar.
func TestRecover(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	defer slog.SetDefault(prev)
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))

	var relanzado any
	func() {
		defer func() { relanzado = recover() }()
		func() {
			defer Recover("prueba")
			panic("boom")
		}()
	}()

	if relanzado != "boom" {
		t.Fatalf("no se relanzó el panic: %v", relanzado)
	}
	out := buf.String()
	for _, s := range []string{"level=ERROR", "where=prueba", "panic=boom", "stack="} {
		if !strings.Contains(out, s) {
			t.Errorf("falta %q en:\n%s", s, out)
		}
	}
	if strings.Count(out, "\n") != 1 {
		t.Errorf("el evento debería ocupar una sola línea:\n%s", out)
	}
}

// Sin panic, Recover no hace nada.
func TestRecoverNoPanic(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	defer slog.SetDefault(prev)
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	func() { defer Recover("nada") }()
	if buf.Len() != 0 {
		t.Fatalf("se registró algo sin panic: %s", buf.String())
	}
}

package main

// Arranque en Android: la app de Android (carpeta android/) ejecuta este binario y le muestra la
// interfaz en un WebView. En lugar de Wails, un servidor HTTP en 127.0.0.1 sirve el frontend,
// /call llama a los métodos de App (como los bindings de Wails) y /events manda los eventos.
// osat-android.js arma window.go y window.runtime sobre eso, así el frontend no cambia.
//
// Solo atiende a quien trae el token que la app pasa en OSAT_TOKEN: el WebView lo recibe en la
// primera URL y queda en una cookie. Al cerrarse stdin (se cerró la app) osat termina.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"reflect"
	"runtime"
	"time"

	"github.com/gorilla/websocket"

	"osat/internal/applog"
	"osat/internal/i18n"
	"osat/internal/settings"
	"osat/internal/store"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed android/osat-android.js
var bridgeJS []byte

const tokenCookie = "osat_token"

func main() {
	defer applog.Recover("main")

	// Go en Android arranca siempre en UTC (no lee TZ): la app pasa la zona del teléfono.
	if loc, err := time.LoadLocation(os.Getenv("TZ")); err == nil {
		time.Local = loc
	}

	dir, err := store.ConfigDir()
	cfg := settings.Defaults()
	var cfgErr error
	if err == nil {
		cfg, cfgErr = settings.Load(dir)
		_, err = applog.Setup(dir, cfg.General.VerboseLog)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "osat: no se pudo abrir el archivo de log:", err)
	}
	i18n.SetLanguage(cfg.General.Language)
	slog.Info("arranque", "version", appVersion(), "os", runtime.GOOS, "arch", runtime.GOARCH, "dir", dir)
	if cfgErr != nil {
		slog.Warn("no se pudieron leer los ajustes, se usan los valores por defecto", "err", cfgErr)
	}

	token := os.Getenv("OSAT_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "osat: falta OSAT_TOKEN")
		os.Exit(2)
	}
	app, err := NewApp(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "osat:", err)
		os.Exit(1)
	}
	ctx := context.Background()
	app.startup(ctx)

	ln, err := listen()
	if err != nil {
		fmt.Fprintln(os.Stderr, "osat:", err)
		os.Exit(1)
	}
	// La primera línea de stdout es lo que espera la app de Android para cargar el WebView.
	fmt.Printf("OSAT_LISTEN %s\n", ln.Addr())

	go func() {
		io.Copy(io.Discard, bufio.NewReader(os.Stdin))
		app.shutdown(ctx)
		os.Exit(0)
	}()

	slog.Info("servidor de Android", "addr", ln.Addr().String())
	if err := http.Serve(ln, newMux(app, token)); err != nil {
		slog.Error("servidor de Android", "err", err)
	}
}

// fixedAddr es siempre el mismo porque el WebView guarda localStorage por origen (con el puerto):
// con uno al azar, osat olvidaría en cada arranque lo que el frontend recuerda ahí (grupos
// plegados, tema…). Si está ocupado (por ejemplo, el backend anterior todavía cerrando) se
// reintenta un rato y, si no, cualquiera.
const fixedAddr = "127.0.0.1:47831"

func listen() (net.Listener, error) {
	for i := 0; i < 30; i++ {
		if ln, err := net.Listen("tcp", fixedAddr); err == nil {
			return ln, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	slog.Warn("puerto fijo ocupado, se usa otro", "addr", fixedAddr)
	return net.Listen("tcp", "127.0.0.1:0")
}

func newMux(app *App, token string) http.Handler {
	dist, _ := fs.Sub(assets, "frontend/dist")
	files := http.FileServer(http.FS(dist))
	upgrader := websocket.Upgrader{CheckOrigin: sameOrigin}

	mux := http.NewServeMux()
	mux.HandleFunc("/osat-android.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		w.Write(bridgeJS)
	})
	mux.HandleFunc("/call", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			M string            `json:"m"`
			A []json.RawMessage `json:"a"`
		}
		if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&req) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		res, err := callMethod(app, req.M, req.A)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			json.NewEncoder(w).Encode(map[string]string{"e": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"r": res})
	})
	mux.HandleFunc("/picked", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID    int64    `json:"id"`
			Paths []string `json:"paths"`
		}
		if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&req) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		pickDone(req.ID, req.Paths)
	})
	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		c := hub.add()
		defer hub.remove(c)
		go func() { // solo para enterarse de que se cerró
			for {
				if _, _, err := conn.NextReader(); err != nil {
					conn.Close()
					return
				}
			}
		}()
		for msg := range c {
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			files.ServeHTTP(w, r)
			return
		}
		page, err := fs.ReadFile(dist, "index.html")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// El puente va antes que el frontend, que busca window.go al arrancar.
		page = bytes.Replace(page, []byte("<head>"), []byte(`<head><script src="/osat-android.js"></script>`), 1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(page)
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if t := r.URL.Query().Get("t"); t != "" && r.URL.Path == "/" {
			if subtle.ConstantTimeCompare([]byte(t), []byte(token)) != 1 {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: tokenCookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		c, err := r.Cookie(tokenCookie)
		if err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(token)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func sameOrigin(r *http.Request) bool {
	return r.Header.Get("Origin") == "http://"+r.Host
}

var errorType = reflect.TypeOf((*error)(nil)).Elem()

// callMethod llama a un método exportado de App como lo haría Wails: los argumentos llegan en JSON,
// vuelve el resultado que no es error, y un error rechaza la llamada con su texto.
func callMethod(app *App, name string, args []json.RawMessage) (interface{}, error) {
	m := reflect.ValueOf(app).MethodByName(name)
	if !m.IsValid() {
		return nil, fmt.Errorf("método desconocido: %s", name)
	}
	t := m.Type()
	if len(args) != t.NumIn() {
		return nil, fmt.Errorf("%s: se esperaban %d argumentos y llegaron %d", name, t.NumIn(), len(args))
	}
	in := make([]reflect.Value, len(args))
	for i, raw := range args {
		v := reflect.New(t.In(i))
		if err := json.Unmarshal(raw, v.Interface()); err != nil {
			return nil, fmt.Errorf("%s: argumento %d: %w", name, i+1, err)
		}
		in[i] = v.Elem()
	}
	out := m.Call(in)
	var result interface{}
	for _, v := range out {
		if v.Type() == errorType {
			if !v.IsNil() {
				return nil, v.Interface().(error)
			}
			continue
		}
		result = v.Interface()
	}
	return result, nil
}

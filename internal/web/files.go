package web

import (
	"html"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"

	"osat/internal/i18n"
)

// File es lo que hace falta de un archivo abierto (lo cumplen os.File y sftp.File).
type File interface {
	io.ReadSeeker
	io.Closer
	Stat() (fs.FileInfo, error)
}

// Source es una carpeta que se sirve (local o por SFTP). Los nombres son rutas
// con "/" relativas a su raíz y ya limpias ("/", "/css/app.css").
type Source interface {
	Stat(name string) (fs.FileInfo, error)
	Open(name string) (File, error)
	ReadDir(name string) ([]fs.FileInfo, error)
}

// En Windows el registro a veces asocia .js o .css a otros tipos y el
// navegador no los ejecuta: estos se fijan acá.
var mimeFixes = map[string]string{
	".html": "text/html; charset=utf-8",
	".htm":  "text/html; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".js":   "text/javascript; charset=utf-8",
	".mjs":  "text/javascript; charset=utf-8",
	".json": "application/json; charset=utf-8",
	".svg":  "image/svg+xml",
	".wasm": "application/wasm",
	".txt":  "text/plain; charset=utf-8",
	".md":   "text/plain; charset=utf-8",
}

func contentType(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if t, ok := mimeFixes[ext]; ok {
		return t
	}
	return mime.TypeByExtension(ext) // vacío: ServeContent lo deduce del contenido
}

// Files sirve src por HTTP: archivos con rangos y caché desactivada (así se ve
// lo último que se guardó), index.html en las carpetas y, si no hay, un listado.
func Files(src Source) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, i18n.T("web.methodNotAllowed"), http.StatusMethodNotAllowed)
			return
		}
		name := path.Clean("/" + r.URL.Path)
		fi, err := src.Stat(name)
		if err != nil {
			http.Error(w, i18n.T("web.notFound", name), http.StatusNotFound)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")

		if fi.IsDir() {
			if !strings.HasSuffix(r.URL.Path, "/") {
				http.Redirect(w, r, path.Base(name)+"/", http.StatusMovedPermanently)
				return
			}
			for _, idx := range []string{"index.html", "index.htm"} {
				if ifi, err := src.Stat(path.Join(name, idx)); err == nil && !ifi.IsDir() {
					serveFile(w, r, src, path.Join(name, idx), ifi)
					return
				}
			}
			listing(w, src, name)
			return
		}
		serveFile(w, r, src, name, fi)
	})
}

func serveFile(w http.ResponseWriter, r *http.Request, src Source, name string, fi fs.FileInfo) {
	f, err := src.Open(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()
	if t := contentType(name); t != "" {
		w.Header().Set("Content-Type", t)
	}
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}

func listing(w http.ResponseWriter, src Source, dir string) {
	infos, err := src.ReadDir(dir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sort.Slice(infos, func(i, j int) bool {
		if infos[i].IsDir() != infos[j].IsDir() {
			return infos[i].IsDir()
		}
		return strings.ToLower(infos[i].Name()) < strings.ToLower(infos[j].Name())
	})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var b strings.Builder
	b.WriteString(`<!doctype html><meta charset="utf-8"><title>` + html.EscapeString(dir) + `</title>` +
		`<style>body{font:14px ui-monospace,Consolas,monospace;margin:24px}a{color:#06c;text-decoration:none}li{list-style:none;line-height:1.7}</style>` +
		`<h3>` + html.EscapeString(dir) + `</h3><ul>`)
	if dir != "/" {
		b.WriteString(`<li><a href="../">../</a></li>`)
	}
	for _, fi := range infos {
		n := fi.Name()
		if fi.IsDir() {
			n += "/"
		}
		b.WriteString(`<li><a href="` + (&url.URL{Path: n}).EscapedPath() + `">` + html.EscapeString(n) + `</a></li>`)
	}
	b.WriteString(`</ul>`)
	_, _ = io.WriteString(w, b.String())
}

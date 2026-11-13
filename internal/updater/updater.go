// Package updater busca versiones nuevas de osat en las Releases de GitHub, baja la que
// corresponde a este sistema y la instala (apply_*.go). También saca del CHANGELOG lo que
// cambió en una versión, para el aviso de "Novedades".
package updater

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"osat/internal/i18n"
)

// LatestURL es la Release más nueva publicada. OSAT_UPDATE_URL la reemplaza (para probar contra un servidor local).
func LatestURL() string {
	if u := os.Getenv("OSAT_UPDATE_URL"); u != "" {
		return u
	}
	return "https://api.github.com/repos/brunomick/osat/releases/latest"
}

// ListURL son las Releases más recientes, betas incluidas (para quien acepta betas).
// OSAT_UPDATE_LIST_URL la reemplaza (para probar contra un servidor local).
func ListURL() string {
	if u := os.Getenv("OSAT_UPDATE_LIST_URL"); u != "" {
		return u
	}
	return "https://api.github.com/repos/brunomick/osat/releases?per_page=20"
}

// Release es una versión publicada.
type Release struct {
	Version string  // sin la "v": 0.5.0
	Body    string  // notas de la Release tal cual (en español y, después de enMarker, en inglés); ver NotesFor
	PageURL string  // página de la Release
	Beta    bool    // es una pre-release de GitHub
	Assets  []Asset // archivos para descargar
}

// Asset es un archivo de una Release.
type Asset struct {
	Name string
	URL  string
	Size int64
}

var client = &http.Client{Timeout: 30 * time.Second}

// ghRelease es una Release como la devuelve la API de GitHub.
type ghRelease struct {
	TagName string `json:"tag_name"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	Draft   bool   `json:"draft"`
	Pre     bool   `json:"prerelease"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

func (r ghRelease) release() Release {
	rel := Release{Version: strings.TrimPrefix(r.TagName, "v"), Body: r.Body, PageURL: r.HTMLURL, Beta: r.Pre}
	for _, a := range r.Assets {
		rel.Assets = append(rel.Assets, Asset{Name: a.Name, URL: a.URL, Size: a.Size})
	}
	return rel
}

// getJSON pide url a la API de GitHub y decodifica la respuesta en v.
func getJSON(ctx context.Context, url, userAgent string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return i18n.Err("update.httpStatus", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v)
}

// Latest consulta la Release más nueva (sin betas).
func Latest(ctx context.Context, url, userAgent string) (Release, error) {
	var r ghRelease
	if err := getJSON(ctx, url, userAgent, &r); err != nil {
		return Release{}, err
	}
	if r.TagName == "" || r.Draft || r.Pre {
		return Release{}, i18n.Err("update.noRelease")
	}
	return r.release(), nil
}

// Newest consulta la lista de Releases y devuelve la de versión más alta, estable o beta
// (los borradores no cuentan). Es la que se le ofrece a quien acepta betas.
func Newest(ctx context.Context, url, userAgent string) (Release, error) {
	var list []ghRelease
	if err := getJSON(ctx, url, userAgent, &list); err != nil {
		return Release{}, err
	}
	var best *ghRelease
	for i, r := range list {
		if r.TagName == "" || r.Draft {
			continue
		}
		if best == nil || Newer(r.TagName, best.TagName) {
			best = &list[i]
		}
	}
	if best == nil {
		return Release{}, i18n.Err("update.noRelease")
	}
	return best.release(), nil
}

// Newer dice si la versión a es más nueva que b ("0.10.0" > "0.9.3"). Lo que no es un número cuenta como 0.
// Una beta es anterior a su versión estable, y las betas se ordenan por su número:
// 1.1.0-beta.1 < 1.1.0-beta.2 < 1.1.0-beta.10 < 1.1.0 < 1.1.1-beta.1.
func Newer(a, b string) bool {
	return compare(a, b) > 0
}

func compare(a, b string) int {
	na, pa := parts(a)
	nb, pb := parts(b)
	for i := 0; i < 3; i++ {
		if na[i] != nb[i] {
			return cmpInt(na[i], nb[i])
		}
	}
	switch {
	case pa == pb:
		return 0
	case pa == "":
		return 1 // la estable es más nueva que cualquiera de sus betas
	case pb == "":
		return -1
	}
	// Como en semver: se comparan las partes separadas por puntos; las numéricas, como números.
	xa, xb := strings.Split(pa, "."), strings.Split(pb, ".")
	for i := 0; i < len(xa) && i < len(xb); i++ {
		ia, errA := strconv.Atoi(xa[i])
		ib, errB := strconv.Atoi(xb[i])
		switch {
		case errA == nil && errB == nil:
			if ia != ib {
				return cmpInt(ia, ib)
			}
		case errA == nil:
			return -1 // lo numérico va antes que el texto
		case errB == nil:
			return 1
		default:
			if c := strings.Compare(xa[i], xb[i]); c != 0 {
				return c
			}
		}
	}
	return cmpInt(len(xa), len(xb))
}

func cmpInt(a, b int) int {
	switch {
	case a > b:
		return 1
	case a < b:
		return -1
	}
	return 0
}

// parts separa "v1.2.3-beta.1+algo" en [1 2 3] y "beta.1" (lo que va después de "+" no cuenta).
func parts(v string) (nums [3]int, pre string) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v, pre = v[:i], v[i+1:]
	}
	for i, p := range strings.SplitN(v, ".", 3) {
		nums[i], _ = strconv.Atoi(p)
	}
	return nums, pre
}

// Kind es la forma de instalar que usa este osat.
type Kind string

const (
	KindWindowsPortable  Kind = "windows-portable"  // reemplaza osat.exe en su carpeta
	KindWindowsInstaller Kind = "windows-installer" // corre el instalador (instala para el usuario, sin permisos)
	KindLinux            Kind = "linux"
	KindMac              Kind = "macos"
)

// suffix es el final del nombre del archivo de cada forma de instalar (ver .github/workflows/release.yml).
var suffix = map[Kind]string{
	KindWindowsPortable:  "-windows-x64-portable.zip",
	KindWindowsInstaller: "-windows-x64-setup.exe",
	KindLinux:            "-linux-x64.tar.gz",
	KindMac:              "-macos-universal.zip",
}

// PickAsset elige el archivo de la Release para esa forma de instalar.
func PickAsset(rel Release, k Kind) (Asset, bool) {
	s, ok := suffix[k]
	if !ok {
		return Asset{}, false
	}
	for _, a := range rel.Assets {
		if strings.HasSuffix(a.Name, s) {
			return a, true
		}
	}
	return Asset{}, false
}

// Download baja url a dest y avisa el avance (total puede ser 0 si el servidor no lo dice).
func Download(ctx context.Context, url, dest, userAgent string, progress func(done, total int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := (&http.Client{}).Do(req) // sin el Timeout corto: el archivo puede tardar
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return i18n.Err("update.downloadStatus", resp.Status)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	total, done := max(resp.ContentLength, 0), int64(0) // -1: el servidor no dice el tamaño
	buf := make([]byte, 64<<10)
	last := time.Time{}
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return werr
			}
			done += int64(n)
			if progress != nil && time.Since(last) > 100*time.Millisecond {
				progress(done, total)
				last = time.Now()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			return rerr
		}
	}
	if progress != nil {
		progress(done, total)
	}
	if total > 0 && done != total {
		f.Close()
		return i18n.Err("update.incomplete", done, total)
	}
	return f.Close()
}

// ChangelogSection devuelve lo que dice el CHANGELOG para esa versión (sin su título), o "".
func ChangelogSection(changelog, version string) string {
	var out []string
	on := false
	for _, line := range strings.Split(strings.ReplaceAll(changelog, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "## [") {
			if on {
				break
			}
			on = strings.HasPrefix(line, "## ["+version+"]")
			continue
		}
		if on {
			out = append(out, line)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// enMarker separa, en las notas de la Release, la parte en español (antes) de la parte en inglés (después).
// Es un comentario de HTML: GitHub no lo muestra. Las versiones 0.5.x cortan las notas en "## Descargas",
// que está antes, así que ven solo el español.
const enMarker = "<!-- lang:en -->"

// NotesFor devuelve las notas de la Release en ese idioma ("es" o "en"), sin la sección de descargas.
// Si no hay parte en inglés, usa la de español.
func NotesFor(body, lang string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	es, en, ok := strings.Cut(body, enMarker)
	if lang == i18n.EN && ok && StripDownloads(en) != "" {
		return StripDownloads(en)
	}
	return StripDownloads(es)
}

// StripDownloads saca de las notas la sección "Descargas" / "Downloads" que agrega el workflow
// (dentro de osat no hace falta: la actualización se baja sola) y el separador "---" entre idiomas.
func StripDownloads(notes string) string {
	notes = "\n" + strings.ReplaceAll(notes, "\r\n", "\n")
	for _, h := range []string{"\n## Descargas", "\n## Downloads", "\n---"} {
		if i := strings.Index(notes, h); i >= 0 {
			notes = notes[:i]
		}
	}
	return strings.TrimSpace(notes)
}

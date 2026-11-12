// Package browser muestra páginas web reales (http/https) en una vista nativa que va
// encima de la ventana de osat: WebView2 en Windows y WebKitGTK en Linux. Tiene un
// perfil propio y fijo, así el login de los sitios sobrevive a los reinicios.
//
// No tiene nada que ver con internal/web (el navegador integrado de localhost por SSH).
// La vista nativa no expone window.go ni ningún objeto de osat: una página no puede
// llamar a la app.
package browser

import (
	"errors"
	"math"
)

// ErrUnsupported: este sistema no tiene motor de navegador nativo (macOS, Android).
var ErrUnsupported = errors.New("browser: no hay navegador nativo en este sistema")

// Bounds es el rectángulo de la pestaña en píxeles CSS, relativo al área cliente de la ventana.
type Bounds struct{ X, Y, W, H int }

// Physical lo pasa a píxeles físicos con el devicePixelRatio del frontend. Redondea los
// bordes (no el ancho y el alto) para que dos vistas pegadas no dejen una línea en medio.
// Una escala no válida (<= 0) cuenta como 1.
func (b Bounds) Physical(scale float64) Bounds {
	if scale <= 0 {
		scale = 1
	}
	px := func(v int) int { return int(math.Round(float64(v) * scale)) }
	x0, y0 := px(b.X), px(b.Y)
	return Bounds{X: x0, Y: y0, W: px(b.X+b.W) - x0, H: px(b.Y+b.H) - y0}
}

// Event es lo que el motor le cuenta al frontend (evento de Wails "browser:event").
// Todos traen el estado actual de la vista; Kind dice qué cambió:
//   - "nav": cambió la dirección o el historial (URL, CanBack, CanFwd).
//   - "title": cambió el título de la página (Title).
//   - "newWindow": la página pidió una ventana nueva; URL es su destino y no se abrió nada.
//   - "error": falló la carga de URL. No trae texto: la interfaz pone el suyo.
type Event struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	URL     string `json:"url"`
	Title   string `json:"title"`
	CanBack bool   `json:"canBack"`
	CanFwd  bool   `json:"canFwd"`
}

// Engine maneja una vista nativa por pestaña, identificada por id.
// scale es el devicePixelRatio del frontend.
type Engine interface {
	// Open crea la vista y empieza a cargar url. Si el id ya está abierto, solo
	// actualiza su posición (no vuelve a navegar). Espera a que la vista exista.
	Open(id, url string, b Bounds, scale float64) error
	Navigate(id, url string) error
	Back(id string) error
	Forward(id string) error
	Reload(id string) error
	// SetBounds y SetVisible no esperan al motor (el frontend los llama seguido).
	// Sobre un id desconocido no hacen nada.
	SetBounds(id string, b Bounds, scale float64) error
	SetVisible(id string, visible bool) error
	Close(id string)
	// CloseAll cierra todas las vistas. El motor sigue usable (se puede abrir de nuevo).
	CloseAll()
}

// Package rdpwin muestra un escritorio remoto con el cliente de Escritorio remoto de
// Windows (el control de mstsc, mstscax.dll) dentro de una pestaña de osat: una ventana
// hija de la ventana principal, igual que las vistas de internal/browser. Así se tiene
// todo lo de mstsc (portapapeles con archivos, Kerberos, RD Gateway, audio…).
//
// Solo existe en Windows. En Linux y macOS el escritorio remoto sigue con IronRDP
// (frontend/src/components/RdpView.svelte).
package rdpwin

import (
	"errors"

	"osat/internal/browser"
)

// ErrUnsupported: este sistema no tiene el cliente de Escritorio remoto de Windows.
var ErrUnsupported = errors.New("rdpwin: no hay cliente de Escritorio remoto de Windows en este sistema")

// Config es a qué y cómo conectarse.
type Config struct {
	Server   string // nombre o IP del equipo (o 127.0.0.1 si se llega por un salto SSH)
	Port     int
	User     string
	Domain   string
	Password string
	// Gateway: el RD Gateway ("host" o "host:puerto"); vacío = sin gateway. Usa las mismas credenciales.
	Gateway string
}

// Event es lo que el control le cuenta al frontend (evento de Wails "rdp:native").
//   - "connected": se abrió la conexión (todavía sin iniciar sesión).
//   - "loginComplete": el servidor aceptó el usuario y la contraseña.
//   - "disconnected": se cortó o no se pudo conectar. Reason es el código de Windows y
//     Text su descripción (en el idioma de Windows); Reason 1 o 2 es un cierre normal.
//   - "filesCopied": se soltaron Files archivos sobre el escritorio y quedaron en el
//     portapapeles, para pegarlos allá con Ctrl+V. "filesError": no se pudieron copiar (Text).
type Event struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Reason int    `json:"reason"`
	Text   string `json:"text"`
	Files  int    `json:"files,omitempty"`
}

// Engine maneja un escritorio remoto por pestaña, identificado por id. scale es el
// devicePixelRatio del frontend; los Bounds van en píxeles CSS (como en internal/browser).
type Engine interface {
	// Open crea el control en el lugar de la pestaña y empieza a conectar. Espera a que el
	// control exista (no a que conecte: eso llega por eventos).
	Open(id string, cfg Config, b browser.Bounds, scale float64) error
	// SetBounds y SetVisible no esperan al motor (el frontend los llama seguido). Un id
	// desconocido no hace nada. Con la sesión abierta, el escritorio sigue el tamaño.
	SetBounds(id string, b browser.Bounds, scale float64)
	SetVisible(id string, visible bool)
	// Focus le pasa el teclado al escritorio remoto.
	Focus(id string)
	// Notice muestra un aviso corto encima del escritorio (si se ve), que se cierra solo.
	Notice(id, text string)
	// Close desconecta y destruye el control.
	Close(id string)
	CloseAll()
}

package main

import (
	"log/slog"

	"osat/internal/i18n"
	"osat/internal/remote"
	"osat/internal/store"
	"osat/internal/vncproxy"
)

// ---------- Escritorio remoto (VNC) ----------

// VncOpenRequest: Secret es opcional (si se pidió al usuario en el momento;
// si hay que recordarlo, el frontend llama a VncSaveSecret).
type VncOpenRequest struct {
	ConnectionID string `json:"connectionId"`
	Secret       string `json:"secret"`
	// Contraseñas de los saltos (ProxyJump) pedidas al conectar, por id de conexión.
	JumpSecrets map[string]remote.JumpSecret `json:"jumpSecrets,omitempty"`
}

// VncSession: lo que noVNC necesita para conectarse a través del proxy local.
// Password es la guardada (o la de Secret): vacía si la conexión no tiene.
type VncSession struct {
	vncproxy.Ticket
	Password string `json:"password"`
}

// VncOpen conecta con el servidor (directo o a través del salto de la
// conexión) y prepara el proxy para una sesión.
func (a *App) VncOpen(req VncOpenRequest) (VncSession, error) {
	c, ok := a.store.Get(req.ConnectionID)
	if !ok {
		return VncSession{}, i18n.Err("app.connectionDoesNotExist")
	}
	if c.Protocol != store.ProtoVNC {
		return VncSession{}, i18n.Err("vnc.notVnc")
	}
	secret := req.Secret
	if secret == "" {
		secret = a.store.SecretFor(c)
	}
	var via vncproxy.Via // nil = directo
	if c.JumpID != "" {
		client, err := a.mgr.DialJump(c, req.JumpSecrets)
		if err != nil {
			return VncSession{}, err
		}
		via = client // la sesión VNC lo cierra al terminar
	}
	t, err := a.vnc.Open(c.Host, c.Port, via)
	if err != nil {
		remote.LogError("vnc: no se pudo abrir", err, "conn", c.Name, "host", c.Host, "port", c.Port)
		return VncSession{}, err
	}
	slog.Info("vnc: abierto", "conn", c.Name, "host", c.Host, "port", c.Port, "id", t.ID)
	return VncSession{Ticket: t, Password: secret}, nil
}

// VncSaveSecret guarda la contraseña (la que se pidió al conectar).
func (a *App) VncSaveSecret(connectionID, secret string) error {
	return a.store.SetSecret(connectionID, secret)
}

func (a *App) VncClose(id string) { a.vnc.Close(id) }

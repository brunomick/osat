package i18n

// Textos de vncproxy y app_vnc.go.
func init() {
	for k, v := range map[string]entry{
		"vnc.notVnc":      {es: "la conexión no es de escritorio remoto VNC", en: "the connection is not a VNC remote desktop one"},
		"vnc.notFound":    {es: "no se encontró el servidor (revisá el nombre)", en: "the server was not found (check the name)"},
		"vnc.timeout":     {es: "el servidor no responde (tiempo de espera agotado)", en: "the server isn't responding (timed out)"},
		"vnc.refused":     {es: "el servidor rechazó la conexión (¿está corriendo el servidor VNC en ese puerto?)", en: "the server refused the connection (is the VNC server running on that port?)"},
		"vnc.jumpFailed":  {es: "el bastión no pudo conectarse a %s: %v", en: "the jump host couldn't connect to %s: %v"},
		"vnc.jumpTimeout": {es: "el servidor no responde a través del bastión (tiempo de espera agotado)", en: "the server isn't responding through the jump host (timed out)"},
		"vnc.osatClosing": {es: "osat se está cerrando", en: "osat is closing"},
	} {
		catalog[k] = v
	}
}

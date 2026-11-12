package i18n

// Textos de internal/updater y app_update.go (actualizaciones).
func init() {
	for k, v := range map[string]entry{
		"update.noRelease":      {es: "no hay una versión publicada", en: "there is no published version"},
		"update.httpStatus":     {es: "GitHub respondió %s", en: "GitHub answered %s"},
		"update.downloadStatus": {es: "la descarga respondió %s", en: "the download answered %s"},
		"update.incomplete":     {es: "descarga incompleta (%d de %d bytes)", en: "incomplete download (%d of %d bytes)"},
		"update.noAsset":        {es: "la versión %s no trae el archivo para este sistema", en: "version %s has no file for this system"},
		"update.missingInFile":  {es: "el archivo descargado no trae %s", en: "the downloaded file doesn't contain %s"},
		"update.badPath":        {es: "ruta inválida en el archivo descargado: %s", en: "invalid path in the downloaded file: %s"},
		"update.notWritable": {
			es: "osat no puede reemplazarse a sí mismo en la carpeta donde está instalado: bajá la versión nueva de la página de descargas.",
			en: "osat can't replace itself in the folder where it's installed: download the new version from the downloads page.",
		},
		"update.notApp":      {es: "osat no está corriendo desde osat.app", en: "osat isn't running from osat.app"},
		"update.notHere":     {es: "en este sistema osat no se actualiza solo: bajá la versión nueva de la página de descargas.", en: "on this system osat doesn't update itself: download the new version from the downloads page."},
		"update.nothingToDo": {es: "no hay una actualización para instalar", en: "there is no update to install"},
	} {
		catalog[k] = v
	}
}

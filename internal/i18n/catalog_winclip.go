package i18n

// Textos de internal/winclip (pegar archivos del portapapeles del sistema).
func init() {
	for k, v := range map[string]entry{
		"winclip.badName":    {es: "el portapapeles trae un nombre de archivo no válido: %q", en: "the clipboard has an invalid file name: %q"},
		"winclip.noDir":      {es: "%s no es una carpeta", en: "%s is not a folder"},
		"winclip.busy":       {es: "el portapapeles está ocupado por otro programa: probá de nuevo", en: "another program is using the clipboard: try again"},
		"winclip.readFailed": {es: "no se pudo traer %s del portapapeles: %v", en: "couldn't get %s from the clipboard: %v"},
		"winclip.nothing":    {es: "no hay archivos copiados para pegar", en: "there are no copied files to paste"},
	} {
		catalog[k] = v
	}
}

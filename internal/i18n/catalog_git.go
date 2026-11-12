package i18n

// Textos de internal/remote/git.go (control de versiones de la pestaña Repositorio).
func init() {
	for k, v := range map[string]entry{
		"remote.gitNotInstalled": {es: "git no está instalado en el servidor (o no está en el PATH de sus comandos): probá \"git --version\" en una terminal de ese servidor", en: "git is not installed on the server (or it is not in the PATH of its commands): try \"git --version\" in a terminal on that server"},
		"remote.gitTimeout":      {es: "git tardó demasiado en contestar", en: "git took too long to answer"},
		"remote.gitFileTooBig":   {es: "el archivo es demasiado grande para mostrar sus cambios", en: "the file is too big to show its changes"},
		"remote.gitBinary":       {es: "es un archivo binario: no se pueden mostrar sus cambios", en: "it's a binary file: its changes can't be shown"},
		"remote.gitDubious":      {es: "git no confía en este repositorio porque es de otro usuario del servidor. Para permitirlo, corré en una terminal: git config --global --add safe.directory <carpeta>", en: "git doesn't trust this repository because it belongs to another user on the server. To allow it, run in a terminal: git config --global --add safe.directory <folder>"},
		"remote.gitNoDir":        {es: "la carpeta del repositorio ya no existe", en: "the repository folder no longer exists"},
		"remote.gitFailed":       {es: "git terminó con error (código %d)", en: "git ended with an error (code %d)"},
		"remote.gitNoFiles":      {es: "no hay archivos para esta acción", en: "there are no files for this action"},
		"remote.gitNoMessage":    {es: "escribí un mensaje para el commit", en: "write a commit message"},
		"remote.gitBadIdentity":  {es: "falta el nombre o el email", en: "the name or the email is missing"},
		"remote.gitBadBranch":    {es: "%q no es un nombre de rama válido", en: "%q is not a valid branch name"},
	} {
		catalog[k] = v
	}
}

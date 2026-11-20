//go:build windows

package localterm

import (
	"encoding/base64"
	"os"
	"strings"
	"unicode/utf16"
)

// ---------- Carpeta actual ----------
// Cada shell avisa en qué carpeta está cada vez que muestra el prompt, con la secuencia
// OSC 9;9 (ESC ] 9 ; 9 ; carpeta ESC \), la misma que usa Windows Terminal. La pestaña la
// guarda para abrir esa carpeta en "Este equipo".

// psPrompt envuelve el prompt de PowerShell (el del perfil del usuario, si tiene) para que
// antes avise la carpeta. Solo si es del sistema de archivos (no HKLM:, Env:...).
const psPrompt = `$global:__osatPrompt = $function:prompt
function global:prompt {
  $l = $executionContext.SessionState.Path.CurrentLocation
  if ($l.Provider.Name -eq 'FileSystem') { [Console]::Write("$([char]27)]9;9;$($l.ProviderPath)$([char]27)\") }
  & $global:__osatPrompt
}`

// psArgs abre PowerShell sin logo y con psPrompt, codificado para no pelear con las comillas.
func psArgs() []string {
	u := utf16.Encode([]rune(psPrompt))
	b := make([]byte, 0, len(u)*2)
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return []string{"-NoLogo", "-NoExit", "-EncodedCommand", base64.StdEncoding.EncodeToString(b)}
}

// cmdEnv antepone el aviso al prompt de cmd ($E es ESC y $P la carpeta), sin perder el del usuario.
func cmdEnv() []string {
	prompt := os.Getenv("PROMPT")
	if prompt == "" {
		prompt = "$P$G"
	}
	return []string{`PROMPT=$E]9;9;$P$E\` + prompt}
}

// bashCwd es el PROMPT_COMMAND de Git Bash: /c/Users/yo → c:\Users\yo (y cygpath para las
// carpetas propias de Git, como /usr).
const bashCwd = `case $PWD in /[a-zA-Z]|/[a-zA-Z]/*) __osat_d=${PWD:1:1}:${PWD:2};; *) __osat_d=$(cygpath -w "$PWD");; esac; printf '\e]9;9;%s\e\\' "${__osat_d//\//\\}"`

// wslCwd es el PROMPT_COMMAND de bash en WSL: /mnt/c/x → c:\x, y lo de Linux por la red
// interna de WSL (/home/yo → \\wsl.localhost\Ubuntu\home\yo), que Windows abre como una carpeta más.
const wslCwd = `case $PWD in /mnt/[a-zA-Z]|/mnt/[a-zA-Z]/*) __osat_d=${PWD:5:1}:${PWD:6};; *) __osat_d=//wsl.localhost/$WSL_DISTRO_NAME$PWD;; esac; printf '\e]9;9;%s\e\\' "${__osat_d//\//\\}"`

// wslEnv le pasa wslCwd a WSL: Windows solo comparte con Linux las variables que nombra WSLENV.
func wslEnv() []string {
	share := "PROMPT_COMMAND"
	if w := os.Getenv("WSLENV"); w != "" {
		share = w + ":" + share
	}
	return []string{"PROMPT_COMMAND=" + wslCwd, "WSLENV=" + share}
}

// setEnv pone kv ("CLAVE=valor") en env, reemplazando la que haya con esa clave (sin
// distinguir mayúsculas, como Windows): repetida, la shell podría quedarse con la vieja.
func setEnv(env []string, kv string) []string {
	key, _, _ := strings.Cut(kv, "=")
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if k, _, _ := strings.Cut(e, "="); !strings.EqualFold(k, key) {
			out = append(out, e)
		}
	}
	return append(out, kv)
}

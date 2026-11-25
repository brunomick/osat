# Shells persistentes — plan de implementación

> **Para quien lo ejecute:** se ejecuta en esta sesión (nativo), tarea por tarea, con revisión de toda la rama al final. Pasos con `- [ ]`.

**Objetivo:** shells SSH que sobreviven al cierre de la pestaña, con tmux por debajo y la interfaz de osat, listadas en la barra lateral bajo su servidor.

**Arquitectura:** el backend habla con tmux por canales de ejecución SSH aparte (listar, crear, renombrar, terminar). Abrir una shell de un panel usa el **modo control** (`tmux -C attach` sin pty): `%output` → la terminal de osat, teclas → `send-keys -H`, tamaño → `refresh-client -C`. Las sesiones con varias ventanas/paneles se abren con `tmux attach` común en un pty. El frontend agrega `Tab.tmux` y una lista de sesiones por conexión en la barra lateral.

**Stack:** Go (golang.org/x/crypto/ssh), Wails, Svelte 4 + TypeScript, vitest.

**Spec:** `docs/superpowers/specs/2026-11-25-shells-persistentes-design.md`

## Restricciones globales

- tmux mínimo **3.0** (`send-keys -H`); *Solo mirar* requiere **3.1** (`attach -f read-only,ignore-size`). Con menos, se avisa y no se abre/crea.
- Nunca opciones globales de tmux; a las sesiones de osat solo se les pone `@osat_owner` (`usuario@equipo`).
- Las sesiones se apuntan por `session_id` (`$N`), nunca por nombre.
- Quien mira (*Solo mirar*) no manda `refresh-client`.
- Textos visibles por i18n (Go: `internal/i18n/catalog_remote.go`; frontend: `frontend/src/lib/i18n/{es,en}.json`). Comentarios en español.
- Commits con `brunomick <brunomick23@gmail.com>`, sin coautor.

## Foco de revisión

1. Salida con bytes partidos (UTF-8 o escapes cortados entre dos `%output`) → la terminal no muestra basura: el decodificador trabaja por bytes, no por runas.
2. Pegar un texto grande → se parte en varios `send-keys` sin pasar el largo máximo de línea de comando de tmux.
3. La sesión se termina desde otro osat mientras está abierta → la pestaña muestra que terminó, no queda colgada.
4. Servidor sin tmux o con tmux viejo → mensaje claro, no un error crudo de shell.
5. Corte de red → la reconexión automática vuelve a la misma sesión (`Tab.tmux.id`), no crea otra.

---

### Tarea 1: utilidades de tmux (puras)

**Archivos:** crear `internal/remote/tmux.go`, `internal/remote/tmux_test.go`.

**Produce:**
- `type TmuxSession struct { ID, Name string; Attached int; Activity int64; Windows, Panes int; Owner string }` (json: `id,name,attached,activity,windows,panes,owner`).
- `tmuxListFormat` (const) y `parseTmuxSessions(out string) []TmuxSession`.
- `parseTmuxVersion(out string) (major, minor int, ok bool)` — acepta `tmux 3.4`, `tmux 3.0a`, `tmux next-3.5`, `tmux master` (ok=false → se trata como nueva).
- `shellQuote(s string) string` (comillas simples POSIX), si no existe ya en el paquete.
- `sendKeysCommands(pane string, data []byte) []string` — `send-keys -t <pane> -H xx xx…`, partido cada 512 bytes.
- `decodeOctal(b []byte) []byte` — deshace `\ooo` de `%output`.
- `type controlParser` con `feed(line []byte)` que separa: respuestas `%begin…%end/%error` (por número de comando) y notificaciones (`%output`, `%exit`, `%session-renamed`).

**Pasos:** tests primero (`TestParseTmuxSessions`, `TestParseTmuxVersion`, `TestSendKeysCommandsChunks`, `TestDecodeOctalKeepsPartialUTF8`, `TestControlParserBlocksAndNotifications`), verlos fallar, implementar, `go test ./internal/remote -run Tmux`, commit.

### Tarea 2: operaciones (listar, crear, renombrar, terminar)

**Archivos:** `internal/remote/tmux.go`, `app.go`, `internal/i18n/catalog_remote.go`.

**Produce (Manager):**
- `TmuxSessions(sessionID string) ([]TmuxSession, error)` — usa la conexión de esa sesión (`withClient`).
- `TmuxSessionsDial(req ConnectRequest) ([]TmuxSession, error)` — conecta, lista y cierra.
- `TmuxCreate(sessionID, name string) (TmuxSession, error)` — `new-session -d -P -F <formato> -s name`, pone `@osat_owner`, manda el comando inicial de la conexión con `send-keys -l`.
- `TmuxRename(sessionID, tmuxID, name string) (string, error)` — devuelve el nombre que quedó.
- `TmuxKill(sessionID, tmuxID string) error`.
- Errores: `remote.tmuxMissing`, `remote.tmuxOld` (con versión). "no server running" = lista vacía.
- Bindings en `app.go` con el mismo nombre.

**Pasos:** test de los comandos armados (`TestTmuxCommands`), implementar, prueba a mano contra WSL, commit.

### Tarea 3: terminal en modo control

**Archivos:** crear `internal/remote/tmuxterm.go`, `internal/remote/tmuxterm_test.go`; modificar `manager.go` (Session, Write, Resize, Close, OpenTerminal, OpenTerminalFrom), `app.go`.

**Consume:** Tarea 1.
**Produce:**
- `type TmuxTarget struct { ID string; ReadOnly bool; Plain bool }` (json `id,readOnly,plain`) y campo `Tmux *TmuxTarget` en `ConnectRequest`.
- `OpenTerminalFrom(sessionID string, cols, rows int, tmux *TmuxTarget)`.
- `SessionInfo.Title` = nombre de la sesión tmux.
- Eventos: `term:data:<id>` (igual que hoy), `term:exit:<id>`, `term:tmuxname:<id>` (nuevo nombre).
- Al conectarse: `refresh-client -C` (si no es solo mirar), `display -p '#{pane_id} #{cursor_x} #{cursor_y}'`, `capture-pane -p -e -S -2000`, se escribe el historial + `CUP` y recién ahí se pasa `%output`. Sin arranque limpio.
- `Plain`: `tmux attach -t <id>` en un pty (como `startShell`, sin arranque limpio ni comando inicial).

**Pasos:** test con un flujo falso de líneas de control (`TestTmuxTermReplaysHistoryThenOutput`, `TestTmuxTermWriteSendsKeys`, `TestTmuxTermReadOnlyNoResize`, `TestTmuxTermExit`), implementar, prueba con WSL (`OSAT_TMUX_WSL=1`), commit.

### Tarea 4: frontend — pestaña

**Archivos:** `frontend/src/lib/api.ts`, `stores.ts`, `connect.ts`, el componente de terminal, `workspace.ts` (+ tests).

**Produce:** `Tab.tmux?: { id: string; name: string; readOnly?: boolean; plain?: boolean }`; `connectTab` pasa `tmux` en el pedido y en `openTerminalFrom`; renombrar la pestaña → `api.tmuxRename` y `label`; evento `term:tmuxname` actualiza el título; espacios de trabajo guardan y restauran `tmux`. Se extrae de `connectTab` el bucle de reintentos a `connectWith(conn, open, opts)` para reusarlo en *Actualizar*.

**Pasos:** tests de `workspace` con `tmux`, implementar, `npm test`, `npm run check`, commit.

### Tarea 5: frontend — barra lateral

**Archivos:** `frontend/src/components/Sidebar.svelte`, nuevo `frontend/src/lib/tmux.ts` (+ test), i18n.

**Produce:** store `tmuxByConn` (lista + `stale`), refresco cada 30 s mientras haya una pestaña `ready` de esa conexión, *Actualizar* (conecta/lista/cierra), filas bajo el servidor con dueño (*mía* / otro / *tmux*), gente adentro (= `attached` − pestañas propias en esa sesión), actividad; menú: Abrir, Solo mirar (si tmux ≥ 3.1), Renombrar, Terminar (confirmación con dueño y gente adentro); *Nueva shell persistente* en el menú del servidor; aviso al abrir una ocupada.

**Pasos:** tests de `tmux.ts` (dueño, gente adentro, orden), implementar, `npm test`, `npm run check`, commit.

### Tarea 6: cierre

CHANGELOG (nueva sección "Sin publicar"), `go test ./...`, `npm test`, build `wails build`, prueba a mano en WSL por SSH si hay `sshd`, revisión de toda la rama.

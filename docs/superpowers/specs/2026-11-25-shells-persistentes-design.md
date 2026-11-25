# Shells persistentes (tmux por debajo) — diseño

Fecha: 2026-11-25 · Rama: `feature/shells-persistentes` (desde `main`, v1.1.5-beta.1)

## Para qué

Hoy una terminal SSH de osat muere en el servidor cuando se cierra la pestaña, se cierra osat o se corta la red. La forma de que sobreviva es tmux, pero convivir con tmux cuesta: entrar y salir de sesiones, el prefijo, navegar sus paneles, el modo copia para ver el historial. tmux no es mala herramienta; es poco versátil si no la sabés usar.

La idea: **la persistencia de tmux con la interfaz de osat.** tmux queda por debajo, solo para que la shell siga viva; las pestañas y el mosaico los pone osat. El usuario nunca toca tmux.

El escenario real: servidores a los que entran **varias personas con el mismo usuario de Linux**, cada una con su osat. Todos ven las shells persistentes de todos.

## Objetivos

- Crear desde osat una **shell persistente** en un servidor SSH, con el nombre que elijas.
- Cerrar la pestaña (o osat, o que se corte la red) **no la mata**: sigue en el servidor.
- En la **barra lateral**, debajo de cada servidor, ver todas las shells persistentes que hay en ese servidor (las tuyas, las de otros osat y las sesiones tmux armadas a mano), aunque no tengas ninguna pestaña abierta.
- Reabrir cualquiera con doble clic, con el historial reciente cargado y el scroll normal de osat.
- **Renombrar** una shell en cualquier momento (renombra la sesión de tmux; todos ven el nombre nuevo).
- **Terminar** una shell explícitamente.
- Saber de quién es cada una y si hay alguien adentro; avisar antes de entrar a una ocupada y permitir **solo mirar**.

## No objetivos

- Manejar ventanas y paneles de tmux con la interfaz de osat (modo control, `tmux -CC`). Queda para una segunda etapa si hace falta. Las sesiones armadas a mano con varias ventanas/paneles se abren con un `tmux attach` común y se ven con su tmux tal cual.
- Instalar tmux o cualquier binario propio en el servidor. Si no hay tmux, la opción no está disponible.
- Ver sesiones de otros usuarios de Linux (sockets distintos) ni armar sockets compartidos.
- Sobrevivir a un reinicio del servidor.
- Android (rama `android-mvp`).

## Anti-objetivos (fracaso aunque funcione)

- Que el usuario tenga que saber tmux para algo del uso diario (prefijo, `Ctrl+B d`, modo copia).
- Tocar el `.tmux.conf` compartido, las opciones globales del servidor tmux o las opciones de las sesiones armadas a mano: esas sesiones tienen que seguir viéndose igual que antes (renombrarlas o terminarlas a pedido del usuario sí se puede).
- Que expandir un servidor en la barra lateral pida contraseñas o abra conexiones sin que el usuario lo pida.
- Que una shell persistente se mate sin querer (cerrar pestaña ≠ terminar).

## Enfoques considerados

1. **tmux por debajo, una sesión = un panel, el mosaico lo pone osat (elegido).** Sin binario propio; anda donde hay tmux. Las sesiones de otros/armadas a mano se listan y se abren con attach común. El modo control se puede sumar después sin rehacer nada.
2. **Modo control de tmux (`-CC`) para todas las sesiones.** Cada ventana/panel de tmux se vuelve pestaña/panel de osat. La experiencia más completa, pero varias veces más trabajo (protocolo, traducir layouts, tamaños con varios clientes). Descartado para esta etapa.
3. **Cliente propio estilo `dtach` subido por SFTP.** Anda sin tmux y el scroll es perfecto, pero hay que compilar/mantener/actualizar un binario por arquitectura y falla en homes `noexec` o servidores con políticas. Descartado: el problema del usuario es la experiencia de uso, no depender de tmux.

Se eligió el 1 porque resuelve el problema (la experiencia) por una fracción del costo y no cierra la puerta al 2.

## Cómo funciona (lo decidido)

### En el servidor

- Se usa el **socket por defecto de tmux** (no `-L osat`), porque ahí están las sesiones de todos.
- osat solo cambia **opciones de sus propias sesiones** (`set -t <sesión> …`), nunca globales: barra de estado apagada, prefijo apagado, mouse de tmux apagado.
- Cada sesión creada por osat guarda opciones de usuario de tmux: `@osat_owner` (quién la creó, `usuario@equipo`) y `@osat_created` (fecha). Con eso se distingue *mía*, *de otro osat* y *tmux a mano* (sin `@osat_owner`).
- Todas las operaciones (listar, crear, renombrar, terminar) van por **un canal de ejecución aparte**, como la barra de recursos (`internal/remote/stats.go`), sin escribir en ninguna shell.
- Las sesiones se apuntan por su `session_id` de tmux (`$3`), no por nombre, para que un renombre hecho por otro no rompa una pestaña abierta.

### Crear

- Clic derecho sobre un servidor SSH → **Nueva shell persistente**. Pide el nombre; si se deja vacío, osat propone uno (el nombre del servidor y un número).
- Nombres: tmux no acepta `.` ni `:`; osat los reemplaza y evita duplicados.
- Si el servidor no tiene tmux, osat lo dice y no crea nada.

### Barra lateral

- Debajo de cada servidor SSH, sus shells persistentes: nombre, dueño (*mía* / *de juan@PC-JUAN* / *tmux*), cuánta gente está adentro y hace cuánto tuvo actividad.
- La lista se actualiza sola (cada ~30 s y al crear/renombrar/terminar) **mientras osat tenga una conexión abierta a ese servidor**: cualquier pestaña viva de esa conexión (terminal, SFTP, reenvío de puertos) cuenta; se usa su cliente SSH. Sin conexión, se ve la última lista conocida (en memoria, no en disco) y un botón **Actualizar**, que conecta, lista y cierra (no deja conexiones de fondo). *(Decisión de diseño.)*
- "Gente adentro" cuenta **otros** clientes: `session_attached` menos las pestañas que este osat tiene abiertas en esa sesión. *(Decisión de diseño.)*
- Menú contextual: Abrir, Solo mirar, Renombrar, Terminar.
- **Todas las sesiones del usuario de Linux se tratan como propias**, las haya creado este osat, otro osat o alguien a mano: todas se pueden abrir, mirar, renombrar y terminar. El dueño se muestra solo para informar, no restringe nada.

### Abrir

- Doble clic → pestaña de terminal conectada a la sesión (`tmux attach`), con el historial reciente cargado y el scroll normal de osat.
- Si hay alguien más adentro, osat avisa y ofrece **Entrar igual** o **Solo mirar** (`attach -f read-only,ignore-size`, para que quien mira no le achique la pantalla a quien trabaja). Requiere tmux 3.1 o más nuevo; con uno más viejo, *Solo mirar* no aparece. *(Decisión de diseño.)*
- Las shells persistentes **no** usan el arranque limpio (`cleanStart`): tienen el prompt propio del usuario, porque no se escribe nada en la shell. *(Decisión de diseño.)*
- Las sesiones *tmux a mano* se abren con attach común y se ven con su propio tmux.

### Cerrar, terminar, renombrar

- Cerrar la pestaña o osat = desconectarse; la shell sigue viva.
- **Terminar shell** (barra lateral o pestaña) = `kill-session`, con confirmación; la confirmación dice de quién es (si no es de este osat) y avisa si hay otras personas adentro.
- **Renombrar** desde la pestaña (doble clic/F2, como hoy) o la barra lateral = `rename-session`. El nombre de la pestaña sigue al de la sesión.

### Con lo que ya existe

- **Reconexión automática:** si se corta la red, la reconexión vuelve a conectarse a la misma sesión en vez de abrir una shell nueva.
- **Espacios de trabajo:** al restaurarse, las pestañas de shells persistentes vuelven a conectarse a la misma sesión; si ya no existe, se avisa y se omite.
- **Puente con Claude Code, log de sesión y barra de recursos:** funcionan como en cualquier terminal SSH.
- **Dueño:** `usuario@equipo` del sistema operativo, sin configurar nada.

## Restricciones

- Go + Wails, Svelte 4. Seguir el estilo del repo: comentarios y textos en español, textos visibles por `i18n`, CHANGELOG en formato Keep a Changelog.
- No escribir nada en la shell del usuario ni en archivos del servidor.
- Funcionar con saltos (ProxyJump) y con la VPN como cualquier conexión SSH.

## Lo que queda para quien lo construya

- ~~Cómo se logra el scroll natural.~~ **Resuelto en la prueba rápida (2026-11-25):** ignorar la pantalla alternativa del cliente tmux pierde líneas cuando la salida es rápida (tmux redibuja). En cambio, conectarse en **modo control** (`tmux -C attach`, por un canal sin pty) da exactamente los bytes que escribió la shell (`%output`), sin pérdidas (250/250 líneas). Como cada shell persistente tiene un solo panel, no hace falta traducir layouts: las teclas van con `send-keys -H`, el tamaño con `refresh-client -C cols,rows`, y al abrir se carga el historial con `capture-pane -p -e`. Así tmux no dibuja nada: **no hace falta apagarle barra ni prefijo** a las sesiones. Quien entra a *Solo mirar* no manda `refresh-client` (si no, cambia el tamaño de todos). tmux ya reemplaza `.` y `:` en los nombres. Las sesiones con varias ventanas/paneles se siguen abriendo con `tmux attach` común en un pty. Condición original: el puente con Claude Code tiene que funcionar, y hoy `TerminalReady` (`internal/remote/bridge.go`) rechaza terminales en pantalla alternativa; la solución tiene que dejar la terminal de osat fuera de la pantalla alternativa (o adaptar ese chequeo para estas sesiones). tmux dibuja en la pantalla alternativa y redibuja, así que la rueda del mouse en xterm no muestra historial. El objetivo es que el scroll se sienta como en una terminal normal sin tocar opciones globales de tmux. Probar primero con una prueba rápida; candidatos: cargar el historial con `capture-pane -p -e -S -<n>` al abrir y que el cliente de osat no entre en pantalla alternativa al conectarse; o ir pidiendo historial con `capture-pane` al scrollear.
- **Versión mínima de tmux** y qué opciones existen en cada una (por ejemplo `prefix None`, `new-session -e`); comportamiento con versiones viejas.
- Cómo se aplican las **variables de entorno** y el **comando inicial** de la conexión a una sesión nueva (solo al crearla).
- **Reenvío del agente SSH:** dentro de tmux, `SSH_AUTH_SOCK` queda viejo al reconectarse; ver si se puede resolver o documentarlo como limitación.
- Detalle visual de la barra lateral (iconos, cómo se muestran las tres clases de sesión).

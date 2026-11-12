# osat: referencia y desarrollo

Cliente SSH + SFTP + Escritorio remoto, todo en uno (reemplazo de PuTTY + WinSCP + mRemoteNG) para Windows, Linux y macOS, hecho con **Go + Wails v2 + Svelte/TypeScript**. Descargalo de [Releases](https://github.com/brunomick/osat/releases/latest).

## Funcionalidades (v1.0)

- **Gestor de conexiones**: servidores agrupados, búsqueda, colores, pegar `usuario@host:puerto` en el campo host.
  - **Grupos anidados** (`A/B`): clic derecho en la barra lateral para agregar un grupo (y, sobre un grupo, *Editar nombre* para renombrarlo); arrastrá grupos dentro de grupos y servidores dentro de grupos. Los grupos vacíos se conservan. Los grupos que plegás siguen plegados al cerrar y abrir osat.
- **Importar configuración** (en *Nueva conexión*): trae las sesiones de **PuTTY**, **WinSCP**, **mRemoteNG** y los `Host` de **OpenSSH** (`~/.ssh/config`, con `Include`, comodines y `Host *`) de este equipo, o de un archivo exportado (`.reg`, `WinSCP.ini`, `confCons.xml`, `config`). Importa host, puerto, usuario, dominio, archivo de clave y carpetas como grupos; las contraseñas no (osat las pide al conectar). Las que ya existen se marcan y vienen sin seleccionar. Antes de importar podés renombrar cada grupo (por ejemplo el "PuTTY" que no tiene carpetas) y poner una ruta como `Clientes/Web`, o tildar *No asociar grupo* para importarlas todas sueltas.
- **Exportar a `.osat`**: las conexiones elegidas (con sus carpetas fijadas, y si querés con contraseñas y claves) a un archivo cifrado con contraseña (Argon2id + AES-256-GCM), para respaldarlas o pasarlas a otra PC e importarlas desde *Importar*. Con *Incluir los snippets* e *Incluir los sitios web* van también todos los snippets y sitios (se puede exportar solo eso, sin conexiones), y con *Incluir las credenciales por defecto* (apagada de entrada), las credenciales por defecto con su contraseña.
- **Credenciales seguras**: contraseñas y passphrases en el keyring del SO (Windows Credential Manager / macOS Keychain / Secret Service). El JSON de conexiones nunca guarda secretos.
- **Credenciales por defecto** (Ajustes → *Credenciales por defecto*): un usuario, una contraseña (en el keyring del SO) y un dominio para RDP que comparten varias conexiones. Las usan las que tienen *Usar credenciales por defecto* (casilla en el editor de la conexión, o clic derecho en un grupo para marcar o desmarcar todas sus conexiones; llevan una llave en la barra lateral) y, si les falta, las que no tienen usuario o contraseña propios. Cambiar la contraseña ahí la cambia para todas. Es cómodo cuando muchos servidores comparten usuario y contraseña, pero una sola contraseña abre todos esos servidores.
- **Autenticación**: contraseña (+ keyboard-interactive), clave privada OpenSSH o PuTTY `.ppk` v2/v3 (con o sin passphrase, y con certificado de usuario si está el `-cert.pub` junto a la clave), agente SSH (Pageant / OpenSSH agent de Windows / ssh-agent).
- **Reenvío del agente SSH** (conexiones SSH, en *Opciones avanzadas*, desactivado por defecto): el servidor puede usar las claves del agente de esta PC (Pageant / OpenSSH agent / ssh-agent) sin que salgan de ella, por ejemplo para un `git pull` en el servidor. Solo se reenvía al servidor final, no a los saltos. Mientras la sesión esté abierta, root en el servidor puede usar tu agente: activalo solo en servidores de confianza. Al importar de OpenSSH, `ForwardAgent yes` lo activa.
- **Verificación de host keys**: `known_hosts` propio, confirmación de huella la primera vez, alerta ante cambio de clave (posible MITM).
- **Saltar a través de un bastión** (ProxyJump): en la conexión (SSH o RDP), *Proxy Jump* con otra conexión guardada; terminal, SFTP, reenvío de puertos y escritorio remoto (RDP) pasan por el salto, autenticando desde tu PC. Se pueden encadenar saltos; cada uno verifica su propia huella. Al importar de OpenSSH, `ProxyJump otro-host` se enlaza solo.
- **Terminal en pestañas** (xterm.js): colores 256/truecolor, redimensionado, links clickeables, zoom, reconexión con Enter. Las terminales SSH arrancan limpias, sin el MOTD ni el banner de bienvenida.
  - **Reconexión automática**: si se cae la conexión de una terminal SSH o de un explorador SFTP (también por un salto), osat reintenta solo, esperando 1, 2, 4, 8, 16 y después 30 segundos entre intentos, sin límite. La terminal muestra "Reconectando (intento N, próximo en S s)…" (`Enter`: reintentar ya, `Esc`: cancelar) y el explorador lo mismo con botones. Si el sistema avisa que volvió la red, reintenta enseguida. Al reconectar, la terminal abre un shell nuevo debajo de lo que había, con una línea "── reconectado ──", y el explorador vuelve a la carpeta en la que estaba. No reintenta solo si la sesión terminó con `exit`, si hace falta una contraseña, frase o código que no está guardado, si falló la autenticación, si la huella del servidor es nueva o cambió, o si la conexión se borró: ahí queda el "Presioná Enter" (o *Reconectar*), que pregunta lo que haga falta. No aplica a las terminales de esta PC ni a RDP.
  - **Snippets** (comandos guardados): `Ctrl+Shift+S` o *Snippets…* en el menú de la pestaña abre un panel sobre la terminal con buscador (por nombre, carpeta y comando) y los snippets agrupados por carpeta. Cada uno se **pega** (se escribe sin Enter, para revisarlo) o se **ejecuta** (se escribe con Enter); con Enter o doble clic se usa el modo guardado. Desde el panel se crean, editan, duplican y borran. Un comando puede tener varias líneas y variables `{{nombre}}`: al usarlo, osat pide cada una (una sola vez aunque se repita) y la reemplaza tal cual; cancelar no manda nada. Cada snippet es **global** (en todas las terminales, también las de esta PC) o de un **grupo** de conexiones (en las de ese grupo y sus subgrupos), y sigue al grupo si lo renombrás o movés. Se guardan en `snippets.json` y viajan en los `.osat` si al exportar queda marcado *Incluir los snippets* (van todos).
  - **Broadcast**: `Ctrl+Shift+M` o *Broadcast…* en el menú de la pestaña abre un diálogo con las terminales abiertas y conectadas (las que se ven en el mosaico vienen marcadas). Con dos o más elegidas, lo que escribas o pegues en cualquiera va a todas. Mientras está activo, cada terminal que recibe tiene un borde de color fuerte y una barra arriba dice "Escribiendo en N terminales" con el botón **Detener** (`Ctrl+Shift+M` también lo detiene). Una terminal que se cierra o se cae sale sola, y con menos de dos el broadcast se detiene. En el panel de snippets, mientras está activo, aparece *Ejecutar en las N terminales*. Solo terminales: no participan las de SFTP ni RDP, ni las terminales embebidas del explorador SFTP.
- **Comando inicial y variables de entorno** (conexiones SSH, en *Opciones avanzadas*): un comando que se escribe en el shell al abrir cada terminal (si termina, seguís en el shell) y variables `NOMBRE=valor` que se mandan al servidor. `sshd` solo acepta `LANG` y `LC_*` salvo que se configure `AcceptEnv`; las que rechaza se avisan en gris en la terminal.
  - Copiar/pegar: `Ctrl+Shift+C` / `Ctrl+Shift+V`, `Shift+Insert`, o clic derecho estilo PuTTY.
  - **Guardar como TXT…** (menú de la pestaña, o el de la terminal si el clic derecho abre un menú): guarda en un `.txt` todo lo que la terminal tiene en memoria (el historial, hasta el límite de *Historial* en Ajustes, y la pantalla), sin colores y con las líneas largas unidas. Para tener cada sesión completa desde el principio está el registro automático de Ajustes → Terminal.
- **Explorador SFTP**: navegación con breadcrumbs, ordenar, filtrar, multiselección, subir/descargar archivos **y carpetas** con progreso y velocidad, cancelación, **drag & drop desde el Explorador de Windows** (o el gestor de archivos del sistema; arrastrar desde osat hacia afuera, por ahora solo en Windows), renombrar, eliminar, permisos (chmod), crear carpeta/archivo y **editor rápido** de archivos de texto (`Ctrl+S`).
  - **Buscar archivos** (lupa de la barra o `Ctrl+F`): busca dentro de la carpeta actual y sus subcarpetas, **por nombre** con comodines (`*.conf`, `nginx*`; sin distinguir mayúsculas) y, si querés, **por contenido** ("que contenga el texto…": texto literal, se saltean los binarios y los archivos de más de 10 MB). Usa el `find` (y el `grep`) del propio servidor, así que no pasan bytes por esta PC, y los resultados aparecen a medida que llegan, con su ruta relativa a la carpeta buscada. Doble clic abre el archivo (o navega a la carpeta); clic derecho: *Mostrar en su carpeta* y *Copiar ruta*. Muestra como máximo 1.000 resultados y *Detener* corta la búsqueda también en el servidor; las carpetas sin permiso se saltean sin errores. Lo que escribís nunca se ejecuta (un nombre como `; rm -rf ~` se busca literalmente). Si el servidor no tiene `find` o no deja ejecutar comandos, la búsqueda por nombre recorre las carpetas por SFTP (más lento, con el mismo límite) y la de contenido queda deshabilitada con la explicación.
  - La barra del explorador es solo de íconos (el nombre aparece al pasar el mouse); en un panel angosto se achican la ruta y el filtro y, si igual no entra, los botones pasan a una segunda fila.
  - **Panel de transferencias** (abajo): se abre al empezar una transferencia y, cuando todas terminan bien, se colapsa solo a los 2 segundos; queda la barra *Transferencias (n)* para volver a abrirlo. Si alguna falló o terminó con aviso, queda abierto. *Limpiar terminadas* las borra.
  - **Reintentar y reanudar transferencias**: una transferencia que falló tiene *Reintentar* en el panel de transferencias, que usa una conexión SFTP abierta a ese servidor (la del explorador, si se reconectó). Si una subida o descarga de un archivo de más de 1 MB se cortó por la conexión, lo ya transferido se conserva (en `archivo.osat-part` al bajar, `.archivo.osat-part` en el servidor al subir) y el reintento sigue desde ahí, pero solo si el origen no cambió de tamaño ni de fecha y el final de lo transferido coincide con el origen; si no, empieza de cero con un aviso en la fila. Las carpetas y los archivos chicos se reintentan de cero, con la misma decisión sobre conflictos que el intento original: si no se eligió *Reemplazar*, el reintento solo reemplaza lo que escribió la propia transferencia y deja como estaba cualquier otro archivo que encuentre en el destino (lo avisa en la fila). Cancelar borra lo transferido como siempre, y lo que se puede reanudar dura mientras osat esté abierto.
  - **Sincronizar carpetas** (`Sincronizar…` en la barra o en el menú de la carpeta), como el "Sincronizar" de WinSCP: compara la carpeta del servidor (la actual, editable) con una de esta PC y copia lo que haga falta. Dirección: **subir**, **bajar** o **ambas** (gana el más nuevo). Modo: **sincronizar** (copia solo lo que es más nuevo en el origen) o **espejo** (deja el destino igual al origen aunque el origen sea más viejo; solo con una dirección). Compara por **fecha y tamaño** (con 2 segundos de tolerancia), **solo tamaño** o **contenido** (`sha256sum` en el servidor, solo para los archivos de igual tamaño). Opcionalmente borra en el destino lo que no está en el origen (apagado por defecto, solo con una dirección; en esta PC se borra definitivamente, sin papelera) y excluye patrones que se comparan con el nombre y con la ruta relativa (por defecto `.git, node_modules`, que tampoco se recorren). *Comparar* muestra siempre una vista previa con una casilla por archivo, el motivo, los borrados en rojo y un resumen; *Sincronizar* ejecuta solo lo marcado como una transferencia del panel, con avance y cancelación. Cada archivo copiado queda con la fecha de modificación del original, así una segunda comparación no encuentra diferencias. Las carpetas vacías se crean; los enlaces simbólicos no se siguen ni se copian (se listan como omitidos). Copia archivos enteros (no es una transferencia delta como rsync) y no se mantiene sincronizado solo.
  - **Comprimir en el servidor**: clic derecho sobre lo seleccionado (archivos y carpetas de una misma carpeta) → *Comprimir en .zip* o *Comprimir en .tar.gz*. Se crea el archivo en esa carpeta con el `zip` o `tar` del propio servidor, así los bytes no pasan por esta PC. El botón elige el formato y pide el nombre (si no termina en esa extensión, se la agrega). Nunca pisa un archivo que ya exista, y las rutas dentro del archivo son relativas (`carpeta/archivo`). Necesita que el servidor deje ejecutar comandos y tenga el programa (`zip` no viene en las instalaciones mínimas; si falta, lo dice y sugiere `.tar.gz`).
  - **Extraer aquí**: clic derecho sobre un `.tar.gz`, `.tgz`, `.tar.bz2`, `.tar.xz`, `.tar.zst`, `.tar` o `.zip` → se extrae en el servidor (con `tar` o `unzip`). Como en 7-Zip o el Explorador: si el archivo trae una sola carpeta en la raíz y no existe ya acá, se extrae directo; si no, en una carpeta nueva con el nombre del archivo. **Nunca pisa nada** (si algo ya existe, va a otra carpeta o `unzip -n` lo deja como está), rechaza archivos con rutas absolutas o que suben de carpeta, y si falla borra lo que había creado.
  - **Editar como root (sudo)**: si abrir o guardar un archivo falla por permiso denegado, osat ofrece hacerlo como root; también está *Abrir como root (sudo)* en el menú de un archivo. Lee y guarda con el `sudo` del servidor por la misma conexión (guardar con `sudo tee` no cambia dueño ni permisos), respeta la codificación, los finales de línea y el límite de 2 MB, y se abre en su propia pestaña (desde un repositorio, en su editor) con la marca **root** en rojo; el mismo archivo puede estar abierto normal y como root a la vez. Primero prueba sin contraseña (`NOPASSWD`); si sudo la pide, la pregunta y la recuerda solo en memoria mientras la conexión esté abierta. La contraseña se valida sola (`sudo -v`), nunca junto con el contenido, así que no puede terminar escrita en el archivo y una incorrecta cuenta como un único intento. Necesita que el servidor deje ejecutar comandos; con `requiretty` no funciona, y con `timestamp_timeout=0` se puede leer como root pero no guardar.
- **Este equipo** (panel fijo en la barra lateral; doble clic para abrirlo): los archivos de esta PC en una pestaña, para ponerla al lado de un SFTP como en WinSCP. Arrastrá de uno a otro para subir o descargar; dentro de la PC se mueve, y Supr manda a la Papelera (en Linux, a la papelera del escritorio; lo que está en otro disco y no puede ir, osat ofrece eliminarlo definitivamente). En macOS todavía no hay papelera: `Cmd+Backspace` o Supr eliminan definitivamente, después de confirmarlo.
  - **Carpetas fijadas**: el botón de chincheta de esas pestañas deja la carpeta como acceso rápido bajo "Este equipo". Clic derecho para ponerle nombre propio o soltarla.
  - **Carpetas fijadas del servidor**: en el explorador SFTP, la chincheta de la pestaña o "Fijar carpeta" (clic derecho en una carpeta o en el fondo) las deja bajo su servidor en la barra lateral. Un clic abre el SFTP en esa carpeta; clic derecho para ponerle nombre o soltarla. Se guardan con la conexión y viajan en la exportación `.osat`.
- **Favoritos**: clic derecho en una conexión → *Agregar a favoritos* / *Quitar de favoritos*. La sección **Favoritos** (arriba de los grupos, plegable, solo aparece si hay favoritas) muestra accesos directos: la conexión sigue también en su grupo y la fila se comporta igual (doble clic, botones, menú), sin pestañas ni carpetas fijadas debajo. Se ordenan arrastrando dentro de la sección, con un orden independiente del de los grupos; arrastrar una conexión de un grupo a la sección la agrega (si no hay favoritas, aparece una zona para soltarla). Una favorita arrastrada solo se puede soltar dentro de Favoritos. La búsqueda también las filtra. Es una preferencia personal: se guarda en `connections.json` pero no viaja en la exportación `.osat`.
- **Terminales de esta PC**: en Windows PowerShell, cmd, WSL y Git Bash (las que haya instaladas), con ConPTY; en Linux y macOS la shell del usuario (`$SHELL`) y las de `/etc/shells` (bash, zsh, fish, sh), con una pty. Se abren desde el botón de terminal de "Este equipo" o con "Abrir terminal aquí" en una carpeta, y "Abrir terminal aquí" usa la terminal local por defecto de los ajustes.
- **Puente con Claude Code** (Ajustes → *Claude MCP*): un servidor MCP local que deja a Claude Code (el de esta PC) ver y usar las terminales que compartas, sin instalar nada en los servidores. Ver *Puente con Claude Code*.
- **Reutilización de conexión**: desde una terminal podés abrir SFTP (o viceversa, o "Abrir terminal aquí" en una carpeta) sin volver a autenticarte.
- **Actualizaciones** (`app_update.go`, `internal/updater`): al abrir (si *Buscar actualizaciones al abrir osat* de Ajustes → General está activo, como viene de entrada) osat consulta la última Release de GitHub (`api.github.com/repos/brunomick/osat/releases/latest`, sin token: el repo tiene que ser público). Si es más nueva, muestra sus notas y ofrece *Actualizar ahora*: baja el archivo de este sistema con una barra de progreso (evento `update:progress`), deja corriendo un proceso que espera a que osat se cierre, reemplaza el programa y lo vuelve a abrir, y cierra osat. En Windows, si la carpeta de `osat.exe` se puede escribir (el portátil) reemplaza el `.exe` por el del `.zip`; si no (instalado en Archivos de programa), corre el instalador con `/S` y permisos de administrador (Windows lo pregunta; si se cancela, vuelve a abrir la versión que estaba). En Linux reemplaza el binario (tiene que estar en una carpeta del usuario, como `~/.local/bin` de `instalar.sh`) y en macOS `osat.app`; si no se puede escribir ahí, ofrece la página de descarga. En Android no se actualiza solo. La primera vez que se abre una versión nueva muestra sus **novedades**, sacadas del `CHANGELOG.md` que trae adentro; la última versión abierta queda en el archivo `version` de la carpeta de datos (una instalación nueva no muestra nada). Para probar contra un servidor local: `OSAT_UPDATE_URL=http://…/latest` (el mismo JSON que da la API de GitHub). Con *Recibir versiones beta* (apagado de entrada) consulta en cambio la lista de Releases (`/releases`, o `OSAT_UPDATE_LIST_URL`) y ofrece la versión más alta, estable o beta: una beta (`1.1.0-beta.2`) es anterior a su estable (`1.1.0`) y las betas se ordenan por número. Un tag con guion (`v1.1.0-beta.1`) se publica como pre-release, que `releases/latest` no devuelve; en una beta, `wails.json` lleva la versión sin el `-beta.N` (la del .exe y del instalador tiene que ser numérica).
- **Ajustes** (engranaje de la barra lateral o `Ctrl+,`): apariencia de la interfaz (oscuro, claro o igual que el sistema), colores (osat oscuro o claro, Solarized, Dracula, One Dark, Gruvbox), fuente, tamaño de letra, líneas de historial, cursor, clic derecho (copiar o pegar como PuTTY, o un menú con Copiar, Pegar, Seleccionar todo, Buscar y Limpiar pantalla) y campana (visual o sonido) de la terminal, que se aplican al instante a las terminales abiertas; preguntar o no dónde descargar, carpeta de descargas y qué hacer si el archivo ya existe en el destino (subidas, descargas y arrastres); la terminal local por defecto, el registro detallado del log y si se restaura la sesión al abrir osat (activado por defecto); las credenciales por defecto, el bloqueo y *Claude MCP* (el puente con Claude Code). Se guardan en `settings.json`; "Restaurar valores por defecto" los devuelve a su valor original.
- **Barra lateral ocultable**: el botón de menú junto a "Servers" (o `Ctrl+Shift+B`) la oculta y deja una franja angosta para volver a mostrarla; se recuerda al reabrir osat.
- **Espacios de trabajo**: con *Restaurar la sesión al abrir osat* (Ajustes → General, activado por defecto), al abrir osat vuelven las pestañas de la última vez, los paneles del mosaico y sus proporciones. En la barra lateral, la sección plegable **Workspaces** (debajo de "Este equipo") guarda el espacio actual con un nombre (*Guardar el espacio actual…*); un clic en uno lo abre y **reemplaza** las pestañas abiertas (pide confirmación si hay alguna), y con clic derecho se abre, renombra, actualiza con el espacio actual o borra. Se guarda lo necesario para reabrir cada pestaña (conexión, carpeta del SFTP o de "Este equipo", shell y carpeta de las terminales locales, archivo del editor, carpeta del repositorio, dirección del navegador), **nunca** contraseñas ni lo que había en las terminales. Cada pestaña restaurada se conecta recién cuando la mostrás, así un workspace con 8 servidores no pide 8 contraseñas juntas; varias pestañas del mismo servidor comparten la conexión y piden la contraseña una sola vez. No vuelven las pestañas de una conexión que ya no existe ni los editores que tenían cambios sin guardar: osat avisa en una línea cuáles. La terminal arranca con un shell nuevo.
- **Abrir como repositorio** (SFTP): clic derecho en una carpeta del servidor (o el botón de repositorio de la barra) la abre como un proyecto de VS Code: a la izquierda el árbol de carpetas y archivos, a la derecha los archivos abiertos, cada uno con su editor (colores, búsqueda, `Ctrl+S`) y sin perder lo que no guardaste al cambiar de uno a otro. Desde el árbol también se crean, renombran y borran archivos y carpetas.
- **Control de versiones** (`internal/remote/git.go`, `GitPanel`, `DiffView`): si la carpeta del repositorio está en un repositorio git, el árbol marca los archivos modificados, nuevos y borrados (y el color de las carpetas que los tienen), y el ícono de ramas de la barra de la izquierda abre un panel como el de VS Code: mensaje y *Commit* (`Ctrl+Enter`; sin nada preparado ofrece preparar todo), listas de *Preparados* y *Cambios* con preparar, sacar y descartar, y en el menú *Commit y push*, pull, push, fetch y ramas. Un clic en un archivo abre sus cambios con CodeMirror (`@codemirror/merge`), lado a lado o unificado: los de *Cambios* comparan lo preparado con el archivo del servidor y los de *Preparados*, el último commit con lo preparado. Abajo, la barra de estado muestra la rama (clic: cambiar o crear), los commits por subir y por traer (clic: sincronizar, o publicar la rama si no sigue a ninguna) y de dónde es el remoto. Todo corre con el git **del servidor** por un canal de comandos de la misma conexión SSH (`sh -c`, con `GIT_TERMINAL_PROMPT=0` para que falle en vez de esperar una contraseña; con el reenvío del agente activo, el push por SSH usa las claves de esta PC). El pull es `--ff-only`: si las ramas se separaron, se resuelve en una terminal. Si git no sabe quién commitea, osat pregunta nombre y email y los guarda con `git config` solo en ese repositorio. El estado se relee al guardar un archivo, al volver a la pestaña y después de cada acción. Prueba con git de verdad: `go test -tags shelltest -run TestGitShell ./internal/remote/`.
- **Navegador integrado**: una pestaña que muestra páginas dentro de osat. Clic derecho en un archivo (o carpeta) del servidor → *Abrir en el navegador integrado*: se sirve su carpeta, así cargan sus estilos, scripts e imágenes. En la barra de direcciones, con un servidor, `localhost:3000` es el puerto 3000 **del servidor** (llega por la conexión SSH, sin abrir nada en la red) y una ruta como `/var/www/index.html` es un archivo suyo; desde "Este equipo" (botón del globo) es `localhost` de esta PC. Ver *Notas de diseño*.
- **Reenvío de puertos**: el botón de flechas de un servidor en la barra lateral abre una pestaña *Puertos*. Cada reenvío es un puerto de **tu PC** (solo `localhost`) que llega, por la conexión SSH, a un puerto del servidor; tus programas (DBeaver, `redis-cli`, Postman, el navegador...) se conectan a `localhost:PUERTO` como si el servicio estuviera acá. Se crean con atajos (Redis, RabbitMQ, PostgreSQL, MySQL, MongoDB...) o a mano, con un interruptor para iniciar y detener, el estado y los datos de uso a la vista, copiar la dirección y abrirlo en el navegador integrado. Se guardan por servidor y pueden arrancar solos al abrir la pestaña. Cerrar la pestaña los detiene (pide confirmar si hay alguno en marcha).
  - **Remoto** (el `-R` de ssh): al revés, un puerto **del servidor** que llega a un puerto de tu PC (por defecto `localhost`), por ejemplo para mostrarle desde el servidor algo que corre en tu PC. El servidor escucha solo en su `localhost`, salvo que tildes *Escuchar en todas las interfaces del servidor* (y el servidor tenga `GatewayPorts` habilitado). Si el servidor no deja abrir el puerto (ocupado, menor a 1024 sin ser root, `AllowTcpForwarding no`), el reenvío queda en error con esas causas posibles.
  - **Dinámico (SOCKS)** (el `-D` de ssh): un proxy SOCKS5 en `localhost` que manda cada conexión por el servidor hacia el destino que pida el programa; solo hay que elegir el puerto de tu PC. Configuralo como proxy SOCKS5 en el navegador, o probalo con `curl --socks5-hostname localhost:PUERTO https://ejemplo.com` (los nombres los resuelve el servidor). Solo soporta `CONNECT`, sin usuario ni contraseña; no hay SOCKS4, `BIND` ni UDP, y no configura el proxy del sistema. Nunca escucha fuera del loopback.
  - **Kubernetes**: la pestaña *Kubernetes* del diálogo lista namespaces, servicios y pods con el `kubectl` **del servidor** y reenvía el que elijas (`kubectl port-forward` por SSH). Hace falta que ese servidor tenga `kubectl` y acceso al cluster.
- **Sitios web**: la sección *Sitios* de la barra lateral (arriba de Workspaces) guarda páginas de internet, como los paneles de tu hosting o de la nube, y las abre en una pestaña con un navegador de verdad: WebView2 en Windows y WebKitGTK 4.1 en Linux. El login se mantiene entre reinicios porque el perfil es propio y fijo (`browser-profile`, en la carpeta de datos de osat). Un clic abre el sitio; clic derecho: *Abrir*, *Editar* y *Quitar*; el **+** del encabezado agrega uno (nombre y dirección); se ordenan arrastrando, se pliega la sección y la búsqueda los filtra. En la pestaña, la ⭐ guarda o quita la página actual de Sitios, y una página que pide una ventana nueva se abre en otra pestaña. Solo `http` y `https`; las páginas no pueden llamar a osat. Los sitios viajan en el `.osat` si al exportar queda marcado *Incluir los sitios web*. Se apaga en *Ajustes → General → Sitios web*, que solo aparece donde hay navegador nativo: se cierran los sitios abiertos y vuelve a prenderse sin reiniciar. No es el navegador integrado de abajo (ese muestra `localhost` del servidor).
- **Paneles en mosaico**: el área principal se divide en paneles verticales u horizontales (anidables, con divisores arrastrables), cada uno con su barra de pestañas. Arrastrá una pestaña (una réplica flota junto al mouse y la original queda atenuada) al borde de un panel para dividirlo, al centro o a otra barra para moverla; o usá *Dividir a la derecha / abajo* en su menú. Mover pestañas no corta sus sesiones.
- **Escritorio remoto (RDP)** dentro de una pestaña, en Windows, Linux y macOS y sin depender de ningún cliente RDP del sistema: el protocolo lo implementa [IronRDP](https://github.com/Devolutions/IronRDP) (compilado a WASM) con NLA, portapapeles y resolución que se adapta a la pestaña. Sin audio ni reconexión automática; NLA solo con NTLM (sin Kerberos). Ver *Notas de diseño*.
  - **RD Gateway**: si al equipo solo se llega por una puerta de enlace (el *Servidor de puerta de enlace* del `.rdp` de Windows), se carga en el campo *RD Gateway* de la conexión. Usa el mismo usuario, dominio y contraseña que el equipo. Si el gateway pide aprobar el ingreso en el celular, la pestaña lo avisa y espera hasta 90 segundos; hay una sola aprobación por conexión (dos la primera vez si hay que aceptar el certificado del equipo). No se combina con Proxy Jump. Al importar de mRemoteNG se trae el gateway de cada conexión.
- **Escritorio remoto VNC** en una pestaña, en Windows, Linux y macOS, con [noVNC](https://github.com/novnc/noVNC). En el editor de conexiones, el protocolo *VNC* pide host, puerto (5900 por defecto) y contraseña, que va al almacén de credenciales del sistema (se deja vacía si el servidor no pide ninguna). Se llega directo o *a través de* una conexión SSH, con el mismo *Proxy Jump* que RDP; para pasar por el SSH del propio servidor, elegí su conexión SSH y poné `localhost` como host. La imagen se escala a la pestaña sin cambiar la resolución del servidor; hay portapapeles de texto en las dos direcciones (con la misma limitación que RDP: solo en Windows), un botón *Ctrl+Alt+Supr* y, si se corta, un aviso con *Reconectar*. Anda con grupos, favoritos, búsqueda, exportación `.osat`, paneles en mosaico, workspaces y restaurar sesión, como RDP. Solo soporta servidores sin autenticación o con contraseña VNC: VeNCrypt/TLS, RealVNC y Apple (ARD) no, y la pestaña lo dice. Sin audio, transferencia de archivos ni cambio de resolución del servidor. Ver *Notas de diseño*.

### Bloqueo con contraseña

En Ajustes → Seguridad, **Pedir contraseña para abrir osat** define una contraseña (4 caracteres como mínimo, con confirmación). Para cambiarla o desactivar el bloqueo hay que dar la actual.

- **Al abrir osat** aparece la pantalla de bloqueo y no se abre ninguna conexión hasta desbloquear, tampoco las de *Restaurar la sesión al abrir osat*. Recargar la ventana no lo saltea: el estado bloqueado lo guarda el programa, no la interfaz.
- **Por inactividad**: *Bloquear tras estos minutos sin uso* (0 = nunca, el valor por defecto). Cuenta como uso el teclado y el mouse dentro de osat, también al escribir en una terminal o un escritorio remoto.
- **Al bloquear no se corta nada**: terminales, SFTP, escritorios remotos, reenvíos de puertos, transferencias y el puente con Claude Code siguen andando por detrás. Con la pantalla bloqueada ninguna tecla ni atajo llega a lo que hay debajo; la barra de título queda libre para minimizar o cerrar la ventana.
- **Windows Hello** (solo Windows, si la PC lo tiene configurado): se ofrece al bloquear y con un botón, y se puede apagar con *Permitir Windows Hello*. Si se cancela, se sigue con la contraseña. En Linux y macOS solo hay contraseña.
- Es una **pantalla de bloqueo**: frena a quien se siente en tu PC con osat abierto, pero no cifra las credenciales, que siguen en el almacén del sistema. La contraseña se guarda como hash argon2id. **Si te olvidás la contraseña, osat no abre.**

### Atajos

En macOS, `Cmd` hace de `Ctrl` (salvo `Ctrl+Tab`), y en la terminal también andan `Cmd+C` / `Cmd+V`.

| Atajo | Acción |
|---|---|
| `Ctrl+Shift+N` | Nueva conexión |
| `Ctrl+Tab` / `Ctrl+Shift+Tab` | Pestaña siguiente / anterior |
| `Ctrl+Shift+T` | Nueva pestaña del navegador de sitios web (`Ctrl+T` a secas es de la shell) |
| `Alt+1…9` | Ir a la pestaña N |
| `Ctrl+Shift+W` | Cerrar pestaña (también clic con la rueda) |
| `Ctrl+Shift+B` | Ocultar / mostrar la barra lateral (`Ctrl+B` a secas no se usa: es el prefijo de tmux) |
| `Ctrl+,` | Abrir los ajustes (también el engranaje de la barra lateral) |
| `Menú` / `Shift+F10` | Menú contextual de la fila o pestaña enfocada |
| Flechas | Recorrer la barra lateral (derecha e izquierda abren y pliegan grupos), las pestañas y los menús |
| `Ctrl +` / `Ctrl -` / `Ctrl 0` | Zoom de la terminal (solo de esa terminal; `Ctrl 0` vuelve al tamaño de los ajustes) |
| `Ctrl+Shift+F` | Buscar en la terminal (Enter / Shift+Enter: siguiente / anterior; Esc: cerrar) |
| `Ctrl+Shift+S` | Panel de snippets de la terminal (Enter / doble clic: usar; flechas: elegir; Esc: cerrar) |
| `Ctrl+Shift+M` | Broadcast: elegir las terminales (si ya está activo, lo detiene) |
| Terminal reconectando: `Enter` / `Esc` | Reintentar ya / cancelar la reconexión automática |
| SFTP: `Enter`, `Backspace`, `F2`, `Supr` (en macOS `Cmd+Backspace`), `F5`, `Ctrl+A` | Abrir, subir nivel, renombrar, eliminar, refrescar, seleccionar todo |
| Listas de archivos: arrastrar desde un lugar vacío | Seleccionar con un rectángulo, como en el escritorio (con `Ctrl`/`Shift` se suma a la selección; cerca del borde la lista se desplaza sola) |

## Puente con Claude Code

osat incluye un servidor MCP (HTTP, solo `127.0.0.1`, con token) para que Claude Code, corriendo en esta PC, pueda listar, leer y escribir en las terminales que compartas, **SSH** o de esta PC (PowerShell, cmd, WSL, Git Bash). Los servidores no necesitan nada instalado. Una terminal de esta PC tiene acceso a todo lo tuyo (archivos, claves): compartila solo si querés que Claude pueda usarla. Sirve, por ejemplo, para que un Claude Code corriendo en una terminal maneje otro programa interactivo (como opencode) en otra: lee su pantalla con `read_terminal` y le escribe con `send_input`.

1. El puente viene **encendido** la primera vez (puerto `47800`); se apaga desde **Configuración → Claude MCP**. Ahí mismo está el comando para agregarlo, una sola vez:
   `claude mcp add --transport http osat http://127.0.0.1:47800/mcp --header "Authorization: Bearer <token>"`
2. En cada terminal que quieras mostrarle, tocá el botón de Claude de su pestaña. Mientras está compartida queda marcado; las demás no se ven. Al apagar el puente o cerrar la sesión se deja de compartir.
3. Pegá en `~/.claude/settings.json` las reglas de permisos que muestra el panel.

Herramientas:

| Herramienta | Qué hace | Permiso |
|---|---|---|
| `list_terminals` | Lista las terminales compartidas | corre sin preguntar |
| `read_terminal` | Lee lo que hay en pantalla / el historial | corre sin preguntar |
| `run_readonly_command` | Corre un comando de solo lectura | corre sin preguntar |
| `run_command` | Corre cualquier otro comando | **pide permiso** |
| `send_input` | Escribe teclas o texto en la terminal | **pide permiso** |
| `interrupt` | Manda Ctrl+C | **pide permiso** |

- `run_readonly_command` solo acepta comandos que osat puede verificar (`ls`, `cat`, `grep`, `ps`, `df`, `docker ps`, `git --no-pager log`...). Rechaza encadenar comandos, redirigir, `sudo`, rutas explícitas y todo lo que toque claves o contraseñas; si dudan, el comando se rechaza y hay que usar `run_command`.
- `run_command` y `send_input` exigen que Claude explique qué hace el comando (`what_it_does`) y para qué (`why`); `interrupt` exige `why`. Esas explicaciones las escribe el modelo: leelas, pero no son una garantía.
- **Modos de permisos** (en el panel de Claude): *Con permisos* (el de siempre y el recomendado: lo que solo lee corre sin preguntar y lo demás pide permiso) y *Sin permisos* (Claude corre lo que quiera en las terminales compartidas, sin preguntarte y sin tener que explicar cada comando). Es una decisión grave si compartís un servidor de producción o una terminal de esta PC. Quien pregunta es **Claude Code**, no osat: cambiar el modo cambia las reglas que muestra el panel (en *Sin permisos* las seis herramientas van en `allow`) y lo que osat le dice al modelo, pero hay que pegar esas reglas en `settings.json` y sacar las del modo anterior. El modo se guarda y arranca siempre en *Con permisos*.
- Las reglas de permisos dependen de Claude Code: en sus modos de aceptar todo no se pregunta nada. Dejá `run_command`, `send_input` e `interrupt` en `ask` y no los pases a `allow`.
- Que un comando sea "de solo lectura" no significa que no pueda mostrar datos sensibles (un log con tokens, por ejemplo): el filtro cubre lo conocido, no todo.
- Cambiar el token en el panel invalida el comando ya agregado. Si el puerto `47800` está ocupado, osat espera unos segundos y, si sigue ocupado, usa otro solo por esa sesión (el panel lo avisa).
- La configuración (encendido, puerto, token) está en `bridge.json`, en la carpeta de datos de osat (`%APPDATA%\osat\` en Windows; ver *Estructura*).

## Requisitos

- Go 1.25+
- Node.js 18+
- Wails CLI v2: `go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0` (misma versión que `github.com/wailsapp/wails/v2` en `go.mod`)
  - Las CLI anteriores a v2.16 (compiladas con `golang.org/x/tools` viejo) fallan con Go 1.27+ con `internal error: package "encoding/json" without types`. Si te pasa, reinstalá la CLI con el comando de arriba.
- Windows: WebView2 (viene con Windows 10/11). Corré `wails doctor` para verificar.
- Linux: GTK 3 y WebKitGTK 4.1, y para compilar `gcc` y `pkg-config`. En Ubuntu 24.04 / Debian 12: `sudo apt install build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev`. Las contraseñas van al Secret Service (gnome-keyring, KWallet...): sin uno en marcha osat no puede guardarlas (se pueden escribir al conectar).
- macOS: Xcode Command Line Tools (`xcode-select --install`). Las contraseñas van al Llavero.

## Desarrollo

```bash
go mod tidy          # genera go.sum
wails dev            # app con hot-reload
```

Go baja las dependencias la primera vez que compila (las versiones quedan fijas en `go.sum`). Después de agregar, quitar o actualizar una dependencia, corré `go mod tidy`.

Después de cambiar dependencias (de Go o del frontend), actualizá también los avisos de licencias de terceros en `THIRD_PARTY_NOTICES.md`.

## Build

```bash
wails build                        # → build/bin/osat.exe
wails build -nsis                  # instalador para Windows (requiere NSIS)
wails build -tags webkit2_41       # Linux (en Linux): → build/bin/osat, contra WebKitGTK 4.1
wails build -platform darwin/universal   # macOS (en una Mac)
```

Linux y macOS se compilan en su propio sistema (Wails usa cgo con GTK/WebKit o Cocoa): desde Windows no se puede. En Linux, `wails dev` también necesita `-tags webkit2_41`. Para que osat aparezca en el menú del escritorio, con su ícono:

```bash
install -Dm755 build/bin/osat ~/.local/bin/osat
install -Dm644 build/linux/osat.desktop ~/.local/share/applications/osat.desktop
install -Dm644 build/appicon.png ~/.local/share/icons/hicolor/1024x1024/apps/osat.png
```

(`~/.local/bin` tiene que estar en el `PATH`; si no, poné la ruta completa en `Exec=` del `.desktop`.)

### Paquete para Linux

Para repartir osat a otros usuarios de Linux, en Linux:

```bash
sh build/linux/empaquetar.sh   # → build/bin/osat-linux.tar.gz
```

El paquete trae el programa, el ícono, el acceso del menú (`osat.desktop`) y `instalar.sh`. Quien lo recibe necesita Ubuntu 22.04 o más nuevo, Debian 12 o más nuevo, o un derivado:

1. Instalar WebKitGTK 4.1, una sola vez (en Ubuntu de escritorio suele venir): `sudo apt install -y libwebkit2gtk-4.1-0`.
2. Instalar osat: `tar xzf osat-linux.tar.gz && sh osat-linux/instalar.sh`. No pide `sudo`: deja el programa en `~/.local/bin/osat`, el ícono y el acceso **OSAT** en el menú de aplicaciones. Si falta WebKitGTK, lo avisa.
3. Abrirlo desde el menú, como cualquier aplicación.

Para actualizar, se repite el paso 2 con el paquete nuevo. Las conexiones y los ajustes (`~/.config/osat`) no se tocan.

Sin aceleración gráfica (por ejemplo, en una máquina virtual sin 3D), los sitios web van lentos. No sirve `WEBKIT_DISABLE_DMABUF_RENDERER=1`: activa un camino de WebKitGTK con un error que cierra osat al navegar.

## Estructura

```
main.go                     arranque de Wails (ventana, drag&drop, bindings)
app.go                      métodos expuestos al frontend
internal/store              conexiones (JSON) + secretos (keyring)
internal/importer           importar sesiones de PuTTY, WinSCP, mRemoteNG y OpenSSH
internal/applog             log de osat (logs/osat.log): archivo rotativo, filtro de secretos, últimas líneas y registro de panic
internal/settings           ajustes de osat (settings.json): valores por defecto, validación y archivo dañado
internal/remote/dial.go     autenticación y conexión SSH
internal/remote/hostkeys.go known_hosts propio (confiar / olvidar)
internal/remote/manager.go  sesiones: terminal PTY, SFTP, conexiones compartidas
internal/remote/files.go    operaciones de archivos SFTP
internal/remote/transfers.go subidas/descargas concurrentes con progreso
internal/remote/cleanstart.go terminales SSH sin MOTD (línea oculta + marcador OSC)
internal/remote/localterm.go terminales de esta PC (sesiones sobre internal/localterm)
internal/remote/bridge.go   terminales compartidas: leer, escribir, correr y esperar un comando
internal/localterm          shells de esta PC: PowerShell, cmd, WSL, Git Bash con ConPTY (Windows); $SHELL y /etc/shells con una pty (Linux, macOS)
internal/vt                 emulador de terminal: pantalla, historial y pantalla alterna
internal/readonly           lista estricta de comandos "de solo lectura" para el puente
internal/bridge             servidor MCP (HTTP, loopback, token) con las herramientas del puente
bridge.go                   puente: encendido, puerto, token y permisos (bridge.json)
internal/rdgateway          escritorio remoto: cliente de RD Gateway (MS-TSGU por WebSocket, NTLMv2 con channel binding)
internal/rdpproxy           escritorio remoto: proxy RDCleanPath (WebSocket local ⇄ TCP+TLS) y confianza en certificados
internal/vncproxy           escritorio remoto VNC: proxy WebSocket local ⇄ TCP, directo o por un salto SSH
internal/updater            actualizaciones: última Release de GitHub, descarga, instalación por sistema y novedades del CHANGELOG
internal/web                navegador integrado: sitios locales que sirven una carpeta o hacen de proxy a un puerto
internal/remote/web.go      carpetas del servidor por SFTP y puertos del servidor por la conexión SSH (para internal/web)
internal/portfwd            reenvío de puertos: escucha en loopback y pasa cada conexión a un destino (la conexión SSH)
internal/remote/forwards.go reenvíos en marcha de cada pestaña; k8s.go: kubectl del servidor (listas y port-forward)
internal/store/forwards.go  reenvíos guardados por conexión (forwards.json), con validación estricta
internal/store/snippets.go  snippets guardados (snippets.json): comandos con variables, globales o de un grupo
internal/store/workspaces.go espacios de trabajo (workspaces.json): la última sesión y los guardados con nombre
internal/localfs            archivos de esta PC (panel "Este equipo"); papelera de Windows y de freedesktop (Linux)
internal/osdrag             arrastrar archivos desde osat al Explorador (drag & drop OLE, Windows)
internal/winutil            utilidades Win32 compartidas
frontend/src/lib            api (bindings tipados), stores, diálogos, flujo de conexión
frontend/src/components     Sidebar, TabBar, TerminalView, SftpView, RepoView, BrowserView, Transfers, etc.
```

Los datos se guardan en `%APPDATA%\osat\` (Windows), `~/Library/Application Support/osat/` (macOS) o `~/.config/osat/` (Linux): `connections.json` (conexiones y grupos), `forwards.json` (reenvíos de puertos), `snippets.json` (snippets de comandos), `workspaces.json` (la última sesión y los workspaces con nombre; si está dañado se aparta como `workspaces.json.bak` y se empieza sin ellos), `settings.json` (ajustes; si está dañado se usan los valores por defecto y se aparta como `settings.json.bak`), `known_hosts`, `bridge.json` (puente con Claude Code, solo legible por tu usuario) y `version` (la última versión que se abrió, para las novedades). En la subcarpeta `logs/` está el log de osat (`osat.log`): arranque y cierre, conexiones y saltos, huellas de host, transferencias, reenvíos, escritorio remoto, puente con Claude, errores del frontend y cierres inesperados. Rota al llegar a 5 MB y guarda hasta 3 archivos viejos (`osat.1.log` a `osat.3.log`). Nunca registra contraseñas, frases de claves ni tokens, ni lo que se escribe o se ve en una terminal. En *Acerca de*, **Abrir carpeta de logs** la abre y **Copiar información de diagnóstico** copia la versión, el sistema, la carpeta de datos y las últimas 200 líneas del log. Las carpetas fijadas y sus nombres se guardan en el almacenamiento local de la ventana.

> La app antes se llamaba **Kairos**. Al arrancar, si existe la carpeta de datos `Kairos` y todavía no hay una `osat`, se mueve automáticamente. Las contraseñas guardadas en el keyring con el servicio `kairos` se pasan a `osat` la primera vez que se usan.

### Notas de diseño

- La salida de la terminal viaja al frontend como **base64** (evento `term:data:<id>`) para no romper secuencias UTF‑8 cortadas entre lecturas.
- El backend no envía salida hasta que el frontend llama a `TermAttach`, así no se pierde nada de lo que imprime la terminal.
- **Arranque limpio de SSH**: al abrir la terminal se manda una línea oculta que limpia la pantalla y emite un marcador OSC (`\x1b]777;osat-ready\x07`); la salida se retiene hasta que aparece, así no se ve el MOTD ni el eco de esa línea (`internal/remote/cleanstart.go`).
- **Puente**: el servidor MCP (`internal/bridge`) solo habla con `127.0.0.1`, exige token y revisa `Host` y `Origin`. Cada terminal compartida mantiene una pantalla virtual (`internal/vt`) para poder leerla y para saber si está esperando un comando antes de escribirle.
- Los errores que requieren interacción (pedir contraseña, confirmar huella) viajan como `OSAT:{json}` y los resuelve `frontend/src/lib/connect.ts`.
- El frontend usa `window.go.main.App` directamente desde `src/lib/api.ts` (tipado a mano); la carpeta `frontend/wailsjs` que genera Wails no es necesaria.
- **RDP**: el cliente es IronRDP (`@devolutions/iron-remote-desktop` 0.11.0 + `-rdp` 0.7.0, versiones fijas), que dibuja en un canvas de `RdpView.svelte` y se carga recién al abrir el primer escritorio (el WASM pesa unos 6 MB). Como el WebView no puede abrir TCP ni TLS, el backend hace de proxy con el protocolo **RDCleanPath** de Devolutions (`internal/rdpproxy`):
  - `RdpOpen` primero **sondea** el servidor (X.224 + TLS): tiene que aceptar TLS o NLA (la seguridad RDP clásica sin TLS no se soporta) y su certificado tiene que ser válido para el sistema o estar aceptado en `rdp_known_certs` (huella SHA-256, como `known_hosts`). Si no, viaja `RDPCERT_UNKNOWN` / `RDPCERT_MISMATCH` y el frontend pregunta.
  - Después abre un WebSocket en `127.0.0.1` con puerto al azar y una ruta secreta de un solo uso (también exige `Host` exacto). IronRDP manda ahí su X.224 envuelto en RDCleanPath; el proxy lo reenvía **al servidor de la conexión guardada** (ignora el destino del pedido, así no sirve de relé), hace el TLS exigiendo el mismo certificado del sondeo y devuelve la cadena. Desde ahí reenvía en crudo; la autenticación (NLA/CredSSP) la hace IronRDP, por eso la contraseña llega al WebView.
  - El tamaño del escritorio remoto es el del área de la pestaña en píxeles CSS (Display Control lo ajusta al redimensionar, con 400 ms de espera).
  - Mientras el foco está dentro del escritorio remoto, el teclado va al servidor (los atajos de osat no aplican), igual que en `mstsc`. El foco se toma con un clic, no al pasar el mouse.
  - Con el equipo se habla TLS 1.2 (`MaxVersion` en `dialServer`): Windows 11 ofrece TLS 1.3 y con él corta la sesión RDP con "internal error" antes de la autenticación. Al terminar cada sesión, el log dice qué lado cortó, con qué error, la versión de TLS y los bytes en cada sentido.
  - El portapapeles necesita un contexto seguro: anda en Windows (`http://wails.localhost`); en Linux/macOS el WebView carga `wails://` y IronRDP lo deshabilita con un aviso.
  - Test contra un servidor real: `go test -tags e2e -run E2E -v ./internal/rdpproxy/` (por defecto `127.0.0.1:3390`).
  - **RD Gateway** (`internal/rdgateway`): cliente de MS-TSGU por WebSocket. `*rdgateway.Tunnel` cumple `rdpproxy.Via`, así que `RdpOpen` lo usa en el lugar del salto SSH. Detalles que pide un gateway de Windows (sacados de FreeRDP): el upgrade a WebSocket va con el método `RDG_OUT_DATA`, `Content-Length: 0` y los encabezados `RDG-Connection-Id` / `RDG-Correlation-Id` escritos así; si algo no le gusta, corta la conexión sin responder. Por eso el pedido HTTP y el WebSocket (RFC 6455, lado cliente) están hechos a mano. La autenticación es NTLMv2 propio (`ntlm.go`) con *channel binding* del TLS (protección extendida), MIC y `HTTP/host`, como Windows; sin el channel binding el gateway corta. El doble factor se espera en la autorización del túnel. Un túnel abre un solo canal (otro sería otra aprobación), por eso es `rdpproxy.OneShot`: la conexión del sondeo queda abierta y la usa la sesión de IronRDP.
  - Test contra un gateway real: `OSAT_RDG_HOST`, `OSAT_RDG_DOMAIN`, `OSAT_RDG_USER`, `OSAT_RDG_PASSWORD` y `OSAT_RDG_TARGET` (`equipo:3389`), y `go test -mod=mod -tags e2e -run 'E2EGateway$' -v ./internal/rdgateway/` (manda una notificación de doble factor si el gateway la usa). `TestE2EGatewayTLS` prueba solo el primer pedido, sin credenciales.
- **VNC**: el cliente es noVNC (`@novnc/novnc` 1.7.0, versión fija), que dibuja en un canvas de `VncView.svelte` y se carga recién al abrir el primer escritorio VNC. Como el WebView no puede abrir TCP, el backend hace de proxy (`internal/vncproxy`), con el mismo patrón que `rdpproxy`:
  - `VncOpen` abre el TCP al servidor **en el momento** (directo, o por el `Dial` de la conexión SSH del salto), así un servidor apagado o un puerto cerrado dan un error claro enseguida. Después abre un WebSocket en `127.0.0.1` con puerto al azar y una ruta secreta de **un solo uso**; si el WebSocket no llega en 30 s, se descarta. El destino lo fija la conexión guardada, no el pedido. Los bytes pasan sin tocarse, en mensajes binarios (como `websockify`). Cerrar la pestaña o la app cierra el TCP.
  - Con host `localhost` y un salto, el destino es el `localhost` **del servidor del salto**, no el de tu PC.
  - La autenticación la hace noVNC, por eso la contraseña llega al WebView. Soporta ninguna o contraseña VNC. Si el servidor pide usuario u otra cosa, la pestaña lo dice. noVNC no avisa cuando el servidor usa un cifrado que no soporta (VeNCrypt con TLS, por ejemplo): corta antes de conectar, y osat lo explica como "el servidor cortó antes de terminar de conectar".
  - noVNC usa un `await` de nivel superior, que el destino por defecto de Vite no admite: `vite.config.ts` fija `build.target: 'es2022'` (WebView2, WebKitGTK 4.1 y el WKWebView de macOS actuales lo admiten).
  - El portapapeles necesita un contexto seguro (anda en Windows, no en Linux/macOS, como en RDP). El del servidor llega al de la PC al copiar allá; el de la PC va al servidor cuando el escritorio toma el foco (al hacer clic o al volver a osat).
  - Mientras el foco está dentro del escritorio VNC, el teclado va al servidor (los atajos de osat no aplican). Esto incluye `Ctrl+Tab`: para cambiar de pestaña, hacé clic en la barra.
  - Para probarlo a mano: `sudo apt install tigervnc-standalone-server` y `vncserver :1 -localhost yes -SecurityTypes VncAuth` (pide crear una contraseña). Conexión VNC con *Proxy Jump* a la SSH de ese servidor, host `localhost` y puerto 5901.
- **Navegador integrado** (`BrowserView.svelte`, `internal/web`): la página va en un `<iframe>` y lo que la alimenta es siempre un servidor HTTP propio en `127.0.0.1` con puerto al azar, que vive mientras viva la pestaña:
  - *Carpetas del servidor*: se leen por una sesión SFTP propia (sigue andando si cerrás el explorador), sin salirse de la raíz elegida y sin caché. La raíz es la carpeta del archivo, o la del repositorio si se abrió desde ahí.
  - *Puertos del servidor y de esta PC*: un proxy HTTP (también websockets, para la recarga en caliente) que abre las conexiones por el `Dial` de la conexión SSH, o directo si es localhost de esta PC. Saca `X-Frame-Options` y `frame-ancestors` (si no, muchos servicios no se dejarían mostrar en un iframe) y le muestra al destino su propio nombre como `Host`/`Origin`. El proxy local solo acepta loopback.
  - *Seguridad*: cada sitio solo contesta si el pedido va dirigido a `127.0.0.1:<su puerto>` (contra DNS rebinding) y no manda cabeceras CORS, así otra página no puede leerlo. El iframe va con `sandbox` sin permiso para navegar la ventana de osat. Los sitios de internet (que no son localhost) se cargan directo y pueden negarse a mostrarse ahí: para eso está el botón de abrir en el navegador del sistema.
  - *Límites*: desde un iframe de otro origen no se puede leer a qué página se llegó con clics ni usar atrás/adelante, así que la barra muestra lo que se pidió y "Recargar" vuelve a cargar esa dirección. Los enlaces absolutos como `/css/app.css` funcionan; los que suben por encima de la raíz (`../../x`), no.

- **Reenvío de puertos** (`PortsView.svelte`, `internal/portfwd`): es el `-L` de ssh. Un listener en `127.0.0.1` (y `::1`, para que `localhost` ande aunque el programa elija IPv6) abre por cada conexión un canal `direct-tcpip` en la conexión SSH, y copia en los dos sentidos respetando el cierre a medias. Nunca escucha fuera del loopback. El remoto (`-R`) pide `tcpip-forward` al servidor y atiende con el mismo `portfwd.Listener` (`portfwd.Serve`) las conexiones que llegan por canales `forwarded-tcpip`, abriendo cada una hacia el destino de esta PC. El dinámico (`-D`, `internal/portfwd/socks.go`) usa el mismo `Listener`: hace el saludo y el pedido SOCKS5 y abre el destino pedido con `direct-tcpip`.
  - Cada reenvío retiene una referencia de la conexión SSH compartida y la suelta al detenerse; si la conexión se corta, pasa a "error" y la pestaña reconecta al volver a iniciarlo. Si el servidor rechaza el destino ("no hay nada escuchando en…") se ve como aviso en la fila sin cortar el reenvío.
  - *Kubernetes*: se corre `kubectl port-forward -n NS RECURSO :PUERTO --address 127.0.0.1` en el servidor con una PTY (así, al cerrar, kubectl recibe el aviso y no queda corriendo), se lee el puerto que eligió y se reenvía ese. Nombres de namespace y recurso se validan estrictamente (`store.Forward.Validate`) antes de llegar a ningún comando.
  - Si `kubectl` termina (pod reiniciado o borrado), el reenvío queda en error con lo último que dijo; no se reintenta solo.

## Tests

```bash
go test ./internal/...
cd frontend && npm test   # tests del frontend (Vitest), corren una vez y terminan
```

Test de integración contra un sshd real (en Linux/macOS; en Windows `store` usaría tu `%APPDATA%` y keyring reales):

```bash
OSAT_E2E_HOST=127.0.0.1 OSAT_E2E_PORT=22 OSAT_E2E_USER=usuario OSAT_E2E_PASS=clave \
  go test -tags e2e ./internal/remote/ -v
```

## Ideas para próximas versiones

- Kubernetes directo desde esta PC con kubeconfig, sin pasar por el servidor

## Contribuir

Los reportes de errores, las ideas y los pull requests son bienvenidos: ver [CONTRIBUTING.md](../CONTRIBUTING.md). Las vulnerabilidades se reportan en privado, como dice [SECURITY.md](../SECURITY.md).

## Licencia

osat es software libre: se distribuye bajo la **GNU General Public License v3.0 o posterior** ([LICENSE](../LICENSE)).

Podés usarlo, estudiarlo, modificarlo y compartirlo, también con tus cambios, siempre que lo que distribuyas siga bajo la GPL y con su código fuente disponible. Así osat sigue siendo una herramienta abierta para sysadmins y ninguna versión derivada se puede cerrar.

Copyright (C) 2026 Bruno Mick

Las dependencias incluidas en el ejecutable tienen sus propias licencias (MIT, BSD, ISC, Apache-2.0 y OFL-1.1, todas compatibles con la GPL-3.0): ver [THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md).

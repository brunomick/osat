# Cambios

Todos los cambios importantes de osat se anotan acá.

El formato sigue [Keep a Changelog](https://keepachangelog.com/es-ES/1.1.0/).

## [1.0.0] - 2026-11-12

Primera versión estable de osat.

### Lo que reúne

- **Terminal SSH, archivos por SFTP y escritorio remoto (RDP y VNC)** en pestañas en mosaico, también a través de bastiones y de un RD Gateway.
- **Sitios web**: tus paneles de administración guardados al lado de tus servidores, con el login guardado.
- **Snippets, broadcast, reenvío de puertos y Kubernetes**, e importación desde PuTTY, WinSCP, mRemoteNG y OpenSSH.
- **Seguridad**: contraseñas en el almacén del sistema, bloqueo con contraseña o Windows Hello.
- **Se actualiza sola** y te muestra las novedades de cada versión, en español o en inglés.

### Cambiado

- Sitio web y README renovados.

## [0.5.3] - 2026-11-12

### Cambiado

- Versión de prueba del actualizador: no cambia nada en osat. Sirve para ver el aviso de versión nueva en pantalla completa y en tu idioma.

## [0.5.2] - 2026-11-12

### Cambiado

- El aviso de actualización y las **novedades** salen en el idioma en que usás osat (español o inglés).
- El aviso de actualización y su progreso ocupan toda la ventana de osat en lugar de una tarjeta flotante.

## [0.5.1] - 2026-11-12

### Cambiado

- Las **novedades** de una versión nueva se abren en una pestaña de osat, después de restaurar tus pestañas de la última vez, en lugar de en una ventana flotante.

## [0.5.0] - 2026-11-12

### Agregado

- **Actualizaciones automáticas**: al abrir, osat busca si hay una versión nueva. Si la hay, te muestra qué trae y, con *Actualizar ahora*, la descarga con una barra de progreso, se cierra y se vuelve a abrir ya actualizado. Las conexiones abiertas se cierran al reiniciar. En Windows, si osat está instalado en Archivos de programa, Windows pide permiso para instalar.
- En Ajustes → General, *Buscar actualizaciones al abrir osat* (activado de entrada) apaga la búsqueda automática, y el botón *Buscar actualizaciones* busca en el momento. osat solo consulta las versiones publicadas en GitHub: no manda ningún dato tuyo.
- **Novedades**: la primera vez que abrís una versión nueva, osat te muestra lo que cambió en una pestaña (como esta).

## [0.4.0] - 2026-11-11

### Agregado

- **Guardar como TXT…** en el menú de la pestaña de una terminal (y en el de la terminal, si el clic derecho abre un menú): guarda en un `.txt` todo lo que la terminal tiene en memoria, sin colores.
- **Android (beta)**: terminal SSH y SFTP en una interfaz de una pantalla a la vez. Por ahora se compila desde el código (ver `android/README.md`).
- **Credenciales por defecto**: en Ajustes → *Credenciales por defecto* se cargan un usuario, una contraseña (va al almacén de credenciales del sistema) y un dominio para RDP. Las usan las conexiones con *Usar credenciales por defecto* (casilla en el editor, o clic derecho en un grupo para marcar todas sus conexiones, con una llave en la barra lateral) y, si les falta, las que no tienen usuario o contraseña propios. Cambiar la contraseña ahí la cambia para todas. Al exportar un .osat, *Incluir las credenciales por defecto* (apagada de entrada) las agrega con su contraseña; al importar, se elige si reemplazan las de este osat.
- **Exportar snippets y sitios web en el .osat**: al exportar, *Incluir los snippets* e *Incluir los sitios web* (marcadas, con la cantidad) suman todos los snippets y sitios, y se puede exportar solo eso, sin conexiones. Al importar, el resumen dice cuántos trae el archivo y se importan aunque no se elija ninguna conexión (los que ya tenés no se duplican).
- **Paquete para Linux**: `build/linux/empaquetar.sh` arma `osat-linux.tar.gz` con el programa, el ícono y `instalar.sh`, que lo instala para el usuario (sin `sudo`) con su acceso en el menú de aplicaciones. Ver *Paquete para Linux* en `docs/desarrollo.md`.
- **Escritorio remoto VNC** en una pestaña, con noVNC: protocolo *VNC* en el editor de conexiones (host, puerto 5900 y contraseña, que va al almacén de credenciales del sistema). Se llega directo o *a través de* una conexión SSH (*Proxy Jump*); para pasar por el SSH del propio servidor, se elige su conexión SSH y se pone `localhost` como host. La imagen se escala a la pestaña, hay portapapeles de texto (solo en Windows, como en RDP), botón *Ctrl+Alt+Supr* y aviso con *Reconectar* si se corta. Solo soporta servidores sin autenticación o con contraseña VNC; con VeNCrypt/TLS, RealVNC o Apple la pestaña lo dice.
- Sección **Sitios** en la barra lateral, arriba de Workspaces: páginas de internet guardadas que se abren en una pestaña con el navegador nativo (WebView2 en Windows, WebKitGTK en Linux), con el login guardado entre reinicios. Se agregan con el **+** (nombre y dirección), se editan o quitan con clic derecho, se ordenan arrastrando y se filtran con la búsqueda. El globo de la sección, el **+** de la barra del sitio o `Ctrl+Shift+T` abren una pestaña de navegador vacía para escribir cualquier dirección. La sección se pliega y recuerda cómo quedó. En Ajustes → General, *Sitios web* la apaga (cierra los sitios abiertos) y la vuelve a prender sin reiniciar.
- Escritorio remoto a través de un **RD Gateway**: campo *RD Gateway* en la conexión RDP (por ejemplo `rds.empresa.com`, o `host:puerto`). Usa el mismo usuario, dominio y contraseña que el equipo, con NTLM (sin Kerberos). Si el gateway pide aprobar el ingreso en el celular, la pestaña lo avisa y espera hasta 90 segundos; hay una sola aprobación por conexión (dos la primera vez, si hay que aceptar el certificado del equipo). No se combina con Proxy Jump. El aviso de certificado dice cuándo es el del gateway, y si el gateway rechaza la contraseña se vuelve a pedir.
- Escritorio remoto: con el equipo se usa TLS 1.2 aunque ofrezca 1.3. Windows 11 cortaba la sesión con "internal error" antes de la autenticación cuando se negociaba TLS 1.3. El log registra qué lado corta una sesión, con qué error y cuántos datos pasaron.
- Importar de mRemoteNG trae el RD Gateway de cada conexión RDP (también el heredado de la carpeta). Si está en "detectar" o usa otras credenciales, se importa igual y avisa.
- **Bloqueo con contraseña**: en Ajustes → Seguridad se activa una contraseña que osat pide al abrirse y después de los minutos sin uso que se elijan (0 = nunca). Con osat bloqueado no se abre ninguna conexión (tampoco las de *Restaurar la sesión al abrir*) y las que ya estaban siguen andando por detrás. En Windows también se desbloquea con Windows Hello, si la PC lo tiene; su diálogo aparece recién cuando osat está al frente (si se bloqueó minimizado o detrás de otra aplicación, espera a que se vuelva a la ventana). Es una pantalla de bloqueo: no cifra las credenciales y, si te olvidás la contraseña, osat no abre.

### Cambiado

- El puente con Claude Code se configura en Ajustes → **Claude MCP**; ya no hay botón en la barra lateral.
- La barra del explorador SFTP es solo de íconos y, en un panel angosto, se acomoda en vez de cortarse.
- El panel de transferencias se colapsa solo a los 2 segundos cuando todo terminó bien.
- Las dependencias de Go ya no van copiadas en el repositorio (`vendor/`): se bajan al compilar.

## [0.3.0] - 2026-10-31

### Agregado

- Pestaña **Snippets** para editarlos con más lugar: todos los snippets a la izquierda (con búsqueda y carpetas) y el elegido a la derecha, para editarlo, duplicarlo o borrarlo. Se abre con clic derecho en el botón de snippets de la barra lateral o con el lápiz del panel de snippets de la terminal.
- Barra lateral: botón de snippets junto al de Claude, que abre los snippets en la terminal activa (lo mismo que `Ctrl+Shift+S`).
- Este equipo: botón Adelante, Alt+←/→ y botones laterales del mouse, y al subir de carpeta queda seleccionada aquella de la que se venía (como en el SFTP).
- Ajustes: al pie, "Abrir carpeta de logs" y "Copiar información de diagnóstico" (antes estaban en Acerca de).
- Terminal: ajustes nuevos en Ajustes → Terminal. Texto: interlineado, espaciado entre letras, grosor de la letra y de la negrita, margen interno, contraste mínimo, negrita en colores brillantes y cursor sin foco. Comportamiento: confirmar antes de pegar varias líneas, líneas por paso de la rueda, volver al final al escribir o con salida nueva, separadores de palabra del doble clic y abrir links con clic o Ctrl+clic. Conexión: keepalive, tipo de terminal (TERM) y guardar lo que muestra cada terminal en un archivo. Los valores por defecto son los de antes.
- Terminal: al copiar al seleccionar aparece el aviso "Copiado".
- SFTP: botón Adelante junto a Atrás; los dos responden a Alt+←/→ y a los botones laterales del mouse. Al subir de carpeta queda seleccionada aquella de la que se venía.
- Terminal: lo que se selecciona con el mouse se copia solo al portapapeles, como en PuTTY. Viene encendido y se apaga en Ajustes → Terminal.
- Temas de color para toda la app y la terminal: Dracula, One Dark, Solarized oscuro y claro, Gruvbox oscuro, Nord, Tokyo Night, Catppuccin Mocha y Latte, Monokai, GitHub Dark y Light, Rosé Pine y Rosé Pine Dawn, Kanagawa y Everforest. Se eligen en Ajustes → Apariencia con tarjetas que muestran cada tema; con las flechas se pasa de uno a otro y se aplica al momento. La terminal en "Auto" usa la paleta del mismo tema. El logo mantiene siempre los colores de osat.
- Idioma: en Ajustes → General, **Idioma** con *Español*, *English* o *Igual que el sistema* (por defecto). Por ahora están traducidos al inglés la barra lateral (con Favoritos, carpetas fijadas y workspaces), las pestañas, la barra de título, el formulario de conexión, importar y exportar, Ajustes, "Acerca de", la terminal (con su reconexión, la búsqueda, los snippets y el broadcast), el reenvío de puertos, SFTP y el explorador de esta PC (lista de archivos, menús, transferencias, sincronización, búsqueda y sudo), el editor, el repositorio, el navegador integrado, el escritorio remoto, el puente con Claude Code, los diálogos generales el formato de las fechas y los mensajes de error de conexión, huellas, sudo y conflictos de archivos (se reconocen por su código, no por el texto) y los errores y avisos de las conexiones, archivos, reenvíos, búsqueda y sincronización que arma Go, los diálogos nativos de abrir y guardar, las páginas de error del navegador integrado y los errores de validación de conexiones, grupos, snippets, workspaces, importación y archivos `.osat`. Con eso, la interfaz queda traducida por completo; el resto se va traduciendo en las próximas versiones.
- Tema claro: en Ajustes → General, **Apariencia** con *Oscuro* (por defecto), *Claro* o *Igual que el sistema* (cambia solo si cambia el modo del sistema). Cambia toda la interfaz, incluido el resaltado del editor y la pantalla de carga, al instante y sin reiniciar. El color de fondo de la ventana al abrir también sigue el tema elegido.
- Temas de la terminal: en Ajustes → Terminal, **Colores** con osat oscuro (el de siempre), osat claro, Solarized oscuro y claro, Dracula, One Dark y Gruvbox oscuro, o *Igual que la interfaz* (por defecto). Se aplica al instante a todas las terminales abiertas, también al fondo que las rodea.
- Favoritos: *Agregar a favoritos* en el menú de una conexión y una sección **Favoritos** al principio de la lista, plegable, con acceso directo a esas conexiones. Se ordenan arrastrando (también desde un grupo para agregarlas) y la búsqueda las filtra. No viajan en los `.osat`.
- Carpetas fijadas del servidor: la chincheta de la pestaña SFTP o "Fijar carpeta" en el menú del explorador las deja bajo su servidor en la barra lateral, con su nombre opcional. Un clic abre el SFTP en esa carpeta. Se guardan en la conexión y viajan en los `.osat`.
- Log de osat en `logs/osat.log`, dentro de la carpeta de datos: arranque y cierre, conexiones y saltos (con el error exacto si falla), huellas de host, transferencias, reenvíos de puertos, escritorio remoto, puente con Claude, errores del frontend y cierres inesperados con su traza. Rota a los 5 MB (hasta 3 archivos viejos) y nunca guarda contraseñas, frases de claves ni tokens. También registra errores del almacén de credenciales que antes se perdían.
- Pantalla de ajustes (engranaje en la barra lateral o `Ctrl+,`; `Cmd+,` en macOS) con la fuente, el tamaño de letra, las líneas de historial y el cursor de la terminal. Los cambios se guardan al momento en `settings.json` y se aplican a las terminales abiertas sin reiniciar; el zoom de cada terminal sigue siendo solo de esa terminal. Incluye "Restaurar valores por defecto".
- Ajustes de la terminal: clic derecho con la opción "Abrir menú" (Copiar, Pegar, Seleccionar todo, Buscar y Limpiar pantalla) y campana visual (parpadea el borde) o con sonido.
- Ajustes de archivos: "Preguntar dónde descargar" (apagado, las descargas van directo a la carpeta de descargas, que se elige con "Elegir…") y qué hacer si el archivo ya existe en el destino (preguntar, reemplazar u omitir), para subidas, descargas y arrastres.
- Ajuste "Registro detallado" para sumar más detalle al log.
- En *Acerca de*: botones **Abrir carpeta de logs** y **Copiar información de diagnóstico** (versión, sistema, carpeta de datos y las últimas 200 líneas del log).
- Buscar en la terminal con `Ctrl+Shift+F`: resalta todas las coincidencias del historial, con contador, siguiente/anterior y opciones de mayúsculas, palabra completa y expresión regular.
- Certificados de usuario SSH: con autenticación por clave, si junto a la clave está el `-cert.pub` (por ejemplo `id_ed25519-cert.pub`; para una `.ppk`, `server.ppk-cert.pub` o `server-cert.pub`), osat se autentica con el certificado, igual que `ssh`. Un `-cert.pub` dañado o que no corresponde a la clave da un error que nombra el archivo.
- Comando inicial y variables de entorno en las conexiones SSH (*Opciones avanzadas*): el comando se escribe en el shell de cada terminal nueva, al duplicar y al reconectar, y las variables se mandan al abrirla. Si el servidor rechaza una variable (por `AcceptEnv`), la terminal abre igual y lo avisa en gris.
- Reenvío del agente SSH (conexiones SSH, *Opciones avanzadas*, desactivado por defecto): el servidor puede usar las claves del agente de esta PC (Pageant u OpenSSH agent) sin que salgan de la PC. Solo se reenvía al servidor final, nunca a los saltos. Al importar de OpenSSH, `ForwardAgent yes` lo activa. Si no hay agente en esta PC, la terminal abre igual y lo avisa en gris.
- Reenvío de puertos remoto (el `-R` de ssh): en el diálogo de reenvíos, el tipo *Remoto* abre un puerto del servidor (por defecto solo en su `localhost`) que llega a un puerto de esta PC. Se ve en la pestaña *Puertos* con su estado y tráfico. Si el servidor no permite abrir el puerto, el reenvío queda en error con las causas posibles.
- Reenvío de puertos dinámico (el `-D` de ssh): en el diálogo de reenvíos, el tipo *Dinámico (SOCKS)* abre un proxy SOCKS5 en `localhost` (solo el puerto de tu PC) que manda cada conexión por el servidor hacia el destino que pida el programa; los nombres los resuelve el servidor. Se ve en la pestaña *Puertos* como `SOCKS localhost:PUERTO`, con un botón para copiar `socks5h://localhost:PUERTO` y una explicación de uso. Soporta solo `CONNECT` (sin SOCKS4, `BIND` ni UDP).
- Sincronizar una carpeta del servidor con una de esta PC desde el explorador SFTP (*Sincronizar…*), como en WinSCP: subir, bajar o ambas; modo sincronizar o espejo; comparar por fecha y tamaño, solo tamaño o contenido (`sha256sum`); borrar en el destino (opcional) y exclusiones. Muestra una vista previa con casillas antes de copiar nada, corre como una transferencia del panel y deja en cada copia la fecha del original.
- Buscar archivos en el servidor desde el explorador SFTP (*Buscar…* o `Ctrl+F`): por nombre con comodines y, opcionalmente, por el texto que contienen, dentro de la carpeta actual y sus subcarpetas. Los resultados aparecen a medida que llegan (hasta 1.000), con *Detener*; sin `find` en el servidor se recorren las carpetas por SFTP.
- Reintentar transferencias falladas desde el panel. Si una subida o descarga de un archivo de más de 1 MB se corta por la conexión, se conserva lo ya transferido y *Reintentar* sigue desde ahí cuando es seguro (si no, empieza de cero y lo avisa en la fila).
- Reconexión automática de las terminales SSH y los exploradores SFTP cuando se cae la conexión: reintenta solo cada 1, 2, 4, 8, 16 y después 30 segundos (`Enter`: reintentar ya, `Esc`: cancelar; en el explorador, con botones), y enseguida si vuelve la red. La terminal conserva lo que había en pantalla y marca "── reconectado ──"; el explorador vuelve a la carpeta en la que estaba. Si hace falta una contraseña, un código o confirmar una huella, no reintenta y queda el "Presioná Enter" de siempre.
- Snippets (comandos guardados): `Ctrl+Shift+S` o *Snippets…* en el menú de la pestaña abre un panel en la terminal con buscador y carpetas, y cada snippet se pega (sin Enter) o se ejecuta (con Enter); se crean, editan, duplican y borran desde el panel. El comando puede tener varias líneas y variables `{{nombre}}` que osat pide al usarlo. Son globales (en todas las terminales, también las de esta PC) o de un grupo de conexiones (en las de ese grupo y sus subgrupos, y siguen al grupo cuando lo renombrás o movés; se borran con él). Se guardan en `snippets.json` y el archivo `.osat` los lleva (los globales siempre, y los de un grupo si exportás una conexión de ese grupo o de un subgrupo); al importar no se duplican y el diálogo de importación avisa cuántos trae.
- Broadcast a varias terminales: `Ctrl+Shift+M` o *Broadcast…* en el menú de la pestaña abre un diálogo para elegir las terminales abiertas y conectadas (vienen marcadas las que se ven en el mosaico); con dos o más, lo que se escribe o se pega en una va a todas. Las que reciben tienen un borde de color fuerte y una barra arriba dice "Escribiendo en N terminales" con *Detener* (`Ctrl+Shift+M` también lo detiene). Una terminal que se cierra o se cae sale sola, y con menos de dos se detiene. El panel de snippets suma *Ejecutar en las N terminales* mientras el broadcast está activo.
- Espacios de trabajo: con *Restaurar la sesión al abrir osat* (Ajustes → General, activado por defecto) vuelven al abrir osat las pestañas, los paneles del mosaico y sus proporciones de la última vez. La sección **Workspaces** de la barra lateral guarda el espacio actual con un nombre; un clic lo abre (reemplaza las pestañas abiertas, con confirmación) y con clic derecho se renombra, actualiza o borra. Cada pestaña restaurada se conecta recién cuando se muestra, y las de un mismo servidor comparten la conexión (una sola contraseña). No vuelven las pestañas de conexiones borradas ni los editores con cambios sin guardar, y se avisa cuáles. Se guardan en `workspaces.json`, sin contraseñas ni el contenido de las terminales; si el archivo está dañado se aparta como `workspaces.json.bak` y osat arranca igual.
- Editar archivos del servidor como root con sudo: osat lo ofrece cuando abrir o guardar falla por permiso denegado, y está *Abrir como root (sudo)* en el menú de un archivo. La pestaña lleva la marca **root** y la contraseña de sudo se recuerda solo en memoria mientras la conexión esté abierta.

### Cambiado

- Terminal: al salir de la shell (`exit`, `logout`, Ctrl+D) se cierra la pestaña. Si se corta la conexión, la pestaña queda y se reconecta como siempre.
- Barra lateral: un divisor debajo de "Este equipo" y otro debajo de Favoritos, como el de Workspaces.
- Ajustes: las categorías (Apariencia, Terminal, Archivos, General) se despliegan y se pliegan, se recuerda cuáles quedaron abiertas y la pestaña ocupa todo el ancho. Los colores de la terminal se eligen con las mismas tarjetas que la apariencia, con una vista previa de la terminal al costado.
- La barra lateral recuerda su ancho al volver a abrir osat.
- Las barras de scroll son finas (3px) y redondeadas, sin riel ni flechas.
- Ajustes, importar conexiones, nueva o editar conexión y Claude Code se abren como pestañas, igual que las de terminal o SFTP, en lugar de diálogos flotantes.
- El "Acerca de" ya no tiene los botones "Abrir carpeta de logs" y "Copiar información de diagnóstico".
- Los grupos de la barra lateral arrancan plegados: osat recuerda los que abriste o plegaste, y un grupo nuevo (o todos, la primera vez con esta versión) aparece plegado. Al buscar se abren para mostrar las coincidencias.
- Los títulos de las pestañas "Este equipo", "Puertos · …" y "Navegador" se arman al mostrarlos; los espacios de trabajo guardados antes siguen abriéndose igual. La pestaña de puertos y el explorador SFTP reconocen la conexión cortada por un código y ya no por el texto del error.
- Teclado: la barra lateral (Favoritos y grupos, "Este equipo", Workspaces) se recorre con las flechas (derecha e izquierda abren y pliegan grupos) con una sola parada de Tab por sección; los botones que aparecen al pasar el mouse salen del orden de Tab y sus acciones se alcanzan con la tecla Menú o `Shift+F10` sobre la fila o la pestaña; las pestañas se recorren con flechas, Inicio y Fin; los menús contextuales se recorren con flechas, Inicio, Fin, Enter y Esc, y devuelven el foco; los diálogos mantienen el foco adentro y lo devuelven al cerrarse.
- Accesibilidad: los botones de solo ícono tienen nombre para los lectores de pantalla, los diálogos se anuncian como diálogos modales con su título, y los divisores (barra lateral, paneles, explorador, repositorio) se enfocan con Tab y se mueven con las flechas. Las filas del explorador de archivos y las pestañas se pueden enfocar con Tab (Enter abre o activa; Espacio selecciona).
- Contraste de los textos (WCAG AA) en los dos temas: el gris más tenue (`--text-3`) es un poco más claro en el oscuro y más oscuro en el claro, el aviso ámbar del tema claro es más oscuro, y el texto de los ítems peligrosos del menú pasa a tinta sobre el rojo del tema oscuro.

- La terminal local por defecto es ahora un ajuste fijo: abrir otra terminal ya no cambia la que usa "Abrir terminal aquí". La primera vez se toma la que venías usando.
- En el diálogo de reenvíos, el tipo *Puerto del servidor* ahora se llama *Local*.

### Corregido

- Barra lateral: el encabezado de Favoritos no plegaba ni desplegaba la sección.

## [0.2.0] - 2026-10-30

### Agregado

- Terminal: lo que se selecciona con el mouse se copia solo al portapapeles, como en PuTTY. Viene encendido y se apaga en Ajustes → Terminal.
- Temas de color para toda la app (Ajustes → General → Apariencia): Dracula, Solarized oscuro y claro, Gruvbox oscuro y One Dark. La terminal en "Auto" usa la paleta del mismo tema.
- Jump host / ProxyJump: campo *Proxy Jump* en las conexiones SSH y RDP que apunta a otra conexión guardada. Terminal, SFTP, reenvío de puertos y escritorio remoto pasan por el salto, se pueden encadenar y cada uno verifica su propia huella.
- Importar `Host` de OpenSSH (`~/.ssh/config` o un archivo `config`) desde *Importar configuración*, con `Include`, comodines y `Host *`. Un `ProxyJump` se enlaza con la conexión del bastión.

## [0.1.0]

Primera versión.

### Agregado

- Terminal: lo que se selecciona con el mouse se copia solo al portapapeles, como en PuTTY. Viene encendido y se apaga en Ajustes → Terminal.
- Temas de color para toda la app (Ajustes → General → Apariencia): Dracula, Solarized oscuro y claro, Gruvbox oscuro y One Dark. La terminal en "Auto" usa la paleta del mismo tema.
- Gestor de conexiones con grupos anidados, búsqueda y colores, e importación de sesiones de PuTTY, WinSCP y mRemoteNG.
- Credenciales en el keyring del sistema, autenticación con contraseña, clave (OpenSSH y `.ppk`) o agente SSH, y verificación de host keys con `known_hosts` propio.
- Terminal SSH en pestañas (xterm.js), terminales de esta PC y duplicar pestaña.
- Explorador SFTP con subida y descarga de archivos y carpetas, arrastrar y soltar, editor rápido, comprimir y extraer en el servidor, vista previa de imágenes y apertura de carpetas como repositorio.
- Panel "Este equipo" con los archivos locales, carpetas fijadas y papelera.
- Reenvío de puertos locales (con atajos y Kubernetes por `kubectl` del servidor) y navegador integrado.
- Paneles en mosaico y escritorio remoto (RDP) dentro de una pestaña.
- Puente con Claude Code: servidor MCP local para compartir terminales.

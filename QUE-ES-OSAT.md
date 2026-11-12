# ¿Qué es osat?

**osat** es una aplicación de escritorio que junta en una sola ventana todo lo que hace falta para trabajar con servidores remotos: **terminal SSH, archivos por SFTP y escritorio remoto (RDP)**. Funciona en Windows, Linux y macOS.

## El problema que soluciona

Administrar servidores suele obligar a usar varios programas a la vez:

- **PuTTY** para la terminal,
- **WinSCP** para los archivos,
- **mRemoteNG** (o el Escritorio remoto de Windows) para los servidores Windows,

cada uno con su propia lista de conexiones, sus propias contraseñas y su propia ventana. Las conexiones se duplican, se desordenan, y hay que estar saltando entre programas.

osat reemplaza a los tres con **una sola lista de conexiones** y **pestañas** para todo.

## Qué hace

- **Conexiones ordenadas**: grupos y subgrupos, búsqueda, colores, y un orden que elegís vos arrastrando.
- **Importa lo que ya tenés**: trae las sesiones de PuTTY, WinSCP, mRemoteNG y OpenSSH (`~/.ssh/config`) con sus carpetas como grupos.
- **Terminal SSH** en pestañas, con contraseña, claves (OpenSSH o `.ppk`) o agente.
- **Saltar a través de un bastión** (ProxyJump): llegar a servidores internos (SSH o RDP) pasando por un bastión SSH, sin dejar claves en el bastión.
- **Explorador SFTP**: subir y bajar archivos y carpetas (también arrastrando desde el Explorador), editar archivos de texto, comprimir y extraer en el servidor.
- **Escritorio remoto (RDP)** dentro de una pestaña, sin depender del cliente RDP del sistema, con portapapeles y resolución que se adapta a la pestaña.
- **Este equipo**: los archivos y las terminales de tu PC (PowerShell, WSL, bash…) al lado de los del servidor.
- **Reenvío de puertos**: usar desde tu PC (DBeaver, Postman, el navegador…) servicios del servidor como Redis o PostgreSQL, incluso de Kubernetes.
- **Paneles en mosaico**: dividir la ventana para ver varias pestañas a la vez.
- **Puente con Claude Code**: dejar que Claude Code vea y use las terminales que compartas.

## Seguro por defecto

- Las contraseñas van al almacén de credenciales del sistema (Administrador de credenciales de Windows, Llavero de macOS, Secret Service en Linux), **nunca** al archivo de conexiones.
- Verifica la identidad de cada servidor (huella SSH y certificado RDP) y avisa si cambia.
- Se puede exportar todo a un archivo `.osat` para respaldar o pasar las conexiones a otra PC.

---

Más detalles técnicos, atajos y cómo compilarlo: ver [README.md](README.md).

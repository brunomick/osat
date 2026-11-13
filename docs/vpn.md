# VPN WireGuard integrada

Una sola VPN WireGuard que usa solo osat: con la VPN activa, las conexiones de
osat salen por el túnel y el resto del equipo no cambia.

## Cómo funciona

- `internal/vpn`: `wireguard-go` + `tun/netstack` (gVisor), todo dentro del
  proceso. No instala drivers ni pide permisos de administrador.
- `vpn.Dial` / `vpn.DialTimeout` reemplazan al `net.Dial` directo en SSH/SFTP
  (`internal/remote/dial.go`), RDP, VNC y RD Gateway. Con salto por bastión, el
  bastión se alcanza por la VPN y el resto va por SSH.
- **Falla cerrado**: con la VPN activada y el túnel caído, `Dial` devuelve
  `vpn.ErrDown`; nunca sale directo.
- DNS: los nombres se resuelven con el `DNS` del `.conf`, dentro del túnel. Si
  el `.conf` no trae DNS, con el del sistema. Lo único que sale por fuera es la
  búsqueda del `Endpoint`.
- Si el `.conf` no indica `PersistentKeepalive`, se usa 25 s: el handshake sale
  al activar (así se sabe si el servidor contesta) y el NAT queda abierto.

## Qué no pasa por la VPN

Terminales locales, reenvíos hacia esta PC, el navegador integrado (WebView2) y
el actualizador.

## Guardado

- `vpn.json` en la carpeta de datos: el `.conf` sin `PrivateKey` y si está
  activada (se vuelve a activar al abrir osat).
- La clave privada, en el llavero del sistema (`osat` / `vpn-private-key`).

## Interfaz

- Botón (escudo) en la cabecera del sidebar, entre snippets y ajustes: activa o
  desactiva; sin VPN configurada abre Ajustes → VPN. Color según el estado:
  verde activa, amarillo conectando, rojo si no contesta o no levantó.
- Ajustes → VPN: pegar o importar el `.conf`, guardar, borrar y activar.

## Pendiente

- Navegador integrado por la VPN (SOCKS local + `--proxy-server` de WebView2).
- Botón en la vista de Android.
- Varias VPN o una por conexión.

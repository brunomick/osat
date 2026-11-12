package remote

import (
	"context"
	"io/fs"
	"net"
	"path"

	"github.com/pkg/sftp"

	"osat/internal/i18n"
	"osat/internal/web"
)

// Navegador integrado: sitios locales que muestran carpetas del servidor o
// puertos del servidor (ver internal/web). Cada sitio pertenece a una pestaña
// (owner) y se cierra con ella.

// sftpSource sirve una carpeta remota.
type sftpSource struct {
	cl   *sftp.Client
	root string
}

func (s sftpSource) abs(name string) string { return path.Join(s.root, name) }

func (s sftpSource) Stat(name string) (fs.FileInfo, error) { return s.cl.Stat(s.abs(name)) }

func (s sftpSource) Open(name string) (web.File, error) {
	f, err := s.cl.Open(s.abs(name))
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (s sftpSource) ReadDir(name string) ([]fs.FileInfo, error) { return s.cl.ReadDir(s.abs(name)) }

// ServeFolder sirve la carpeta root del servidor de la sesión. Usa una sesión
// SFTP propia, así sigue andando si se cierra la pestaña desde la que se pidió.
func (m *Manager) ServeFolder(owner, sessionID, root string) (*web.Site, error) {
	info, err := m.OpenSFTPFrom(sessionID)
	if err != nil {
		return nil, err
	}
	cl, err := m.sftpOf(info.ID)
	if err != nil {
		m.Close(info.ID)
		return nil, err
	}
	root = path.Clean(root)
	if fi, err := cl.Stat(root); err != nil || !fi.IsDir() {
		m.Close(info.ID)
		if err == nil {
			err = i18n.Err("remote.itIsNotAFolder")
		}
		return nil, err
	}
	site, err := m.web.Add(owner, web.Files(sftpSource{cl: cl, root: root}), func() { m.Close(info.ID) })
	if err != nil {
		m.Close(info.ID)
		return nil, err
	}
	return site, nil
}

// Tunnel muestra un puerto del servidor (visto desde el propio servidor, p. ej.
// localhost:3000) por la conexión SSH de la sesión, sin abrir nada en la red.
func (m *Manager) Tunnel(owner, sessionID string, t web.Target) (*web.Site, error) {
	if err := t.Valid(); err != nil {
		return nil, err
	}
	src, err := m.get(sessionID)
	if err != nil {
		return nil, err
	}
	if src.shared == nil {
		return nil, errNotRemote
	}
	if err := src.shared.acquire(); err != nil {
		return nil, err
	}
	client := src.shared.client
	dial := func(_ context.Context, network, addr string) (net.Conn, error) { return client.Dial(network, addr) }
	site, err := m.web.Add(owner, web.Proxy(t, dial), src.shared.release)
	if err != nil {
		src.shared.release()
		return nil, err
	}
	return site, nil
}

// LocalProxy muestra un servicio de esta PC (localhost:5173...). Solo loopback:
// otros sitios no pasan por acá (se abren en el navegador del sistema).
func (m *Manager) LocalProxy(owner string, t web.Target) (*web.Site, error) {
	if err := t.Valid(); err != nil {
		return nil, err
	}
	if !web.IsLoopback(t.Host) {
		return nil, i18n.Err("remote.theBuiltInBrowserOnly")
	}
	return m.web.Add(owner, web.Proxy(t, (&net.Dialer{}).DialContext), nil)
}

// CloseWeb cierra los sitios que abrió una pestaña.
func (m *Manager) CloseWeb(owner string) { m.web.CloseOwner(owner) }

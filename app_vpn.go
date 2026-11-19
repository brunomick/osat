package main

import (
	"log/slog"
	"strings"

	"osat/internal/i18n"
	"osat/internal/store"
	"osat/internal/vpn"
)

// VpnInfo es lo que ve el frontend de la VPN: el .conf sin la clave privada,
// si está activada y cómo está el túnel.
type VpnInfo struct {
	Configured bool       `json:"configured"`
	Conf       string     `json:"conf"`
	Enabled    bool       `json:"enabled"`
	Status     vpn.Status `json:"status"`
}

func (a *App) vpnInfo() VpnInfo {
	v := a.store.VPN()
	s := vpn.Current()
	if v.Enabled && s.State == vpn.StateOff {
		s.State = vpn.StateConnecting // al abrir osat, antes de que vpnAutoStart la levante
	}
	return VpnInfo{Configured: v.Conf != "", Conf: v.Conf, Enabled: v.Enabled, Status: s}
}

// vpnConfig arma la configuración guardada (con la clave del keyring).
func (a *App) vpnConfig() (vpn.Config, error) {
	v := a.store.VPN()
	if v.Conf == "" {
		return vpn.Config{}, i18n.Err("vpn.notConfigured")
	}
	return vpn.Parse(vpn.WithPrivateKey(v.Conf, a.store.VPNKey()))
}

// vpnAutoStart vuelve a activar la VPN al abrir osat si quedó activada.
func (a *App) vpnAutoStart() {
	if !a.store.VPN().Enabled {
		return
	}
	cfg, err := a.vpnConfig()
	if err != nil {
		slog.Warn("no se pudo leer la VPN guardada", "err", err)
		vpn.Start(vpn.Config{}) // queda activada y en error: nada sale por fuera de la VPN
		return
	}
	vpn.Start(cfg)
}

// VpnGet devuelve la VPN guardada y su estado.
func (a *App) VpnGet() VpnInfo { return a.vpnInfo() }

// VpnStatus devuelve solo el estado del túnel (para refrescar el botón).
func (a *App) VpnStatus() vpn.Status { return vpn.Current() }

// VpnSave guarda el .conf. Si no trae PrivateKey (se editó el texto que muestra
// osat, que no la incluye) se conserva la clave guardada. Si la VPN estaba
// activada, se reinicia con la configuración nueva.
func (a *App) VpnSave(conf string) (VpnInfo, error) {
	conf = strings.TrimSpace(strings.ReplaceAll(conf, "\r\n", "\n"))
	key := vpn.PrivateKeyText(conf)
	full := conf
	if key == "" {
		full = vpn.WithPrivateKey(conf, a.store.VPNKey())
	}
	cfg, err := vpn.Parse(full)
	if err != nil {
		return a.vpnInfo(), err
	}
	enabled := a.store.VPN().Enabled
	if err := a.store.SaveVPN(store.VPN{Conf: vpn.StripPrivateKey(conf), Enabled: enabled}, key); err != nil {
		return a.vpnInfo(), i18n.Err("vpn.saveFailed", err)
	}
	slog.Info("VPN guardada", "server", cfg.Peers[0].Endpoint)
	if enabled {
		vpn.Start(cfg)
	}
	return a.vpnInfo(), nil
}

// VpnDelete borra la VPN guardada (y la desactiva).
func (a *App) VpnDelete() (VpnInfo, error) {
	vpn.Stop()
	if err := a.store.DeleteVPN(); err != nil {
		return a.vpnInfo(), err
	}
	slog.Info("VPN borrada")
	return a.vpnInfo(), nil
}

// VpnSetEnabled activa o desactiva la VPN y lo recuerda para la próxima vez.
// Al activarla, el error (si el túnel no se pudo levantar) vuelve al frontend,
// pero la VPN queda activada: las conexiones fallan en vez de salir directo.
func (a *App) VpnSetEnabled(on bool) (VpnInfo, error) {
	v := a.store.VPN()
	if on {
		cfg, err := a.vpnConfig()
		if err != nil {
			return a.vpnInfo(), err
		}
		v.Enabled = true
		if err := a.store.SaveVPN(v, ""); err != nil {
			return a.vpnInfo(), i18n.Err("vpn.saveFailed", err)
		}
		err = vpn.Start(cfg)
		return a.vpnInfo(), err
	}
	vpn.Stop()
	v.Enabled = false
	if v.Conf != "" {
		if err := a.store.SaveVPN(v, ""); err != nil {
			return a.vpnInfo(), i18n.Err("vpn.saveFailed", err)
		}
	}
	return a.vpnInfo(), nil
}

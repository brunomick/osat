package i18n

// Textos de internal/vpn (VPN WireGuard integrada).
func init() {
	for k, v := range map[string]entry{
		"vpn.badLine":        {es: "línea %d: no es una línea válida de un archivo de WireGuard", en: "line %d: not a valid line of a WireGuard file"},
		"vpn.badValue":       {es: "línea %d: el valor de %s no es válido", en: "line %d: the value of %s is not valid"},
		"vpn.noPrivateKey":   {es: "falta PrivateKey en [Interface]", en: "PrivateKey is missing in [Interface]"},
		"vpn.noAddress":      {es: "falta Address en [Interface]", en: "Address is missing in [Interface]"},
		"vpn.noPeer":         {es: "falta la sección [Peer] con el servidor", en: "the [Peer] section with the server is missing"},
		"vpn.incompletePeer": {es: "a un [Peer] le falta PublicKey o Endpoint", en: "a [Peer] is missing PublicKey or Endpoint"},
		"vpn.badEndpoint":    {es: "%q no es un Endpoint válido (servidor:puerto)", en: "%q is not a valid Endpoint (server:port)"},
		"vpn.endpointLookup": {es: "no se pudo encontrar el servidor de la VPN %s: %v", en: "couldn't find the VPN server %s: %v"},
		"vpn.startFailed":    {es: "no se pudo levantar la VPN: %v", en: "couldn't start the VPN: %v"},
		"vpn.down":           {es: "la VPN está activada pero no está conectada: la conexión no sale para no hacerlo por fuera de la VPN", en: "the VPN is on but not connected: the connection is blocked so it doesn't go outside the VPN"},
		"vpn.dialTimeout":    {es: "%s no contestó a través de la VPN", en: "%s didn't answer through the VPN"},
		"vpn.dialFailed":     {es: "no se pudo llegar a %s a través de la VPN: %v", en: "couldn't reach %s through the VPN: %v"},
		"vpn.notConfigured":  {es: "todavía no hay una VPN configurada", en: "no VPN is configured yet"},
		"vpn.saveFailed":     {es: "no se pudo guardar la VPN: %v", en: "couldn't save the VPN: %v"},
	} {
		catalog[k] = v
	}
}

package store

import (
	"strings"

	"osat/internal/mongouri"
)

// validateMongo arregla una conexión MongoDB: la cadena queda sin la
// contraseña y de ella salen el host, el puerto y el usuario que se muestran.
// Lo que es de otros protocolos se borra. El salto (JumpID) vale: es el túnel SSH.
func validateMongo(c *Connection) error {
	u, err := mongouri.Parse(c.URI)
	if err != nil {
		return err
	}
	u.Password = ""
	c.URI = u.Redacted()
	c.Host, c.Port = u.First()
	c.User = u.User
	c.Domain, c.Gateway = "", ""
	c.AuthType, c.KeyPath = AuthPassword, ""
	c.UseDefaultCreds = false // las credenciales por defecto son de los servidores, no de las bases
	c.ForwardAgent, c.InitialCommand, c.Env = false, "", nil
	c.Name = strings.TrimSpace(c.Name)
	return nil
}

// mongoPassword devuelve la contraseña que trae la cadena de conexión ("" si
// no trae o no se puede leer: validate avisa del error).
func mongoPassword(uri string) string {
	u, err := mongouri.Parse(uri)
	if err != nil {
		return ""
	}
	return u.Password
}

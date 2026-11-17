package i18n

// Textos de las conexiones MongoDB: la cadena de conexión (mongouri), la
// importación desde MongoDB Compass y mongosh (remote/mongo.go).
func init() {
	for k, v := range map[string]entry{
		"mongo.badScheme":           {es: "la cadena de conexión tiene que empezar con mongodb:// o mongodb+srv://", en: "the connection string must start with mongodb:// or mongodb+srv://"},
		"mongo.badUserEncoding":     {es: "el usuario de la cadena de conexión está mal codificado", en: "the user in the connection string is badly encoded"},
		"mongo.badPasswordEncoding": {es: "la contraseña de la cadena de conexión está mal codificada", en: "the password in the connection string is badly encoded"},
		"mongo.noHost":              {es: "la cadena de conexión no tiene host", en: "the connection string has no host"},
		"mongo.srvOneHost":          {es: "una cadena mongodb+srv:// lleva un solo host", en: "a mongodb+srv:// string takes a single host"},
		"mongo.badHost":             {es: "la cadena de conexión tiene un host inválido: %q", en: "the connection string has an invalid host: %q"},
		"mongo.badOptions":          {es: "la base o las opciones de la cadena de conexión tienen espacios", en: "the database or options in the connection string contain spaces"},
		"mongo.notMongo":            {es: "la conexión no es de MongoDB", en: "the connection is not a MongoDB one"},
		"mongo.noMongosh":           {es: "no se encontró mongosh en esta PC: instalalo desde https://www.mongodb.com/try/download/shell (o agregalo al PATH) y volvé a abrir la conexión", en: "mongosh was not found on this PC: install it from https://www.mongodb.com/try/download/shell (or add it to the PATH) and open the connection again"},
		"mongo.ended":               {es: "mongosh se cerró apenas abrió (revisá el mensaje de arriba)", en: "mongosh closed right after starting (see the message above)"},

		// Importar de Compass.
		"mongo.compassBadFile":       {es: "el archivo no es una exportación de conexiones de MongoDB Compass", en: "the file is not a MongoDB Compass connections export"},
		"mongo.compassBadVersion":    {es: "el archivo de Compass es de una versión que osat no conoce (%v)", en: "the Compass file is from a version osat doesn't know (%v)"},
		"mongo.compassInvalidJSON":   {es: "el archivo de Compass no es JSON válido: %v", en: "the Compass file is not valid JSON: %v"},
		"mongo.compassBadSecrets":    {es: "el archivo de Compass tiene datos cifrados dañados", en: "the Compass file has damaged encrypted data"},
		"mongo.compassNoURI":         {es: "%s: no tiene cadena de conexión", en: "%s: it has no connection string"},
		"mongo.compassBadURI":        {es: "%s: %v", en: "%s: %v"},
		"mongo.compassAuthMechanism": {es: "%s: usa autenticación %s; mongosh puede pedir más datos al conectar", en: "%s: it uses %s authentication; mongosh may ask for more details when connecting"},
		"mongo.compassOIDC":          {es: "%s: usa OIDC; osat no guarda ese inicio de sesión", en: "%s: it uses OIDC; osat doesn't keep that sign-in"},
		"mongo.compassFLE":           {es: "%s: usa cifrado a nivel de campo (CSFLE/Queryable Encryption); osat no importa esas claves", en: "%s: it uses field-level encryption (CSFLE/Queryable Encryption); osat doesn't import those keys"},
		"mongo.compassProxy":         {es: "%s: usa un proxy de la aplicación; osat conecta sin él", en: "%s: it uses an application proxy; osat connects without it"},
		"mongo.compassOtherSecret":   {es: "%s: trae una contraseña de %s que osat no importa", en: "%s: it has a %s password that osat doesn't import"},
		"mongo.compassSSHNoHost":     {es: "%s: el túnel SSH no tiene host (se importa sin túnel)", en: "%s: the SSH tunnel has no host (imported without the tunnel)"},
		"native.filterCompass":       {es: "Conexiones exportadas de MongoDB Compass (*.json)", en: "MongoDB Compass exported connections (*.json)"},
	} {
		catalog[k] = v
	}
}

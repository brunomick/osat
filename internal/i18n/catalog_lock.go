package i18n

// Textos de applock (bloqueo con contraseña).
func init() {
	for k, v := range map[string]entry{
		"lock.wrongPassword":    {es: "contraseña incorrecta", en: "wrong password"},
		"lock.tooShort":         {es: "la contraseña tiene que tener al menos %d caracteres", en: "the password must have at least %d characters"},
		"lock.storeUnavailable": {es: "no se pudo usar el almacén de credenciales del sistema: %v", en: "couldn't use the system credential store: %v"},
		"lock.notEnabled":       {es: "el bloqueo con contraseña no está activado", en: "password lock is not enabled"},
		"lock.helloMessage":     {es: "Desbloqueá osat", en: "Unlock osat"},
	} {
		catalog[k] = v
	}
}

import { parseError } from './api'

// Las credenciales de los sitios van cifradas en el almacén del sistema con una clave de esta PC
// (store/credvault.go). Si la clave se pierde, sus datos no se pueden leer y se cargan de nuevo editándolas.

/** Los datos de la credencial no se pueden descifrar en esta PC: hay que cargarlos de nuevo. */
export const isCredsLost = (e: unknown) => parseError(e)?.code === 'CREDS_LOST'
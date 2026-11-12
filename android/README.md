# osat para Android (MVP)

La misma app de escritorio, empaquetada para Android:

- **Backend**: el osat de Go compilado para Android (`main_android.go`), como `libosat.so`. La app
  lo ejecuta y él sirve el frontend y la API en `127.0.0.1` con un token por arranque.
- **Interfaz**: el frontend de Svelte de siempre en un WebView. `osat-android.js` arma
  `window.go` y `window.runtime` (lo que en escritorio pone Wails) sobre esa API.
- **Secretos**: en `secrets.bin` (carpeta privada de la app), cifrado con AES-GCM; la clave está
  envuelta con una clave del Android Keystore.

## Compilar

Hace falta Go, Node, el SDK de Android con el NDK 27.0.12077973 y un JDK 17–21 (Gradle 8.11 no
anda con Java 25: el JBR de Android Studio reciente es 25).

```bash
cd android
JAVA_HOME=/ruta/al/jdk-21 ./gradlew assembleDebug
```

`assembleDebug` compila el frontend (`npm run build`) y el backend para arm64 y x86_64; el APK
queda en `app/build/outputs/apk/debug/app-debug.apk`. Se instala con `adb install`.

## Qué anda y qué no

Anda: conexiones, terminal SSH (con barra de Ctrl/Alt/Esc/Tab/Pegar y flechas), SFTP, importar
archivos (el selector de Android copia lo elegido a la caché), ajustes y temas.

La interfaz en Android es de una pantalla a la vez (`frontend/src/lib/mobile.ts`): Inicio
(conexión rápida con `usuario@host:puerto`, recientes, favoritos y todos los servidores; mantener
apretado uno muestra sus opciones), Sesiones (las pestañas abiertas) y cada pestaña a pantalla
completa. Atrás de Android vuelve de una pestaña a Sesiones y de ahí a Inicio. Mientras haya
sesiones abiertas, `SessionsService` corre en primer plano para que Android no las corte en
segundo plano.

Diferente en Android: las descargas y exportaciones van siempre a la carpeta de descargas de la
app (`Android/data/ar.osat/files/Download`), porque Android no da rutas de carpetas.

Sin probar en un dispositivo: RDP, reenvío de puertos, terminal local y el puente con Claude Code.
No hay arrastrar archivos ni ventanas en mosaico pensadas para el teléfono.

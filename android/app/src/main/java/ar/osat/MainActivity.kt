package ar.osat

import android.Manifest
import android.app.Activity
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Intent
import android.content.pm.ApplicationInfo
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Environment
import android.provider.OpenableColumns
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import android.util.Log
import android.view.WindowInsets
import android.webkit.JavascriptInterface
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.FrameLayout
import java.io.File
import java.security.KeyStore
import java.security.SecureRandom
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec
import org.json.JSONArray
import kotlin.concurrent.thread

// osat en Android: arranca el backend de Go (libosat.so, ver main_android.go) y muestra la
// interfaz de siempre en un WebView. Lo que el frontend necesita de Android (portapapeles, abrir
// enlaces, elegir archivos) lo pide por window.OsatAndroid (ver osat-android.js).
class MainActivity : Activity() {
    private lateinit var web: WebView
    private var backend: Process? = null
    private var pickId = 0L
    private var askedNotifications = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (applicationInfo.flags and ApplicationInfo.FLAG_DEBUGGABLE != 0) {
            WebView.setWebContentsDebuggingEnabled(true)
        }
        web = WebView(this)
        web.settings.javaScriptEnabled = true
        web.settings.domStorageEnabled = true
        web.webViewClient = WebViewClient() // los enlaces se abren afuera por openUrl, no acá
        web.addJavascriptInterface(Bridge(), "OsatAndroid")
        // Desde Android 15 la app dibuja debajo de las barras del sistema y del teclado: el marco
        // se corre lo que ocupan, así nada queda tapado (y la terminal se achica con el teclado).
        val frame = FrameLayout(this)
        frame.addView(web)
        if (Build.VERSION.SDK_INT >= 30) {
            frame.setOnApplyWindowInsetsListener { v, insets ->
                val b = insets.getInsets(WindowInsets.Type.systemBars() or WindowInsets.Type.ime() or WindowInsets.Type.displayCutout())
                v.setPadding(b.left, b.top, b.right, b.bottom)
                WindowInsets.CONSUMED
            }
        }
        setContentView(frame)
        startBackend()
    }

    private fun startBackend() {
        val token = randomBase64(32).replace('+', '-').replace('/', '_').trimEnd('=')
        val pb = ProcessBuilder(File(applicationInfo.nativeLibraryDir, "libosat.so").path)
        pb.directory(filesDir)
        pb.environment().apply {
            put("HOME", filesDir.path)
            put("TMPDIR", cacheDir.path)
            put("SHELL", "/system/bin/sh")
            put("TZ", java.util.TimeZone.getDefault().id) // sin esto Go queda en UTC
            put("OSAT_TOKEN", token)
            put("OSAT_SECRET_KEY", secretKey())
            put("OSAT_DOWNLOADS", (getExternalFilesDir(Environment.DIRECTORY_DOWNLOADS) ?: filesDir).path)
        }
        val p = pb.start()
        backend = p
        thread(name = "osat-stderr") {
            p.errorStream.bufferedReader().forEachLine { Log.w(TAG, it) }
        }
        thread(name = "osat-stdout") {
            val out = p.inputStream.bufferedReader()
            val first = out.readLine()
            if (first == null || !first.startsWith("OSAT_LISTEN ")) {
                Log.e(TAG, "el backend no arrancó: $first")
                return@thread
            }
            val addr = first.removePrefix("OSAT_LISTEN ").trim()
            runOnUiThread { web.loadUrl("http://$addr/?t=$token") }
            out.forEachLine { Log.i(TAG, it) }
        }
    }

    override fun onDestroy() {
        // Cerrar stdin es la señal para que el backend cierre las sesiones y termine.
        backend?.let { p ->
            runCatching { p.outputStream.close() }
            thread { if (!runCatching { p.waitFor(3, java.util.concurrent.TimeUnit.SECONDS) }.getOrDefault(false)) p.destroy() }
        }
        SessionsService.update(this, 0)
        web.destroy()
        super.onDestroy()
    }

    // Atrás lo resuelve primero la interfaz (de una pestaña a Sesiones, de ahí a Inicio, ver App.svelte);
    // desde Inicio deja la app en segundo plano en lugar de cerrarla: cerrarla cortaría las sesiones.
    @Deprecated("Deprecated in Java")
    override fun onBackPressed() {
        web.evaluateJavascript("window.__osatBack ? window.__osatBack() : false") { handled ->
            if (handled != "true") moveTaskToBack(true)
        }
    }

    @Deprecated("Deprecated in Java")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode != REQ_PICK) return
        val id = pickId
        val uris = mutableListOf<Uri>()
        if (resultCode == RESULT_OK && data != null) {
            data.clipData?.let { c -> for (i in 0 until c.itemCount) uris.add(c.getItemAt(i).uri) }
            if (uris.isEmpty()) data.data?.let { uris.add(it) }
        }
        thread {
            // Los archivos de Android llegan como content://: se copian a una carpeta propia para
            // que el backend los lea como archivos comunes.
            val dir = File(cacheDir, "picked/${System.currentTimeMillis()}").apply { mkdirs() }
            val paths = JSONArray()
            for (uri in uris) {
                runCatching {
                    val f = File(dir, displayName(uri))
                    contentResolver.openInputStream(uri)!!.use { input -> f.outputStream().use { input.copyTo(it) } }
                    paths.put(f.path)
                }.onFailure { Log.w(TAG, "no se pudo copiar $uri", it) }
            }
            runOnUiThread { web.evaluateJavascript("window.__osatPicked($id, $paths)", null) }
        }
    }

    private fun displayName(uri: Uri): String {
        contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { c ->
            if (c.moveToFirst()) c.getString(0)?.let { return File(it).name }
        }
        return uri.lastPathSegment?.let { File(it).name } ?: "archivo"
    }

    // La clave de secrets.bin: 32 bytes al azar, guardados cifrados con una clave del Android
    // Keystore (que no sale del dispositivo).
    private fun secretKey(): String {
        val prefs = getSharedPreferences("osat", MODE_PRIVATE)
        val wrapKey = keystoreKey()
        prefs.getString("secretKey", null)?.let { stored ->
            val raw = Base64.decode(stored, Base64.NO_WRAP)
            val c = Cipher.getInstance("AES/GCM/NoPadding")
            c.init(Cipher.DECRYPT_MODE, wrapKey, GCMParameterSpec(128, raw, 0, 12))
            return Base64.encodeToString(c.doFinal(raw, 12, raw.size - 12), Base64.NO_WRAP)
        }
        val key = ByteArray(32).also { SecureRandom().nextBytes(it) }
        val c = Cipher.getInstance("AES/GCM/NoPadding")
        c.init(Cipher.ENCRYPT_MODE, wrapKey)
        prefs.edit().putString("secretKey", Base64.encodeToString(c.iv + c.doFinal(key), Base64.NO_WRAP)).apply()
        return Base64.encodeToString(key, Base64.NO_WRAP)
    }

    private fun keystoreKey(): SecretKey {
        val ks = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (ks.getKey(KEY_ALIAS, null) as? SecretKey)?.let { return it }
        val gen = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore")
        gen.init(
            KeyGenParameterSpec.Builder(KEY_ALIAS, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .build(),
        )
        return gen.generateKey()
    }

    private fun randomBase64(n: Int): String =
        Base64.encodeToString(ByteArray(n).also { SecureRandom().nextBytes(it) }, Base64.NO_WRAP)

    inner class Bridge {
        @JavascriptInterface
        fun clipboardGet(): String {
            val cm = getSystemService(ClipboardManager::class.java)
            return cm.primaryClip?.takeIf { it.itemCount > 0 }?.getItemAt(0)?.coerceToText(this@MainActivity)?.toString() ?: ""
        }

        @JavascriptInterface
        fun clipboardSet(text: String) {
            getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText("osat", text))
        }

        @JavascriptInterface
        fun openUrl(url: String) {
            val uri = Uri.parse(url)
            if (uri.scheme != "http" && uri.scheme != "https") return
            runOnUiThread { runCatching { startActivity(Intent(Intent.ACTION_VIEW, uri)) } }
        }

        // Cuántas sesiones hay abiertas: mientras haya alguna, el servicio en primer plano las mantiene
        // vivas. La primera vez se pide permiso para mostrar su notificación (Android 13+).
        @JavascriptInterface
        fun sessions(count: Int) {
            runOnUiThread {
                if (count > 0 && Build.VERSION.SDK_INT >= 33 && !askedNotifications &&
                    checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
                ) {
                    askedNotifications = true
                    requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), REQ_NOTIFY)
                }
                SessionsService.update(this@MainActivity, count)
            }
        }

        @JavascriptInterface
        fun pickFiles(id: Long, multiple: Boolean) {
            runOnUiThread {
                pickId = id
                val i = Intent(Intent.ACTION_OPEN_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE).setType("*/*")
                    .putExtra(Intent.EXTRA_ALLOW_MULTIPLE, multiple)
                @Suppress("DEPRECATION")
                startActivityForResult(i, REQ_PICK)
            }
        }
    }

    companion object {
        private const val TAG = "osat"
        private const val REQ_PICK = 1
        private const val REQ_NOTIFY = 2
        private const val KEY_ALIAS = "osat-secrets"
    }
}

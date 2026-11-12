package ar.osat

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder

// Mientras haya sesiones abiertas, osat corre como servicio en primer plano (con su notificación):
// así Android no corta el backend ni las conexiones cuando la app queda en segundo plano.
class SessionsService : Service() {
    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val count = intent?.getIntExtra(EXTRA_COUNT, 0) ?: 0
        val nm = getSystemService(NotificationManager::class.java)
        nm.createNotificationChannel(NotificationChannel(CHANNEL, getString(R.string.sessions_channel), NotificationManager.IMPORTANCE_LOW))
        val open = PendingIntent.getActivity(
            this, 0,
            Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP),
            PendingIntent.FLAG_IMMUTABLE,
        )
        val n = Notification.Builder(this, CHANNEL)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(getString(R.string.app_name))
            .setContentText(resources.getQuantityString(R.plurals.sessions_open, count, count))
            .setContentIntent(open)
            .setOngoing(true)
            .build()
        if (Build.VERSION.SDK_INT >= 34) {
            startForeground(ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
        } else {
            startForeground(ID, n)
        }
        return START_NOT_STICKY
    }

    companion object {
        private const val CHANNEL = "sessions"
        private const val ID = 1
        private const val EXTRA_COUNT = "count"

        /** Con count > 0 arranca (o actualiza) el servicio; con 0 lo detiene. */
        fun update(ctx: Context, count: Int) {
            val i = Intent(ctx, SessionsService::class.java)
            if (count > 0) {
                runCatching { ctx.startForegroundService(i.putExtra(EXTRA_COUNT, count)) }
            } else {
                ctx.stopService(i)
            }
        }
    }
}

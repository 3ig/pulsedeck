package app.pulsedeck.client

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.wifi.WifiManager
import android.os.Build
import android.os.IBinder
import android.os.PowerManager
import org.json.JSONArray
import java.net.InetSocketAddress
import java.net.Socket
import kotlin.concurrent.thread

/**
 * "Wake with computer": while the screen is off, keeps checking whether one of
 * the known Pulsedeck servers has come back, and turns the screen on when it
 * does. The dashboard itself lets the screen sleep while no computer is
 * connected (see MainActivity.setConnected).
 *
 * Waking is edge-triggered: only a server that was unreachable and became
 * reachable wakes the device, so switching the screen off by hand while the
 * computer is running stays off.
 */
class WakeService : Service() {
    @Volatile private var running = false
    private var cpuLock: PowerManager.WakeLock? = null
    private var wifiLock: WifiManager.WifiLock? = null

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        startForegroundCompat()
        if (!running) {
            running = true
            // Without these the CPU and Wi-Fi go to sleep with the screen and
            // the checks below would stop. Intended for a device on a charger.
            val pm = getSystemService(Context.POWER_SERVICE) as PowerManager
            cpuLock = pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "pulsedeck:watch").apply { acquire() }
            val wifi = applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager
            @Suppress("DEPRECATION")
            wifiLock = wifi.createWifiLock(WifiManager.WIFI_MODE_FULL_HIGH_PERF, "pulsedeck:watch").apply { acquire() }
            thread(name = "pulsedeck-watch", isDaemon = true) { watch(pm) }
        }
        return START_STICKY
    }

    private fun watch(pm: PowerManager) {
        var reachable = true // assume up, so starting the service never wakes the screen by itself
        while (running) {
            val now = servers(this).any { (host, port) -> canConnect(host, port) }
            if (now && !reachable && !pm.isInteractive) wakeScreen(pm)
            reachable = now
            Thread.sleep(if (pm.isInteractive) 15_000 else 4_000)
        }
    }

    private fun canConnect(host: String, port: Int): Boolean = try {
        Socket().use { it.connect(InetSocketAddress(host, port), 1500) }
        true
    } catch (e: Exception) {
        false
    }

    private fun wakeScreen(pm: PowerManager) {
        @Suppress("DEPRECATION")
        val lock = pm.newWakeLock(
            PowerManager.SCREEN_BRIGHT_WAKE_LOCK or PowerManager.ACQUIRE_CAUSES_WAKEUP,
            "pulsedeck:wake"
        )
        lock.acquire(10_000) // long enough for the dashboard to reconnect and take over
        // Bring the dashboard back in front in case something else was on top.
        runCatching {
            startActivity(
                Intent(this, MainActivity::class.java)
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_REORDER_TO_FRONT)
            )
        }
    }

    private fun startForegroundCompat() {
        val nm = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        nm.createNotificationChannel(
            NotificationChannel(CHANNEL, "Wake with computer", NotificationManager.IMPORTANCE_MIN)
        )
        val open = PendingIntent.getActivity(
            this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE
        )
        val notification = Notification.Builder(this, CHANNEL)
            .setSmallIcon(R.drawable.ic_launcher_foreground)
            .setContentTitle("Pulsedeck")
            .setContentText("Will wake the screen when your computer comes online")
            .setContentIntent(open)
            .setOngoing(true)
            .build()
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            startForeground(1, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_CONNECTED_DEVICE)
        } else {
            startForeground(1, notification)
        }
    }

    override fun onDestroy() {
        running = false
        cpuLock?.let { if (it.isHeld) it.release() }
        wifiLock?.let { if (it.isHeld) it.release() }
        super.onDestroy()
    }

    companion object {
        private const val CHANNEL = "wake"
        private const val PREFS = "wake"

        /** Remembers the setting and the server list, and starts or stops the watcher. */
        fun configure(context: Context, enabled: Boolean, serversJson: String) {
            context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit()
                .putBoolean("enabled", enabled).putString("servers", serversJson).apply()
            val intent = Intent(context, WakeService::class.java)
            if (enabled) context.startForegroundService(intent) else context.stopService(intent)
        }

        fun enabled(context: Context): Boolean =
            context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getBoolean("enabled", false)

        /** Parses ["host:port", …] as stored by configure(). */
        fun servers(context: Context): List<Pair<String, Int>> {
            val raw = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getString("servers", "[]")
            return runCatching {
                val arr = JSONArray(raw)
                (0 until arr.length()).mapNotNull { i ->
                    val s = arr.getString(i)
                    val port = s.substringAfterLast(':', "").toIntOrNull() ?: return@mapNotNull null
                    s.substringBeforeLast(':') to port
                }
            }.getOrDefault(emptyList())
        }
    }
}

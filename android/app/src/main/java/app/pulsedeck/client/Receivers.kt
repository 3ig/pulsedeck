package app.pulsedeck.client

import android.app.admin.DeviceAdminReceiver
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent

/**
 * Lets Pulsedeck switch the screen off when no computer is connected. Android
 * only allows that to an app the user has made a "device admin app"; the one
 * policy requested is force-lock (see res/xml/device_admin.xml).
 */
class ScreenAdmin : DeviceAdminReceiver()

/** After a reboot, resume watching for computers if "wake with computer" is on. */
class BootReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action == Intent.ACTION_BOOT_COMPLETED && WakeService.enabled(context)) {
            runCatching { context.startForegroundService(Intent(context, WakeService::class.java)) }
        }
    }
}

package app.pulsedeck.client

import android.annotation.SuppressLint
import android.app.Activity
import android.app.admin.DevicePolicyManager
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.net.nsd.NsdManager
import android.net.nsd.NsdServiceInfo
import android.net.wifi.WifiManager
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.os.PowerManager
import android.provider.Settings
import android.view.MotionEvent
import android.view.View
import android.view.WindowInsets
import android.view.WindowInsetsController
import android.view.WindowManager
import android.webkit.JavascriptInterface
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebView
import android.webkit.WebViewClient
import org.json.JSONObject
import java.util.ArrayDeque

/**
 * Thin native shell around the shared web dashboard (repo: client/).
 *
 * The UI is served to the WebView from the APK's assets under a fixed
 * http origin, so its saved dashboards survive server address changes. The
 * shell adds what a browser cannot do: finding servers on the local network
 * (mDNS), staying awake, immersive fullscreen and file import/export.
 */
class MainActivity : Activity() {
    private lateinit var web: WebView
    private var nsd: NsdManager? = null
    private var discovery: NsdManager.DiscoveryListener? = null
    private var multicastLock: WifiManager.MulticastLock? = null
    private val resolveQueue = ArrayDeque<NsdServiceInfo>()
    private var resolving = false

    private var fileCallback: ValueCallback<Array<Uri>>? = null
    private var pendingSave: String? = null

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)

        web = WebView(this)
        web.setBackgroundColor(0xFF0A0C11.toInt())
        web.overScrollMode = View.OVER_SCROLL_NEVER
        web.settings.javaScriptEnabled = true
        web.settings.domStorageEnabled = true
        web.settings.allowFileAccess = false
        web.settings.allowContentAccess = false
        web.addJavascriptInterface(Bridge(), "PulsedeckNative")
        web.webViewClient = object : WebViewClient() {
            override fun shouldInterceptRequest(view: WebView, request: WebResourceRequest): WebResourceResponse? {
                val url = request.url
                return if (url.host == APP_HOST) serveAsset(url.path ?: "/") else null
            }

            // The page never navigates away from the bundled UI; anything else
            // (there are no external links today) opens in the browser.
            override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
                if (request.url.host == APP_HOST) return false
                runCatching { startActivity(Intent(Intent.ACTION_VIEW, request.url)) }
                return true
            }
        }
        web.webChromeClient = object : WebChromeClient() {
            override fun onShowFileChooser(view: WebView, callback: ValueCallback<Array<Uri>>, params: FileChooserParams): Boolean {
                fileCallback?.onReceiveValue(null)
                fileCallback = callback
                return try {
                    startActivityForResult(params.createIntent(), REQ_PICK)
                    true
                } catch (e: Exception) {
                    fileCallback = null
                    false
                }
            }
        }
        setContentView(web)

        if (savedInstanceState == null) web.loadUrl("http://$APP_HOST/index.html")
        else web.restoreState(savedInstanceState)
    }

    private fun serveAsset(path: String): WebResourceResponse {
        val name = path.trimStart('/').ifEmpty { "index.html" }
        val mime = when (name.substringAfterLast('.', "")) {
            "html" -> "text/html"
            "js" -> "text/javascript"
            "css" -> "text/css"
            "svg" -> "image/svg+xml"
            "json" -> "application/json"
            "png" -> "image/png"
            else -> "application/octet-stream"
        }
        return try {
            WebResourceResponse(mime, "utf-8", assets.open(name))
        } catch (e: Exception) {
            WebResourceResponse("text/plain", "utf-8", 404, "Not Found", emptyMap(), "not found".byteInputStream())
        }
    }

    inner class Bridge {
        @JavascriptInterface
        fun startDiscovery() = runOnUiThread { this@MainActivity.startDiscovery() }

        /** Turns "wake with computer" on or off; servers is a JSON list of "host:port". */
        @JavascriptInterface
        fun setWake(enabled: Boolean, servers: String) = runOnUiThread {
            if (enabled && Build.VERSION.SDK_INT >= 33 &&
                checkSelfPermission(android.Manifest.permission.POST_NOTIFICATIONS) != android.content.pm.PackageManager.PERMISSION_GRANTED
            ) {
                requestPermissions(arrayOf(android.Manifest.permission.POST_NOTIFICATIONS), REQ_NOTIFY)
            }
            runCatching { WakeService.configure(this@MainActivity, enabled, servers) }
            applyKeepAwake()
        }

        /** The dashboard reports whether any computer is currently connected. */
        @JavascriptInterface
        fun setConnected(connected: Boolean) = runOnUiThread {
            anyConnected = connected
            applyKeepAwake()
        }

        /** What Android still needs granted for wake-with-computer, as JSON. */
        @JavascriptInterface
        fun powerStatus(): String {
            val power = getSystemService(Context.POWER_SERVICE) as PowerManager
            return JSONObject()
                .put("screenOff", policy().isAdminActive(adminComponent()))
                .put("background", power.isIgnoringBatteryOptimizations(packageName))
                .put("stayAwake", Settings.Global.getInt(contentResolver, Settings.Global.STAY_ON_WHILE_PLUGGED_IN, 0) != 0)
                .put("timeoutSec", Settings.System.getInt(contentResolver, Settings.System.SCREEN_OFF_TIMEOUT, 0) / 1000)
                .toString()
        }

        /** Opens Android's own screen for making Pulsedeck a device admin app (screen-off only). */
        @JavascriptInterface
        fun requestScreenOff() = runOnUiThread {
            val intent = Intent(DevicePolicyManager.ACTION_ADD_DEVICE_ADMIN)
                .putExtra(DevicePolicyManager.EXTRA_DEVICE_ADMIN, adminComponent())
                .putExtra(DevicePolicyManager.EXTRA_ADD_EXPLANATION, getString(R.string.admin_description))
            runCatching { startActivity(intent) }
        }

        /** Opens Android's prompt for exempting Pulsedeck from battery optimisation. */
        @SuppressLint("BatteryLife")
        @JavascriptInterface
        fun requestBackground() = runOnUiThread {
            runCatching {
                startActivity(Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, Uri.parse("package:$packageName")))
            }
        }

        @JavascriptInterface
        fun saveFile(name: String, data: String) = runOnUiThread {
            pendingSave = data
            val intent = Intent(Intent.ACTION_CREATE_DOCUMENT)
                .addCategory(Intent.CATEGORY_OPENABLE)
                .setType("application/json")
                .putExtra(Intent.EXTRA_TITLE, name)
            runCatching { startActivityForResult(intent, REQ_SAVE) }
        }
    }

    @Deprecated("Framework Activity API; avoids pulling in AndroidX for two callbacks")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        when (requestCode) {
            REQ_PICK -> {
                fileCallback?.onReceiveValue(WebChromeClient.FileChooserParams.parseResult(resultCode, data))
                fileCallback = null
            }
            REQ_SAVE -> {
                val uri = data?.data
                val text = pendingSave
                pendingSave = null
                if (resultCode == RESULT_OK && uri != null && text != null) {
                    runCatching { contentResolver.openOutputStream(uri)?.use { it.write(text.toByteArray()) } }
                }
            }
        }
    }

    private var anyConnected = false
    private val handler = Handler(Looper.getMainLooper())
    private val screenOff = Runnable { turnScreenOff() }

    private fun policy() = getSystemService(Context.DEVICE_POLICY_SERVICE) as DevicePolicyManager
    private fun adminComponent() = ComponentName(this, ScreenAdmin::class.java)

    /**
     * Normally the screen simply stays on. With "wake with computer" enabled it
     * stays on only while a computer is connected. Once none is, the screen is
     * released to the system's own timeout, and after a grace period Pulsedeck
     * switches it off itself if the user allowed that (the system timeout does
     * nothing on devices set to stay awake while charging).
     */
    private fun applyKeepAwake() {
        handler.removeCallbacks(screenOff)
        if (!WakeService.enabled(this) || anyConnected) {
            window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        } else {
            window.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
            handler.postDelayed(screenOff, SCREEN_OFF_GRACE_MS)
        }
    }

    private fun turnScreenOff() {
        if (!WakeService.enabled(this) || anyConnected || !hasWindowFocus()) return
        if (policy().isAdminActive(adminComponent())) runCatching { policy().lockNow() }
    }

    /** Using the tablet (adding a computer, say) restarts the grace period. */
    override fun dispatchTouchEvent(ev: MotionEvent): Boolean {
        if (ev.actionMasked == MotionEvent.ACTION_DOWN && !anyConnected && WakeService.enabled(this)) {
            handler.removeCallbacks(screenOff)
            handler.postDelayed(screenOff, SCREEN_OFF_GRACE_MS)
        }
        return super.dispatchTouchEvent(ev)
    }

    // ---- mDNS discovery of _pulsedeck._tcp servers ----

    private fun startDiscovery() {
        if (discovery != null) return
        val manager = getSystemService(Context.NSD_SERVICE) as NsdManager
        nsd = manager
        // Some devices drop multicast packets unless a lock is held.
        val wifi = applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager
        multicastLock = wifi.createMulticastLock("pulsedeck").apply {
            setReferenceCounted(false)
            runCatching { acquire() }
        }
        val listener = object : NsdManager.DiscoveryListener {
            override fun onServiceFound(info: NsdServiceInfo) = runOnUiThread {
                resolveQueue.add(info)
                resolveNext()
            }
            override fun onServiceLost(info: NsdServiceInfo) {}
            override fun onDiscoveryStarted(type: String) {}
            override fun onDiscoveryStopped(type: String) {}
            override fun onStartDiscoveryFailed(type: String, code: Int) { discovery = null }
            override fun onStopDiscoveryFailed(type: String, code: Int) {}
        }
        discovery = listener
        runCatching { manager.discoverServices(SERVICE_TYPE, NsdManager.PROTOCOL_DNS_SD, listener) }
            .onFailure { discovery = null }
    }

    /** NsdManager resolves one service at a time on older Android versions. */
    private fun resolveNext() {
        if (resolving) return
        val next = resolveQueue.poll() ?: return
        resolving = true
        @Suppress("DEPRECATION")
        nsd?.resolveService(next, object : NsdManager.ResolveListener {
            override fun onResolveFailed(info: NsdServiceInfo, code: Int) = done()
            override fun onServiceResolved(info: NsdServiceInfo) {
                @Suppress("DEPRECATION")
                val host = info.host?.hostAddress
                // Skip IPv6 for now: the UI builds ws://host:port URLs.
                if (host != null && !host.contains(':')) announce(info.serviceName, host, info.port)
                done()
            }
            private fun done() = runOnUiThread {
                resolving = false
                resolveNext()
            }
        })
    }

    private fun announce(name: String, host: String, port: Int) = runOnUiThread {
        val js = "window.__pulsedeckFound && window.__pulsedeckFound(${JSONObject.quote(name)}, ${JSONObject.quote(host)}, $port)"
        web.evaluateJavascript(js, null)
    }

    private fun stopDiscovery() {
        discovery?.let { l -> runCatching { nsd?.stopServiceDiscovery(l) } }
        discovery = null
        resolveQueue.clear()
        resolving = false
        multicastLock?.let { runCatching { it.release() } }
        multicastLock = null
    }

    // ---- lifecycle ----

    private var discoveryWanted = false

    override fun onResume() {
        super.onResume()
        web.onResume()
        // Connections made before the screen went off are not to be trusted.
        web.evaluateJavascript("window.__pulsedeckResume && window.__pulsedeckResume()", null)
        applyKeepAwake()
        if (discoveryWanted) startDiscovery()
    }

    override fun onPause() {
        discoveryWanted = discovery != null
        stopDiscovery()
        handler.removeCallbacks(screenOff)
        web.onPause()
        super.onPause()
    }

    /** Back closes an open panel or edit mode first, and only then leaves the app. */
    @Deprecated("Framework Activity API")
    override fun onBackPressed() {
        web.evaluateJavascript("!!(window.__pulsedeckBack && window.__pulsedeckBack())") { handled ->
            if (handled != "true") {
                @Suppress("DEPRECATION")
                super.onBackPressed()
            }
        }
    }

    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        web.saveState(outState)
    }

    override fun onWindowFocusChanged(hasFocus: Boolean) {
        super.onWindowFocusChanged(hasFocus)
        if (hasFocus) goImmersive()
    }

    private fun goImmersive() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            window.setDecorFitsSystemWindows(false)
            window.insetsController?.let {
                it.hide(WindowInsets.Type.systemBars())
                it.systemBarsBehavior = WindowInsetsController.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
            }
        } else {
            @Suppress("DEPRECATION")
            window.decorView.systemUiVisibility = (View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY
                or View.SYSTEM_UI_FLAG_FULLSCREEN or View.SYSTEM_UI_FLAG_HIDE_NAVIGATION
                or View.SYSTEM_UI_FLAG_LAYOUT_STABLE or View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN
                or View.SYSTEM_UI_FLAG_LAYOUT_HIDE_NAVIGATION)
        }
    }

    private companion object {
        const val APP_HOST = "pulsedeck.app"
        const val SERVICE_TYPE = "_pulsedeck._tcp."
        const val REQ_PICK = 1
        const val REQ_SAVE = 2
        const val REQ_NOTIFY = 3
        const val SCREEN_OFF_GRACE_MS = 60_000L
    }
}

package io.github.ssenerg.gochat.ui

import android.Manifest
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.content.pm.ServiceInfo
import android.graphics.drawable.Icon
import android.os.Build
import android.os.IBinder
import io.github.ssenerg.gochat.core.state.AppState
import io.github.ssenerg.gochat.core.state.GoChatApp
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.launch

/**
 * Keeps a voice call going while the app is in the background, with the
 * screen off or another app open: Android lets only a foreground service,
 * which shows a notice, use the microphone then.
 */
class VoiceCallService : Service() {
    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_LEAVE) {
            onLeave?.invoke()
            return START_NOT_STICKY
        }
        val name = intent?.getStringExtra(EXTRA_NAME).orEmpty()
        val notices = getSystemService(NotificationManager::class.java)
        notices.createNotificationChannel(
            NotificationChannel(NOTICE_CHANNEL, "Voice channels", NotificationManager.IMPORTANCE_LOW).apply { setShowBadge(false) },
        )
        val open = packageManager.getLaunchIntentForPackage(packageName)?.let {
            PendingIntent.getActivity(this, 0, it, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        }
        val leave = PendingIntent.getService(
            this, 1, Intent(this, VoiceCallService::class.java).setAction(ACTION_LEAVE), PendingIntent.FLAG_IMMUTABLE,
        )
        val notice = Notification.Builder(this, NOTICE_CHANNEL)
            .setSmallIcon(R.drawable.gochat_voice)
            .setContentTitle(if (name.isEmpty()) "In a voice channel" else "In $name")
            .setContentText("Talking in GoChat")
            .setContentIntent(open)
            .setOngoing(true)
            .setCategory(Notification.CATEGORY_CALL)
            .addAction(Notification.Action.Builder(null as Icon?, "Leave", leave).build())
            .build()
        if (Build.VERSION.SDK_INT >= 29) {
            startForeground(NOTICE_ID, notice, ServiceInfo.FOREGROUND_SERVICE_TYPE_MICROPHONE)
        } else {
            startForeground(NOTICE_ID, notice)
        }
        return START_NOT_STICKY
    }

    companion object {
        private const val NOTICE_CHANNEL = "voice"
        private const val NOTICE_ID = 1
        private const val EXTRA_NAME = "name"
        private const val ACTION_LEAVE = "io.github.ssenerg.gochat.LEAVE_VOICE"

        /** Leaves the call the service keeps, from its notice. */
        @Volatile
        private var onLeave: (() -> Unit)? = null

        /** Runs the service for as long as [app]'s person talks in a voice channel. */
        @OptIn(ExperimentalCoroutinesApi::class)
        fun follow(context: Context, app: GoChatApp, scope: CoroutineScope) {
            val ctx = context.applicationContext
            val service = Intent(ctx, VoiceCallService::class.java)
            scope.launch {
                app.state
                    .flatMapLatest { st ->
                        val session = (st as? AppState.SignedIn)?.session ?: return@flatMapLatest flowOf(null)
                        session.voice.state.map { call -> call?.let { session to it.name } }
                    }
                    .distinctUntilChanged()
                    .collect { call ->
                        if (call == null) {
                            onLeave = null
                            ctx.stopService(service)
                            return@collect
                        }
                        val (session, name) = call
                        onLeave = { session.voice.leave() }
                        // A call starts once the microphone is allowed, which a
                        // microphone service needs; starting it from the
                        // background, Android refuses, and the call goes on.
                        if (ctx.checkSelfPermission(Manifest.permission.RECORD_AUDIO) == PackageManager.PERMISSION_GRANTED) {
                            runCatching { ctx.startForegroundService(Intent(service).putExtra(EXTRA_NAME, name)) }
                        }
                    }
            }
        }
    }
}

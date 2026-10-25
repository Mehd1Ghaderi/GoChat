package io.github.ssenerg.gochat.ui

import android.Manifest
import android.content.ActivityNotFoundException
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.media.AudioAttributes
import android.media.MediaPlayer
import android.media.MediaRecorder
import android.net.Uri
import android.os.Build
import android.os.SystemClock
import android.provider.OpenableColumns
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.platform.LocalContext
import androidx.core.content.FileProvider
import io.github.ssenerg.gochat.core.state.KeyValueStore
import io.github.ssenerg.gochat.core.voice.RtcEngine
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File

/** Android's side of the app; the Application keeps one. */
class AndroidPlatform(private val context: Context, scope: CoroutineScope) : Platform {
    override val store: KeyValueStore = PreferencesStore(context)
    override val player: AudioPlayer = AndroidAudioPlayer(context, scope)
    override val rtc: RtcEngine = AndroidRtcEngine(context)

    override fun openFile(name: String, mimeType: String, bytes: ByteArray) {
        val dir = File(context.cacheDir, "shared").apply { mkdirs() }
        // Keep only the file's own name: it is chosen by whoever sent it.
        val safe = name.substringAfterLast('/').substringAfterLast('\\').ifBlank { "file" }
        val file = File(dir, safe)
        file.writeBytes(bytes)
        val uri = FileProvider.getUriForFile(context, context.packageName + ".gochat.files", file)
        val view = Intent(Intent.ACTION_VIEW).setDataAndType(uri, mimeType.ifBlank { "application/octet-stream" })
            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
        val chooser = Intent.createChooser(view, safe).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        try {
            context.startActivity(chooser)
        } catch (_: ActivityNotFoundException) {
            val send = Intent(Intent.ACTION_SEND).setType(mimeType).putExtra(Intent.EXTRA_STREAM, uri)
                .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
            context.startActivity(Intent.createChooser(send, safe).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
        }
    }

    override fun share(text: String) {
        val send = Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, text)
        context.startActivity(Intent.createChooser(send, null).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    }

    override fun copy(text: String) {
        val clipboard = context.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
        clipboard.setPrimaryClip(ClipData.newPlainText("GoChat", text))
    }
}

/** The app's private preferences: no other app can read them. */
private class PreferencesStore(context: Context) : KeyValueStore {
    private val prefs = context.getSharedPreferences("gochat", Context.MODE_PRIVATE)

    override fun get(key: String): String? = prefs.getString(key, null)

    override fun put(key: String, value: String?) {
        prefs.edit().apply { if (value == null) remove(key) else putString(key, value) }.apply()
    }
}

private class AndroidAudioPlayer(private val context: Context, private val scope: CoroutineScope) : AudioPlayer {
    private val _state = MutableStateFlow(PlayerState())
    override val state: StateFlow<PlayerState> = _state.asStateFlow()
    private var player: MediaPlayer? = null
    private var ticker: Job? = null

    override fun play(id: String, bytes: ByteArray, mimeType: String) {
        stop()
        val ext = when {
            "webm" in mimeType -> "webm"
            "ogg" in mimeType -> "ogg"
            else -> "m4a"
        }
        val file = File(context.cacheDir, "voice-play.$ext")
        val p = MediaPlayer()
        try {
            file.writeBytes(bytes)
            p.setAudioAttributes(
                AudioAttributes.Builder().setUsage(AudioAttributes.USAGE_MEDIA).setContentType(AudioAttributes.CONTENT_TYPE_SPEECH).build(),
            )
            p.setDataSource(file.absolutePath)
            p.prepare()
        } catch (_: Exception) {
            p.release()
            _state.value = PlayerState(id = id, error = "This voice message cannot play on this device.")
            return
        }
        p.setOnCompletionListener {
            ticker?.cancel()
            _state.value = _state.value.copy(playing = false, positionMs = 0)
        }
        p.start()
        player = p
        _state.value = PlayerState(id = id, playing = true, durationMs = p.duration.toLong().coerceAtLeast(0))
        tick()
    }

    private fun tick() {
        ticker?.cancel()
        ticker = scope.launch {
            while (isActive) {
                val p = player ?: break
                if (p.isPlaying) _state.value = _state.value.copy(positionMs = p.currentPosition.toLong())
                delay(200)
            }
        }
    }

    override fun pause() {
        player?.takeIf { it.isPlaying }?.pause()
        ticker?.cancel()
        _state.value = _state.value.copy(playing = false)
    }

    override fun resume() {
        val p = player ?: return
        p.start()
        _state.value = _state.value.copy(playing = true)
        tick()
    }

    override fun seek(positionMs: Long) {
        player?.seekTo(positionMs.toInt())
        _state.value = _state.value.copy(positionMs = positionMs)
    }

    override fun stop() {
        ticker?.cancel()
        player?.release()
        player = null
        _state.value = PlayerState()
    }
}

private class AndroidVoiceRecorder(
    private val context: Context,
    private val askPermission: suspend () -> Boolean,
) : VoiceRecorder {
    private var recorder: MediaRecorder? = null
    private var file: File? = null
    private var startedAt = 0L

    override suspend fun start(): Boolean {
        if (!askPermission()) return false
        cancel()
        val f = File(context.cacheDir, "voice-${System.currentTimeMillis()}.m4a")
        val r = if (Build.VERSION.SDK_INT >= 31) MediaRecorder(context) else @Suppress("DEPRECATION") MediaRecorder()
        return try {
            // AAC in MP4: the server takes it, and every platform plays it.
            r.setAudioSource(MediaRecorder.AudioSource.MIC)
            r.setOutputFormat(MediaRecorder.OutputFormat.MPEG_4)
            r.setAudioEncoder(MediaRecorder.AudioEncoder.AAC)
            r.setAudioEncodingBitRate(64_000)
            r.setAudioSamplingRate(44_100)
            r.setAudioChannels(1)
            r.setOutputFile(f.absolutePath)
            r.prepare()
            r.start()
            recorder = r
            file = f
            startedAt = SystemClock.elapsedRealtime()
            true
        } catch (_: Exception) {
            r.release()
            f.delete()
            false
        }
    }

    override suspend fun stop(): Recording? {
        val r = recorder ?: return null
        val f = file
        recorder = null
        file = null
        val duration = SystemClock.elapsedRealtime() - startedAt
        // Stopping right after starting throws: there is nothing recorded.
        val ok = runCatching { r.stop() }.isSuccess
        r.release()
        if (!ok || f == null || duration < 500) {
            f?.delete()
            return null
        }
        val bytes = withContext(Dispatchers.IO) { f.readBytes() }
        f.delete()
        return Recording(bytes, "voice-message.m4a", duration)
    }

    override fun cancel() {
        recorder?.let {
            runCatching { it.stop() }
            it.release()
        }
        recorder = null
        file?.delete()
        file = null
    }

    override fun level(): Float = recorder?.let { r -> runCatching { r.maxAmplitude / 32767f }.getOrNull() } ?: 0f
}

@Composable
actual fun rememberVoiceRecorder(): VoiceRecorder {
    val context = LocalContext.current
    val microphone = rememberMicrophoneAccess()
    return remember(context) { AndroidVoiceRecorder(context.applicationContext, microphone) }
}

@Composable
actual fun rememberMicrophoneAccess(): suspend () -> Boolean {
    val context = LocalContext.current
    val pending = remember { arrayOfNulls<CompletableDeferred<Boolean>>(1) }
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        pending[0]?.complete(granted)
        pending[0] = null
    }
    val currentLauncher by rememberUpdatedState(launcher)
    return remember(context) {
        suspend {
            context.checkSelfPermission(Manifest.permission.RECORD_AUDIO) == PackageManager.PERMISSION_GRANTED || run {
                val d = CompletableDeferred<Boolean>()
                pending[0] = d
                currentLauncher.launch(Manifest.permission.RECORD_AUDIO)
                d.await()
            }
        }
    }
}

@Composable
actual fun rememberFilePicker(onPicked: (PickedFile) -> Unit, onError: (String) -> Unit): () -> Unit {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val picked by rememberUpdatedState(onPicked)
    val failed by rememberUpdatedState(onError)
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.GetContent()) { uri: Uri? ->
        if (uri != null) {
            scope.launch {
                try {
                    picked(readPicked(context, uri))
                } catch (e: Exception) {
                    failed(e.message ?: "Could not read the file.")
                }
            }
        }
    }
    return { launcher.launch("*/*") }
}

private suspend fun readPicked(context: Context, uri: Uri): PickedFile = withContext(Dispatchers.IO) {
    val resolver = context.contentResolver
    var name = "file"
    var size = -1L
    resolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE), null, null, null)?.use { c ->
        if (c.moveToFirst()) {
            c.getString(0)?.let { name = it }
            if (!c.isNull(1)) size = c.getLong(1)
        }
    }
    if (size > MAX_PICKED_BYTES) throw IllegalArgumentException("The file is too large to send.")
    val bytes = resolver.openInputStream(uri)?.use { it.readBytes() } ?: throw IllegalStateException("Could not read the file.")
    PickedFile(name, resolver.getType(uri) ?: "application/octet-stream", bytes)
}

@Composable
actual fun PlatformBackHandler(enabled: Boolean, onBack: () -> Unit) {
    BackHandler(enabled = enabled, onBack = onBack)
}

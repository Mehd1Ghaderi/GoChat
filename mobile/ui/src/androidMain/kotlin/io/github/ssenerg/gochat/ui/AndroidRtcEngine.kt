package io.github.ssenerg.gochat.ui

import android.content.Context
import android.media.AudioAttributes
import android.media.AudioDeviceCallback
import android.media.AudioDeviceInfo
import android.media.AudioFocusRequest
import android.media.AudioManager
import android.os.Build
import android.os.Handler
import android.os.Looper
import io.github.ssenerg.gochat.core.voice.IceCandidate
import io.github.ssenerg.gochat.core.voice.IceServer
import io.github.ssenerg.gochat.core.voice.RtcEngine
import io.github.ssenerg.gochat.core.voice.RtcListener
import io.github.ssenerg.gochat.core.voice.RtcPeer
import io.github.ssenerg.gochat.core.voice.RtcState
import kotlinx.coroutines.suspendCancellableCoroutine
import org.webrtc.AudioSource
import org.webrtc.AudioTrack
import org.webrtc.DataChannel
import org.webrtc.MediaConstraints
import org.webrtc.MediaStream
import org.webrtc.PeerConnection
import org.webrtc.PeerConnectionFactory
import org.webrtc.SdpObserver
import org.webrtc.SessionDescription
import org.webrtc.audio.JavaAudioDeviceModule
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException
import kotlin.math.sqrt

/**
 * Voice channels on Android, over the WebRTC library Stream builds from
 * Google's: one connection at a time, which sends the microphone, plays the
 * others on the loudspeaker or a headset, and hears how loud the person is.
 */
class AndroidRtcEngine(context: Context) : RtcEngine {
    private val context = context.applicationContext
    private var factory: PeerConnectionFactory? = null
    private var audio: JavaAudioDeviceModule? = null

    /** Whom the microphone's loudness goes to: the connection open now. */
    @Volatile
    private var levels: RtcListener? = null

    private fun factory(): PeerConnectionFactory {
        factory?.let { return it }
        PeerConnectionFactory.initialize(PeerConnectionFactory.InitializationOptions.builder(context).createInitializationOptions())
        val meter = LevelMeter { level -> levels?.onLevel(level) }
        val module = JavaAudioDeviceModule.builder(context)
            .setUseHardwareAcousticEchoCanceler(JavaAudioDeviceModule.isBuiltInAcousticEchoCancelerSupported())
            .setUseHardwareNoiseSuppressor(JavaAudioDeviceModule.isBuiltInNoiseSuppressorSupported())
            .setSamplesReadyCallback { samples -> meter.add(samples.data) }
            .createAudioDeviceModule()
        val f = PeerConnectionFactory.builder().setAudioDeviceModule(module).createPeerConnectionFactory()
        audio = module
        factory = f
        return f
    }

    override fun connect(iceServers: List<IceServer>, listener: RtcListener): RtcPeer {
        val f = factory()
        val config = PeerConnection.RTCConfiguration(
            iceServers.map { s -> PeerConnection.IceServer.builder(s.urls).setUsername(s.username).setPassword(s.credential).createIceServer() },
        ).apply {
            sdpSemantics = PeerConnection.SdpSemantics.UNIFIED_PLAN
            bundlePolicy = PeerConnection.BundlePolicy.MAXBUNDLE
            rtcpMuxPolicy = PeerConnection.RtcpMuxPolicy.REQUIRE
            // A phone that changes networks offers its new addresses, and
            // the voice may carry on over them.
            continualGatheringPolicy = PeerConnection.ContinualGatheringPolicy.GATHER_CONTINUALLY
        }
        val pc = f.createPeerConnection(config, Observer(listener)) ?: throw IllegalStateException("WebRTC could not start")
        // WebRTC's own echo cancellation, noise suppression and gain
        // control are on by default, as getUserMedia's are on the web.
        val source = f.createAudioSource(MediaConstraints())
        val track = f.createAudioTrack("mic", source)
        pc.addTrack(track, listOf("mic"))
        val routing = AudioRouting(context).also { it.start() }
        levels = listener
        return AndroidRtcPeer(pc, source, track, routing, audio) {
            if (levels === listener) levels = null
        }
    }
}

private class AndroidRtcPeer(
    private val pc: PeerConnection,
    private val source: AudioSource,
    private val track: AudioTrack,
    private val routing: AudioRouting,
    private val audio: JavaAudioDeviceModule?,
    private val onClose: () -> Unit,
) : RtcPeer {
    private val closed = AtomicBoolean(false)

    override suspend fun answer(offer: String): String {
        pc.awaitSet { pc.setRemoteDescription(it, SessionDescription(SessionDescription.Type.OFFER, offer)) }
        val answer = suspendCancellableCoroutine { c ->
            pc.createAnswer(
                object : Sdp() {
                    override fun onCreateSuccess(sdp: SessionDescription) = c.resume(sdp)

                    override fun onCreateFailure(error: String?) = c.resumeWithException(IllegalStateException(error ?: "no answer"))
                },
                MediaConstraints(),
            )
        }
        pc.awaitSet { pc.setLocalDescription(it, answer) }
        return answer.description
    }

    override suspend fun addCandidate(candidate: IceCandidate) {
        if (closed.get()) return
        pc.addIceCandidate(org.webrtc.IceCandidate(candidate.sdpMid ?: "", candidate.sdpMLineIndex ?: 0, candidate.candidate))
    }

    override fun setMuted(muted: Boolean) {
        if (!closed.get()) track.setEnabled(!muted)
    }

    override fun setDeafened(deafened: Boolean) {
        // Silences the speaker, and so every voice that comes in.
        if (!closed.get()) audio?.setSpeakerMute(deafened)
    }

    override fun close() {
        if (!closed.compareAndSet(false, true)) return
        onClose()
        audio?.setSpeakerMute(false)
        pc.dispose()
        source.dispose()
        routing.stop()
    }
}

/** Runs a set call of the peer connection and waits for its outcome. */
private suspend fun PeerConnection.awaitSet(call: (SdpObserver) -> Unit) = suspendCancellableCoroutine { c ->
    call(
        object : Sdp() {
            override fun onSetSuccess() = c.resume(Unit)

            override fun onSetFailure(error: String?) = c.resumeWithException(IllegalStateException(error ?: "refused"))
        },
    )
}

private open class Sdp : SdpObserver {
    override fun onCreateSuccess(sdp: SessionDescription) {}

    override fun onSetSuccess() {}

    override fun onCreateFailure(error: String?) {}

    override fun onSetFailure(error: String?) {}
}

/** Hands WebRTC's events on to the call. */
private class Observer(private val listener: RtcListener) : PeerConnection.Observer {
    override fun onIceCandidate(candidate: org.webrtc.IceCandidate) =
        listener.onCandidate(IceCandidate(candidate.sdp, candidate.sdpMid, candidate.sdpMLineIndex))

    override fun onConnectionChange(newState: PeerConnection.PeerConnectionState) {
        when (newState) {
            PeerConnection.PeerConnectionState.NEW, PeerConnection.PeerConnectionState.CONNECTING -> listener.onState(RtcState.Connecting)
            PeerConnection.PeerConnectionState.CONNECTED -> listener.onState(RtcState.Connected)
            PeerConnection.PeerConnectionState.DISCONNECTED -> listener.onState(RtcState.Disconnected)
            PeerConnection.PeerConnectionState.FAILED -> listener.onState(RtcState.Failed)
            PeerConnection.PeerConnectionState.CLOSED -> {}
        }
    }

    override fun onSignalingChange(state: PeerConnection.SignalingState) {}

    override fun onIceConnectionChange(state: PeerConnection.IceConnectionState) {}

    override fun onIceConnectionReceivingChange(receiving: Boolean) {}

    override fun onIceGatheringChange(state: PeerConnection.IceGatheringState) {}

    override fun onIceCandidatesRemoved(candidates: Array<out org.webrtc.IceCandidate>) {}

    // The voices that come in play by themselves.
    override fun onAddStream(stream: MediaStream) {}

    override fun onRemoveStream(stream: MediaStream) {}

    override fun onDataChannel(channel: DataChannel) {}

    override fun onRenegotiationNeeded() {}
}

/**
 * The microphone's loudness: the root mean square of its 16-bit samples,
 * the loudest of each 60 ms, as often as the web measures it.
 */
private class LevelMeter(private val emit: (Float) -> Unit) {
    private var loudest = 0f
    private var chunks = 0

    fun add(pcm: ByteArray) {
        var sum = 0.0
        val n = pcm.size / 2
        if (n == 0) return
        for (i in 0 until n) {
            val s = ((pcm[2 * i + 1].toInt() shl 8) or (pcm[2 * i].toInt() and 0xff)).toShort() / 32768.0
            sum += s * s
        }
        loudest = maxOf(loudest, sqrt(sum / n).toFloat())
        // WebRTC hands over 10 ms at a time.
        if (++chunks >= 6) {
            emit(loudest)
            loudest = 0f
            chunks = 0
        }
    }
}

/**
 * Puts the phone in a call: voice communication mode and audio focus, on a
 * headset when one is connected and on the loudspeaker otherwise, as voice
 * chat apps do; a headset plugged in or out moves the voice.
 */
private class AudioRouting(context: Context) {
    private val am = context.getSystemService(AudioManager::class.java)
    private var focus: AudioFocusRequest? = null
    private var mode = AudioManager.MODE_NORMAL
    private var sco = false
    private val handler = Handler(Looper.getMainLooper())
    private val devices = object : AudioDeviceCallback() {
        override fun onAudioDevicesAdded(added: Array<out AudioDeviceInfo>) = route()

        override fun onAudioDevicesRemoved(removed: Array<out AudioDeviceInfo>) = route()
    }

    fun start() {
        mode = am.mode
        val attributes = AudioAttributes.Builder()
            .setUsage(AudioAttributes.USAGE_VOICE_COMMUNICATION)
            .setContentType(AudioAttributes.CONTENT_TYPE_SPEECH)
            .build()
        focus = AudioFocusRequest.Builder(AudioManager.AUDIOFOCUS_GAIN).setAudioAttributes(attributes).build().also { am.requestAudioFocus(it) }
        am.mode = AudioManager.MODE_IN_COMMUNICATION
        route()
        am.registerAudioDeviceCallback(devices, handler)
    }

    private fun route() {
        if (Build.VERSION.SDK_INT >= 31) {
            val available = am.availableCommunicationDevices
            val pick = available.firstOrNull { it.type in HEADSETS } ?: available.firstOrNull { it.type == AudioDeviceInfo.TYPE_BUILTIN_SPEAKER }
            if (pick != null) am.setCommunicationDevice(pick)
        } else {
            val outputs = am.getDevices(AudioManager.GET_DEVICES_OUTPUTS)
            val bluetooth = outputs.any { it.type == AudioDeviceInfo.TYPE_BLUETOOTH_SCO }
            val wired = outputs.any { it.type in HEADSETS && it.type != AudioDeviceInfo.TYPE_BLUETOOTH_SCO }
            @Suppress("DEPRECATION")
            run {
                if (bluetooth && !wired && !sco) {
                    am.startBluetoothSco()
                    am.isBluetoothScoOn = true
                    sco = true
                } else if (!bluetooth && sco) {
                    am.stopBluetoothSco()
                    am.isBluetoothScoOn = false
                    sco = false
                }
                am.isSpeakerphoneOn = !bluetooth && !wired
            }
        }
    }

    fun stop() {
        am.unregisterAudioDeviceCallback(devices)
        if (Build.VERSION.SDK_INT >= 31) {
            am.clearCommunicationDevice()
        } else {
            @Suppress("DEPRECATION")
            run {
                if (sco) {
                    am.stopBluetoothSco()
                    am.isBluetoothScoOn = false
                    sco = false
                }
                am.isSpeakerphoneOn = false
            }
        }
        am.mode = mode
        focus?.let { am.abandonAudioFocusRequest(it) }
        focus = null
    }

    private companion object {
        val HEADSETS = setOf(
            AudioDeviceInfo.TYPE_WIRED_HEADSET,
            AudioDeviceInfo.TYPE_WIRED_HEADPHONES,
            AudioDeviceInfo.TYPE_USB_HEADSET,
            AudioDeviceInfo.TYPE_BLUETOOTH_SCO,
            AudioDeviceInfo.TYPE_HEARING_AID,
            AudioDeviceInfo.TYPE_BLE_HEADSET,
        )
    }
}

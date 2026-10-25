package io.github.ssenerg.gochat.ui

import io.github.ssenerg.gochat.core.voice.IceCandidate
import io.github.ssenerg.gochat.core.voice.IceServer
import io.github.ssenerg.gochat.core.voice.RtcEngine
import io.github.ssenerg.gochat.core.voice.RtcListener
import io.github.ssenerg.gochat.core.voice.RtcPeer
import io.github.ssenerg.gochat.core.voice.RtcState
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

// WebRTC as the iOS app supplies it. Its Swift side links Google's WebRTC,
// as stasel/WebRTC builds it, and implements these protocols; Kotlin adapts
// them to the core's RtcEngine. Kotlin calls in on the main thread, and
// Swift calls back on any.

/** Starts voice connections. */
interface IosRtcEngine {
    fun connect(iceServers: List<IosIceServer>, events: IosRtcEvents): IosRtcPeer
}

/** A TURN server the voice server lends. */
class IosIceServer(val urls: List<String>, val username: String, val credential: String)

/** What a connection tells the call. */
interface IosRtcEvents {
    /** A candidate of this device's; [sdpMLineIndex] is -1 when it has none. */
    fun candidate(sdp: String, sdpMid: String?, sdpMLineIndex: Int)

    /** "connecting", "connected", "disconnected" or "failed". */
    fun state(state: String)

    /** How loud the microphone is, from 0 to 1, several times a second. */
    fun level(level: Float)
}

/** Where an answer goes, once WebRTC has made it. */
interface IosRtcAnswer {
    fun answered(sdp: String)

    fun failed(reason: String)
}

/** One voice connection, which sends the microphone and plays the others. */
interface IosRtcPeer {
    /** Takes the voice server's offer, and answers [reply]. */
    fun answer(offer: String, reply: IosRtcAnswer)

    fun addCandidate(sdp: String, sdpMid: String?, sdpMLineIndex: Int)

    fun setMuted(muted: Boolean)

    fun setDeafened(deafened: Boolean)

    /** Ends the connection; again, it does nothing. */
    fun close()
}

/** The core's RtcEngine, over the Swift one. */
internal class SwiftRtc(private val native: IosRtcEngine) : RtcEngine {
    override fun connect(iceServers: List<IceServer>, listener: RtcListener): RtcPeer {
        val events = object : IosRtcEvents {
            override fun candidate(sdp: String, sdpMid: String?, sdpMLineIndex: Int) =
                listener.onCandidate(IceCandidate(sdp, sdpMid, sdpMLineIndex.takeIf { it >= 0 }))

            override fun state(state: String) {
                when (state) {
                    "connecting" -> listener.onState(RtcState.Connecting)
                    "connected" -> listener.onState(RtcState.Connected)
                    "disconnected" -> listener.onState(RtcState.Disconnected)
                    "failed" -> listener.onState(RtcState.Failed)
                }
            }

            override fun level(level: Float) = listener.onLevel(level)
        }
        val peer = native.connect(iceServers.map { IosIceServer(it.urls, it.username, it.credential) }, events)
        return object : RtcPeer {
            override suspend fun answer(offer: String): String = suspendCancellableCoroutine { c ->
                peer.answer(
                    offer,
                    object : IosRtcAnswer {
                        override fun answered(sdp: String) = c.resume(sdp)

                        override fun failed(reason: String) = c.resumeWithException(IllegalStateException(reason))
                    },
                )
            }

            override suspend fun addCandidate(candidate: IceCandidate) =
                peer.addCandidate(candidate.candidate, candidate.sdpMid, candidate.sdpMLineIndex ?: -1)

            override fun setMuted(muted: Boolean) = peer.setMuted(muted)

            override fun setDeafened(deafened: Boolean) = peer.setDeafened(deafened)

            override fun close() = peer.close()
        }
    }
}

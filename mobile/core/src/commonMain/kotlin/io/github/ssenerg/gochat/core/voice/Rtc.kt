package io.github.ssenerg.gochat.core.voice

/** A TURN server the voice server lends, as WebRTC's RTCIceServer has it. */
data class IceServer(val urls: List<String>, val username: String = "", val credential: String = "")

/** An ICE candidate, as WebRTC's RTCIceCandidateInit has it. */
data class IceCandidate(val candidate: String, val sdpMid: String?, val sdpMLineIndex: Int?)

/** How a voice connection stands. */
enum class RtcState { Connecting, Connected, Disconnected, Failed }

/** What a platform's WebRTC tells a call, from any thread. */
interface RtcListener {
    /** A candidate of this device's, for the voice server. */
    fun onCandidate(candidate: IceCandidate)

    fun onState(state: RtcState)

    /** How loud the microphone is, from 0 to 1, several times a second. */
    fun onLevel(level: Float)
}

/**
 * A platform's WebRTC, which carries the voice of a call: Android's comes
 * from the stream-webrtc-android library, iOS's from the WebRTC package the
 * Swift side links.
 */
interface RtcEngine {
    /**
     * Starts a connection that sends the microphone and plays the voices
     * that come back. The microphone is allowed by then.
     */
    fun connect(iceServers: List<IceServer>, listener: RtcListener): RtcPeer
}

/** One voice connection to the voice server, which always offers. */
interface RtcPeer {
    /** Takes an offer of the voice server's and returns the answer to send. */
    suspend fun answer(offer: String): String

    /** Adds a candidate of the voice server's, once its first offer is in. */
    suspend fun addCandidate(candidate: IceCandidate)

    /** Stops sending the microphone, or sends it again. */
    fun setMuted(muted: Boolean)

    /** Stops playing the others, or plays them again. */
    fun setDeafened(deafened: Boolean)

    /** Ends the connection and gives back the microphone and the speaker. */
    fun close()
}

package io.github.ssenerg.gochat.core.gateway

import gochat.v1.AppFrame
import gochat.v1.AppView
import gochat.v1.AppWatch
import gochat.v1.ClientFrame
import gochat.v1.ClientReady
import gochat.v1.Event
import io.github.ssenerg.gochat.core.api.ApiClient
import io.ktor.client.plugins.websocket.webSocket
import io.ktor.client.request.header
import io.ktor.http.HttpHeaders
import io.ktor.websocket.Frame
import io.ktor.websocket.readBytes
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.channels.SendChannel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlin.random.Random

/** Where the event socket stands. */
sealed interface Connection {
    data object Connecting : Connection

    /** Events arrive. */
    data class Online(val ready: ClientReady) : Connection

    /** The socket is down; another attempt follows. */
    data class Offline(val reason: String) : Connection

    /** The session is no longer valid: the person signs in again. */
    data object SignedOut : Connection
}

/** One thing the socket said. */
sealed interface GatewayMessage {
    /**
     * The socket is (again) ready. When [resync] is set, events may have
     * been lost since the last one: what the app shows should be loaded
     * again.
     */
    data class Ready(val ready: ClientReady, val resync: Boolean) : GatewayMessage

    data class Received(val event: Event) : GatewayMessage
}

/**
 * The event socket of the client API: it keeps one open while started,
 * reconnecting with backoff, and hands on the person's events.
 */
class Gateway(private val api: ApiClient, private val scope: CoroutineScope) {
    private val _connection = MutableStateFlow<Connection>(Connection.Offline("Not connected"))
    val connection: StateFlow<Connection> = _connection.asStateFlow()

    private val _messages = MutableSharedFlow<GatewayMessage>(extraBufferCapacity = 256)
    val messages: SharedFlow<GatewayMessage> = _messages.asSharedFlow()

    private var job: Job? = null

    // What the app shows, which the server sends typing and presence for;
    // told again on each socket.
    private var viewing: String = ""
    private var watching: List<String> = emptyList()
    private var outgoing: SendChannel<Frame>? = null

    /** Says which channel the app shows, null for none. */
    fun view(channelId: String?) {
        viewing = channelId.orEmpty()
        tell(AppFrame(view = AppView(channel_id = viewing)))
    }

    /** Says whose presence and profile the app shows. */
    fun watch(userIds: List<String>) {
        watching = userIds.take(MAX_WATCHED)
        tell(AppFrame(watch = AppWatch(user_ids = watching)))
    }

    private fun tell(frame: AppFrame) {
        outgoing?.trySend(Frame.Binary(true, AppFrame.ADAPTER.encode(frame)))
    }

    fun start() {
        if (job?.isActive == true) return
        job = scope.launch { run() }
    }

    fun stop() {
        job?.cancel()
        job = null
        _connection.value = Connection.Offline("Not connected")
    }

    private suspend fun run() {
        var delayMs = MIN_DELAY_MS
        var connectedBefore = false
        while (scope.isActive) {
            _connection.value = Connection.Connecting
            var gotReady = false
            val outcome = try {
                var code: Short? = null
                api.http.webSocket(urlString = api.gatewayUrl, request = {
                    api.token?.let { header(HttpHeaders.Authorization, "Bearer $it") }
                }) {
                    this@Gateway.outgoing = outgoing
                    for (frame in incoming) {
                        if (frame !is Frame.Binary) continue
                        val f = runCatching { ClientFrame.ADAPTER.decode(frame.readBytes()) }.getOrNull() ?: continue
                        f.ready?.let {
                            // A new socket after a lost one has missed events too.
                            val resync = it.resync || (connectedBefore && !gotReady)
                            gotReady = true
                            connectedBefore = true
                            delayMs = MIN_DELAY_MS
                            _connection.value = Connection.Online(it)
                            tell(AppFrame(view = AppView(channel_id = viewing)))
                            tell(AppFrame(watch = AppWatch(user_ids = watching)))
                            _messages.emit(GatewayMessage.Ready(it, resync))
                        }
                        f.event?.let { _messages.emit(GatewayMessage.Received(it)) }
                    }
                    code = closeReason.await()?.code
                }
                Outcome.of(code)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                // A refused handshake names its status in the message.
                if (e.message?.contains("401") == true) Outcome.SignedOut else Outcome.Retry("Cannot reach the server")
            }
            outgoing = null
            when (outcome) {
                Outcome.SignedOut -> {
                    _connection.value = Connection.SignedOut
                    return
                }
                is Outcome.Retry -> {
                    _connection.value = Connection.Offline(outcome.reason)
                    val wait = if (outcome.slow) TOO_MANY_DELAY_MS else delayMs
                    delay(wait / 2 + Random.nextLong(wait / 2 + 1))
                    if (!gotReady) delayMs = minOf(delayMs * 2, MAX_DELAY_MS)
                }
            }
        }
    }

    private sealed interface Outcome {
        data object SignedOut : Outcome

        data class Retry(val reason: String, val slow: Boolean = false) : Outcome

        companion object {
            fun of(code: Short?): Outcome = when (code?.toInt()) {
                CLOSE_UNAUTHENTICATED, CLOSE_REVOKED -> SignedOut
                CLOSE_TOO_MANY -> Retry("Too many devices connected", slow = true)
                CLOSE_TOO_SLOW -> Retry("Catching up")
                else -> Retry("Reconnecting")
            }
        }
    }

    companion object {
        // The close codes of client.proto.
        const val CLOSE_RETRY = 4000
        const val CLOSE_UNAUTHENTICATED = 4001
        const val CLOSE_REVOKED = 4003
        const val CLOSE_TOO_MANY = 4008
        const val CLOSE_TOO_SLOW = 4009

        /** The most people the server takes in one AppWatch. */
        const val MAX_WATCHED = 1000

        private const val MIN_DELAY_MS = 1_000L
        private const val MAX_DELAY_MS = 30_000L
        private const val TOO_MANY_DELAY_MS = 60_000L
    }
}

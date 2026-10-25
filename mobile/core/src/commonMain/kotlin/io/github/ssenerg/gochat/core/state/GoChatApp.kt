package io.github.ssenerg.gochat.core.state

import gochat.v1.SignUpRequest
import io.github.ssenerg.gochat.core.api.ApiClient
import io.github.ssenerg.gochat.core.api.ApiException
import io.github.ssenerg.gochat.core.api.GoChatApi
import io.github.ssenerg.gochat.core.api.normalizeServerUrl
import io.github.ssenerg.gochat.core.api.userMessage
import io.github.ssenerg.gochat.core.voice.RtcEngine
import io.ktor.client.engine.HttpClientEngine
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/** Durable key-value storage the platform provides: the Keychain, preferences. */
interface KeyValueStore {
    fun get(key: String): String?

    /** Stores [value], or removes the key when it is null. */
    fun put(key: String, value: String?)
}

/** A store that forgets everything, for tests and previews. */
class MemoryStore : KeyValueStore {
    private val map = mutableMapOf<String, String>()

    override fun get(key: String) = map[key]

    override fun put(key: String, value: String?) {
        if (value == null) map.remove(key) else map[key] = value
    }
}

/** Which screen the app is on, as far as signing in goes. */
sealed interface AppState {
    data object Starting : AppState

    data class SignedOut(val server: String, val notice: String? = null) : AppState

    /** The password was right; the account asks for its second factor. */
    data class Verifying(val server: String, val mfaToken: String) : AppState

    data class SignedIn(val session: ChatSession) : AppState
}

/**
 * The app as a whole: it restores the saved session, signs people in and
 * out, and holds the [ChatSession] while they are signed in. Use it from
 * one thread, the main one.
 */
class GoChatApp(
    private val store: KeyValueStore,
    private val scope: CoroutineScope,
    private val engine: HttpClientEngine? = null,
    /** The platform's WebRTC, for voice channels; without it they only show who talks. */
    private val rtc: RtcEngine? = null,
) {
    private val _state = MutableStateFlow<AppState>(AppState.Starting)
    val state: StateFlow<AppState> = _state.asStateFlow()

    private var session: ChatSession? = null

    /** The server people signed in to last, to fill the sign-in form. */
    val lastServer: String get() = store.get(KEY_SERVER) ?: ""

    /** Restores the saved session, if it is still valid. */
    fun start() {
        val server = store.get(KEY_SERVER)
        val token = store.get(KEY_TOKEN)
        if (server == null || token == null) {
            _state.value = AppState.SignedOut(server ?: "")
            return
        }
        scope.launch {
            val api = GoChatApi(ApiClient(server, token, engine))
            try {
                open(api, api.me().user ?: throw ApiException(ApiException.UNAUTHENTICATED, "Sign in again."))
            } catch (e: ApiException) {
                api.client.close()
                if (e.isUnauthenticated) store.put(KEY_TOKEN, null)
                _state.value = AppState.SignedOut(server, if (e.isUnauthenticated) "Your session ended. Sign in again." else e.userMessage())
            } catch (e: Exception) {
                // Offline: keep the token and try again from the sign-in screen.
                api.client.close()
                _state.value = AppState.SignedOut(server, e.userMessage())
            }
        }
    }

    /** Signs in; returns why not, or null. */
    suspend fun signIn(server: String, login: String, password: String): String? {
        val url = normalizeServerUrl(server)
        val api = GoChatApi(ApiClient(url, engine = engine))
        return try {
            val res = api.login(login.trim(), password)
            when {
                res.mfa_token != null -> {
                    api.client.close()
                    _state.value = AppState.Verifying(url, res.mfa_token)
                    null
                }
                res.session != null -> {
                    signedIn(api, url, res.session.token)
                    null
                }
                else -> "The server answered in a way this app does not understand."
            }
        } catch (e: Exception) {
            api.client.close()
            e.userMessage()
        }
    }

    /** Answers the second-factor challenge with a TOTP or recovery code. */
    suspend fun verify(code: String): String? {
        val st = _state.value as? AppState.Verifying ?: return null
        val api = GoChatApi(ApiClient(st.server, engine = engine))
        return try {
            val s = api.verifyMfa(st.mfaToken, code.trim()) ?: return "The server gave no session."
            signedIn(api, st.server, s.token)
            null
        } catch (e: ApiException) {
            api.client.close()
            if (e.message == "the sign-in attempt expired, sign in again") {
                _state.value = AppState.SignedOut(st.server, "The sign-in attempt expired. Sign in again.")
                null
            } else {
                e.userMessage()
            }
        } catch (e: Exception) {
            api.client.close()
            e.userMessage()
        }
    }

    fun cancelVerify() {
        val st = _state.value as? AppState.Verifying ?: return
        _state.value = AppState.SignedOut(st.server)
    }

    /** Creates an account, with an invite unless the server is open; returns why not, or null. */
    suspend fun signUp(server: String, invite: String, username: String, email: String, displayName: String, password: String): String? {
        val url = normalizeServerUrl(server)
        val api = GoChatApi(ApiClient(url, engine = engine))
        return try {
            val res = api.signUp(
                SignUpRequest(
                    username = username.trim(), email = email.trim(), display_name = displayName.trim(),
                    password = password, invite_token = inviteToken(invite),
                ),
            )
            val s = res.session ?: return "The server gave no session."
            signedIn(api, url, s.token)
            null
        } catch (e: Exception) {
            api.client.close()
            e.userMessage()
        }
    }

    /** Signs out of this device. */
    suspend fun signOut() {
        val s = session ?: return
        runCatching { s.api.logout() }
        close(null)
    }

    private suspend fun signedIn(api: GoChatApi, server: String, token: String) {
        api.client.token = token
        val me = api.me().user ?: throw ApiException(ApiException.UNAUTHENTICATED, "Sign in again.")
        store.put(KEY_SERVER, server)
        store.put(KEY_TOKEN, token)
        open(api, me)
    }

    private fun open(api: GoChatApi, me: gochat.v1.User) {
        val s = ChatSession(api, me, store, scope, rtc) { notice -> close(notice) }
        session = s
        _state.value = AppState.SignedIn(s)
        s.start()
    }

    private fun close(notice: String?) {
        val s = session ?: return
        session = null
        s.stop()
        store.put(KEY_TOKEN, null)
        _state.value = AppState.SignedOut(s.api.client.baseUrl, notice)
        s.api.client.close()
    }

    companion object {
        private const val KEY_SERVER = "server"
        private const val KEY_TOKEN = "token"

        /** Takes an invite link or its bare token. */
        fun inviteToken(input: String): String = input.trim().substringAfterLast("/join/").substringBefore('?').trim('/')
    }
}

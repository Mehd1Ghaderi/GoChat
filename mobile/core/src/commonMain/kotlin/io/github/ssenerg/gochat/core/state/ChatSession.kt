package io.github.ssenerg.gochat.core.state

import gochat.v1.Attachment
import gochat.v1.Channel
import gochat.v1.Event
import gochat.v1.Message
import gochat.v1.Reaction
import gochat.v1.Status
import gochat.v1.Team
import gochat.v1.User
import gochat.v1.VoiceChannelState
import io.github.ssenerg.gochat.core.api.ApiException
import io.github.ssenerg.gochat.core.api.GoChatApi
import io.github.ssenerg.gochat.core.api.userMessage
import io.github.ssenerg.gochat.core.gateway.Connection
import io.github.ssenerg.gochat.core.gateway.Gateway
import io.github.ssenerg.gochat.core.gateway.GatewayMessage
import io.github.ssenerg.gochat.core.model.displayName
import io.github.ssenerg.gochat.core.model.excerpt
import io.github.ssenerg.gochat.core.model.isArchived
import io.github.ssenerg.gochat.core.model.isConversation
import io.github.ssenerg.gochat.core.model.isVoice
import io.github.ssenerg.gochat.core.model.mentions
import io.github.ssenerg.gochat.core.model.newer
import io.github.ssenerg.gochat.core.model.title
import io.github.ssenerg.gochat.core.model.uuidTime
import io.github.ssenerg.gochat.core.voice.RtcEngine
import io.github.ssenerg.gochat.core.voice.VoiceCall
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlin.time.TimeSource

/** What the sidebar and the rest of the app show of a signed-in person's world. */
data class ChatState(
    val me: User,
    val teams: List<Team> = emptyList(),
    val teamId: String? = null,
    /** The team's channels the person belongs to, its voice channels and every conversation. */
    val channels: List<Channel> = emptyList(),
    val muted: Set<String> = emptySet(),
    val saved: Set<String> = emptySet(),
    /** The people not offline, and how available they are. */
    val presence: Map<String, Status> = emptyMap(),
    /** Who talks in each voice channel, by channel id. */
    val voice: Map<String, VoiceChannelState> = emptyMap(),
    val threadsUnread: Int = 0,
    val loading: Boolean = true,
    val error: String? = null,
    val connection: Connection = Connection.Connecting,
) {
    val team: Team? get() = teams.firstOrNull { it.id == teamId }

    val textChannels: List<Channel>
        get() = channels.filter { !it.isConversation && !it.isVoice && !it.isArchived }.sortedBy { it.display_name.lowercase() }

    val voiceChannels: List<Channel>
        get() = channels.filter { it.isVoice && !it.isArchived }.sortedBy { it.display_name.lowercase() }

    /** Direct and group conversations, the latest first. */
    val conversations: List<Channel>
        get() = channels.filter { it.isConversation }.sortedByDescending { uuidTime(it.last_message_id) }

    fun channel(id: String): Channel? = channels.firstOrNull { it.id == id }

    fun statusOf(userId: String): Status = presence[userId] ?: Status.STATUS_OFFLINE
}

/** A message elsewhere that calls for the person: a mention, a direct message, a reply. */
data class Notice(val title: String, val body: String, val channelId: String, val rootId: String = "")

/**
 * A signed-in person's session: it loads their teams and channels, keeps
 * them current with the event socket's events, and makes the calls the
 * screens need. Use it from one thread, the main one.
 */
class ChatSession internal constructor(
    val api: GoChatApi,
    me: User,
    private val store: KeyValueStore,
    parent: CoroutineScope,
    rtc: RtcEngine?,
    private val onSignedOut: (String) -> Unit,
) {
    private val scope = CoroutineScope(parent.coroutineContext + SupervisorJob(parent.coroutineContext[Job]))
    private val _state = MutableStateFlow(ChatState(me = me))
    val state: StateFlow<ChatState> = _state.asStateFlow()

    val gateway = Gateway(api.client, scope)

    /** The voice channel the person talks in, if any. */
    val voice = VoiceCall(api, rtc, store, me.id, scope)

    private val _notices = MutableSharedFlow<Notice>(extraBufferCapacity = 16)
    val notices: SharedFlow<Notice> = _notices.asSharedFlow()

    private val timelines = LinkedHashMap<String, Timeline>()
    private val threads = LinkedHashMap<String, ThreadView>()

    /** The channel and thread on screen, which read what arrives. */
    private var visibleChannel: String? = null
    private var visibleThread: String? = null
    private var lastTyping = mutableMapOf<String, TimeSource.Monotonic.ValueTimeMark>()

    val me: User get() = _state.value.me

    internal fun start() {
        scope.launch { gateway.messages.collect { handle(it) } }
        scope.launch {
            gateway.connection.collect { c ->
                _state.update { it.copy(connection = c) }
                if (c is Connection.SignedOut) onSignedOut("Your session ended. Sign in again.")
            }
        }
        scope.launch {
            loadTeams()
            gateway.start()
        }
        scope.launch {
            runCatching { api.savedIds() }.onSuccess { ids -> _state.update { it.copy(saved = ids.toSet()) } }
        }
    }

    internal fun stop() {
        voice.close()
        gateway.stop()
        scope.cancel()
    }

    // Loading.

    private suspend fun loadTeams() {
        try {
            val teams = api.listTeams()
            val saved = store.get(KEY_TEAM)
            val teamId = teams.firstOrNull { it.id == saved }?.id ?: teams.firstOrNull()?.id
            _state.update { it.copy(teams = teams, teamId = teamId, error = null) }
            if (teamId != null) loadSidebar() else _state.update { it.copy(loading = false) }
        } catch (e: Exception) {
            fail(e)
        }
    }

    /** Opens another team. */
    fun selectTeam(teamId: String) {
        if (teamId == _state.value.teamId) return
        store.put(KEY_TEAM, teamId)
        _state.update { it.copy(teamId = teamId, channels = emptyList(), voice = emptyMap(), loading = true) }
        scope.launch { loadSidebar() }
    }

    /** Loads the team's channels, mutes, voice states and the peers' presence again. */
    suspend fun loadSidebar() {
        val teamId = _state.value.teamId ?: return
        try {
            val channels = api.listChannels(teamId)
            val muted = api.mutedChannels().toSet()
            if (teamId != _state.value.teamId) return
            _state.update { it.copy(channels = channels, muted = muted, loading = false, error = null) }
            val peers = channels.flatMap { ch -> listOfNotNull(ch.peer?.id) + ch.peers.map { it.id } }.distinct()
            if (peers.isNotEmpty()) {
                val p = api.presence(peers)
                val online = p.online_user_ids.toSet()
                _state.update { st ->
                    st.copy(presence = st.presence + peers.associateWith { id -> statusFor(id in online, p.statuses[id]) })
                }
            }
            runCatching { api.voiceStates(teamId) }.onSuccess { list ->
                _state.update { st -> st.copy(voice = list.associateBy { it.channel_id }) }
            }
            runCatching { api.threads(teamId, limit = 0) }.onSuccess { t ->
                _state.update { it.copy(threadsUnread = t.unread_count) }
            }
        } catch (e: Exception) {
            fail(e)
        }
    }

    private fun statusFor(online: Boolean, status: Status?): Status = when {
        !online -> Status.STATUS_OFFLINE
        status == null || status == Status.STATUS_UNSPECIFIED -> Status.STATUS_ONLINE
        else -> status
    }

    private fun fail(e: Exception) {
        if (e is ApiException && e.isUnauthenticated) {
            onSignedOut("Your session ended. Sign in again.")
            return
        }
        _state.update { it.copy(loading = false, error = e.userMessage()) }
    }

    /** Loads everything again, after a lost connection. */
    fun refresh() {
        scope.launch {
            loadTeams()
            timelines.values.forEach { it.reload() }
            threads.values.forEach { it.reload() }
        }
    }

    // Screens.

    /** The timeline of a channel, kept while a few are open. */
    fun timeline(channelId: String): Timeline = timelines.getOrPut(channelId) {
        Timeline(channelId, this, scope).also { it.reload() }
    }.also { trim(timelines) }

    /** A thread's root and replies. */
    fun thread(channelId: String, rootId: String): ThreadView = threads.getOrPut(rootId) {
        ThreadView(channelId, rootId, this, scope).also { it.reload() }
    }.also { trim(threads) }

    private fun <T> trim(map: LinkedHashMap<String, T>) {
        while (map.size > KEPT_VIEWS) map.remove(map.keys.first())
    }

    /** Tells the session which channel, and thread, the person looks at; null when none. */
    fun setVisible(channelId: String?, rootId: String? = null) {
        visibleChannel = channelId
        visibleThread = rootId
        if (channelId != null) markRead(channelId)
        if (channelId != null && rootId != null) {
            scope.launch { runCatching { api.markThreadRead(channelId, rootId) } }
        }
    }

    /** Marks a channel read up to its newest message. */
    fun markRead(channelId: String) {
        val ch = _state.value.channel(channelId) ?: return
        val newest = timelines[channelId]?.state?.value?.messages?.firstOrNull()?.id ?: ch.last_message_id
        if (newest.isEmpty() || (ch.mention_count == 0 && !newer(newest, ch.last_read_id))) return
        updateChannel(channelId) { it.copy(last_read_id = newest, mention_count = 0) }
        scope.launch { runCatching { api.markRead(channelId, newest) } }
    }

    private fun updateChannel(id: String, f: (Channel) -> Channel) {
        _state.update { st -> st.copy(channels = st.channels.map { if (it.id == id) f(it) else it }) }
    }

    // Actions.

    /** Tells the channel's members the person types, at most every few seconds. */
    fun typing(channelId: String) {
        val now = TimeSource.Monotonic.markNow()
        val last = lastTyping[channelId]
        if (last != null && last.elapsedNow().inWholeMilliseconds < TYPING_EVERY_MS) return
        lastTyping[channelId] = now
        scope.launch { runCatching { api.typing(channelId) } }
    }

    suspend fun upload(channelId: String, name: String, bytes: ByteArray, voice: Boolean = false, durationMs: Long = 0): Attachment =
        api.client.upload(channelId, name, bytes, voice, durationMs)

    /** Adds the person's reaction, or takes it back when they had reacted so. */
    fun react(m: Message, emoji: String) {
        val mine = m.reactions.any { it.emoji == emoji && me.id in it.user_ids }
        scope.launch {
            runCatching { api.react(m, emoji, remove = mine) }.onSuccess { applyReactions(m.channel_id, m.id, m.root_id, it) }
        }
    }

    suspend fun edit(m: Message, content: String): String? = act { apply(api.edit(m, content)) }

    suspend fun delete(m: Message): String? = act {
        api.delete(m)
        removeMessage(m.channel_id, m.id, m.root_id)
    }

    suspend fun pin(m: Message, pinned: Boolean): String? = act { apply(api.pin(m, pinned)) }

    suspend fun save(m: Message, saved: Boolean): String? = act {
        api.save(m, saved)?.let { apply(it) }
        _state.update { st -> st.copy(saved = if (saved) st.saved + m.id else st.saved - m.id) }
    }

    suspend fun setMuted(channelId: String, muted: Boolean): String? = act {
        api.setMuted(channelId, muted)
        _state.update { st -> st.copy(muted = if (muted) st.muted + channelId else st.muted - channelId) }
    }

    suspend fun follow(channelId: String, rootId: String, follow: Boolean): String? = act {
        api.follow(channelId, rootId, follow)
        threads[rootId]?.setFollowing(follow)
    }

    /** Opens, or creates, the conversation with [userIds]. */
    suspend fun openConversation(userIds: List<String>): Channel {
        val ch = if (userIds.size == 1) api.openDirect(userIds[0]) else api.openGroup(userIds)
        if (ch != null) addChannel(ch)
        return ch ?: throw ApiException(0, "The server opened no conversation.")
    }

    suspend fun joinChannel(id: String): Channel? = api.joinChannel(id)?.also { addChannel(it) }

    /** Takes someone out of a voice channel's talk, for its moderators. */
    suspend fun disconnectVoice(channelId: String, userId: String): String? = act { api.disconnectVoiceMember(channelId, userId) }

    suspend fun leaveChannel(id: String): String? = act {
        api.leaveChannel(id)
        _state.update { st -> st.copy(channels = st.channels.filter { it.id != id }) }
    }

    suspend fun createChannel(req: gochat.v1.CreateChannelRequest): Channel? = api.createChannel(req)?.also { addChannel(it) }

    suspend fun setStatus(status: Status, dndMinutes: Int = 0): String? = act {
        api.setStatus(status, dndMinutes)?.let { u -> _state.update { it.copy(me = u) } }
    }

    suspend fun setCustomStatus(emoji: String, text: String, minutes: Int): String? = act {
        api.setCustomStatus(emoji, text, minutes)?.let { u -> _state.update { it.copy(me = u) } }
    }

    suspend fun updateProfile(displayName: String): String? = act {
        api.updateProfile(displayName)?.let { u -> _state.update { it.copy(me = u) } }
    }

    suspend fun setPhoto(picture: ByteArray): String? = act {
        val u = api.client.setPhoto(picture)
        _state.update { it.copy(me = u) }
    }

    suspend fun removePhoto(): String? = act {
        api.removePhoto()?.let { u -> _state.update { it.copy(me = u) } }
    }

    private suspend fun act(block: suspend () -> Unit): String? = try {
        block()
        null
    } catch (e: Exception) {
        if (e is ApiException && e.isUnauthenticated) onSignedOut("Your session ended. Sign in again.")
        e.userMessage()
    }

    private fun addChannel(ch: Channel) {
        _state.update { st -> st.copy(channels = st.channels.filter { it.id != ch.id } + ch) }
    }

    // Events.

    private fun handle(msg: GatewayMessage) {
        when (msg) {
            is GatewayMessage.Ready -> {
                _state.update { it.copy(me = msg.ready.user ?: it.me) }
                if (msg.resync) refresh()
            }
            is GatewayMessage.Received -> event(msg.event)
        }
    }

    internal fun event(ev: Event) {
        val st = _state.value
        ev.message_posted?.let { p -> p.message?.let { posted(it, p.participant_ids) } }
        ev.message_edited?.message?.let { apply(it) }
        ev.message_deleted?.let { removeMessage(it.channel_id, it.message_id, it.root_id) }
        ev.typing?.let { t ->
            val u = t.user ?: return@let
            if (u.id != st.me.id) timelines[t.channel_id]?.typed(u)
        }
        ev.reactions_changed?.let { applyReactions(it.channel_id, it.message_id, it.root_id, it.reactions) }
        ev.channel_joined?.channel?.let { ch ->
            if (ch.isConversation || ch.team_id == st.teamId) addChannel(ch)
        }
        ev.channel_left?.let { left ->
            _state.update { s -> s.copy(channels = s.channels.filter { it.id != left.channel_id }) }
            timelines[left.channel_id]?.gone(left.own)
        }
        ev.channel_updated?.channel?.let { ch ->
            if (st.channel(ch.id) != null) {
                // The event is built for the person who changed the channel:
                // keep what is the reader's own.
                updateChannel(ch.id) { old ->
                    old.copy(
                        name = ch.name, display_name = ch.display_name, purpose = ch.purpose, type = ch.type,
                        archived_at = ch.archived_at, voice_enabled = ch.voice_enabled, pinned_count = ch.pinned_count,
                    )
                }
                timelines[ch.id]?.refreshChannel()
            } else if (ch.team_id == st.teamId && !ch.isArchived) {
                scope.launch { loadSidebar() }
            }
        }
        ev.channel_muted?.let { mu ->
            _state.update { s -> s.copy(muted = if (mu.muted) s.muted + mu.channel_id else s.muted - mu.channel_id) }
        }
        ev.saved_changed?.let { sv ->
            _state.update { s -> s.copy(saved = if (sv.saved) s.saved + sv.message_id else s.saved - sv.message_id) }
            timelines[sv.channel_id]?.update(sv.message_id) { it.copy(saved = sv.saved) }
            threads.values.forEach { t -> t.update(sv.message_id) { it.copy(saved = sv.saved) } }
        }
        ev.pins_changed?.let { p -> updateChannel(p.channel_id) { it.copy(pinned_count = p.count) } }
        ev.presence?.let { p ->
            _state.update { s ->
                val status = statusFor(p.online, p.status)
                val me = if (p.user_id == s.me.id) s.me.copy(status = if (p.online) p.status else s.me.status) else s.me
                s.copy(presence = s.presence + (p.user_id to status), me = me)
            }
        }
        ev.profile_changed?.user?.let { u ->
            _state.update { s ->
                s.copy(
                    me = if (u.id == s.me.id) u else s.me,
                    channels = s.channels.map { ch ->
                        when {
                            ch.peer?.id == u.id -> ch.copy(peer = u)
                            ch.peers.any { it.id == u.id } -> ch.copy(peers = ch.peers.map { if (it.id == u.id) u else it })
                            else -> ch
                        }
                    },
                )
            }
        }
        ev.team_joined?.team?.let { t ->
            _state.update { s -> s.copy(teams = s.teams.filter { it.id != t.id } + t) }
            if (st.teamId == null) selectTeam(t.id)
        }
        ev.team_left?.let { left ->
            _state.update { s -> s.copy(teams = s.teams.filter { it.id != left.team_id }) }
            if (st.teamId == left.team_id) {
                val next = _state.value.teams.firstOrNull()?.id
                _state.update { it.copy(teamId = null, channels = emptyList()) }
                if (next != null) selectTeam(next)
            }
        }
        ev.voice_channel_update?.let { up ->
            val ch = up.channel ?: return@let
            if (up.team_id != st.teamId) return@let
            _state.update { s ->
                val old = s.voice[ch.channel_id]
                if (old != null && old.version >= ch.version) s else s.copy(voice = s.voice + (ch.channel_id to ch))
            }
        }
    }

    private fun posted(m: Message, participants: List<String>) {
        val st = _state.value
        val mine = m.author?.id == st.me.id
        val ch = st.channel(m.channel_id)
        if (m.root_id.isNotEmpty()) {
            threads[m.root_id]?.added(m)
            timelines[m.channel_id]?.replied(m)
            val looking = visibleThread == m.root_id
            if (!mine && st.me.id in participants && !looking) {
                _state.update { it.copy(threadsUnread = it.threadsUnread + 1) }
                notify(m, ch, m.root_id)
            } else if (!mine && !looking && m.mentions(st.me)) {
                notify(m, ch, m.root_id)
            }
            if (looking && !mine) scope.launch { runCatching { api.markThreadRead(m.channel_id, m.root_id) } }
            return
        }
        timelines[m.channel_id]?.added(m)
        if (ch == null) {
            // A conversation someone else just opened.
            scope.launch { loadSidebar() }
            return
        }
        val looking = visibleChannel == m.channel_id
        updateChannel(m.channel_id) { old ->
            var c = if (newer(m.id, old.last_message_id)) old.copy(last_message_id = m.id) else old
            if (mine || looking) {
                c = c.copy(last_read_id = m.id)
            } else if (ch.isConversation || m.mentions(st.me)) {
                c = c.copy(mention_count = c.mention_count + 1)
            }
            c
        }
        if (looking && !mine) scope.launch { runCatching { api.markRead(m.channel_id, m.id) } }
        if (!mine && !looking && m.channel_id !in st.muted && (ch.isConversation || m.mentions(st.me))) notify(m, ch, "")
    }

    private fun notify(m: Message, ch: Channel?, rootId: String) {
        if (_state.value.me.status == Status.STATUS_DND) return
        val where = ch?.let { if (it.isConversation) "" else " in #${it.title()}" } ?: ""
        val title = if (rootId.isNotEmpty()) "${m.author.displayName()} replied$where" else "${m.author.displayName()}$where"
        _notices.tryEmit(Notice(title, m.excerpt(), m.channel_id, rootId))
    }

    private fun apply(m: Message?) {
        if (m == null) return
        timelines[m.channel_id]?.update(m.id) { m }
        threads.values.forEach { t -> t.update(m.id) { m } }
    }

    private fun removeMessage(channelId: String, id: String, rootId: String) {
        timelines[channelId]?.removed(id, rootId)
        threads[rootId]?.removed(id)
        threads[id]?.rootGone()
    }

    private fun applyReactions(channelId: String, id: String, rootId: String, reactions: List<Reaction>) {
        timelines[channelId]?.update(id) { it.copy(reactions = reactions) }
        (threads[rootId] ?: threads[id])?.update(id) { it.copy(reactions = reactions) }
    }

    companion object {
        private const val KEY_TEAM = "team"
        private const val KEPT_VIEWS = 8
        private const val TYPING_EVERY_MS = 3_000L
    }
}

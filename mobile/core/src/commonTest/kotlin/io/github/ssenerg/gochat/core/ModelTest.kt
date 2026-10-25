package io.github.ssenerg.gochat.core

import gochat.v1.Channel
import gochat.v1.ChannelType
import gochat.v1.Message
import gochat.v1.User
import io.github.ssenerg.gochat.core.api.normalizeServerUrl
import io.github.ssenerg.gochat.core.model.Emojis
import io.github.ssenerg.gochat.core.model.displayName
import io.github.ssenerg.gochat.core.model.initials
import io.github.ssenerg.gochat.core.model.isUnread
import io.github.ssenerg.gochat.core.model.mentions
import io.github.ssenerg.gochat.core.model.newer
import io.github.ssenerg.gochat.core.model.parseMentions
import io.github.ssenerg.gochat.core.model.sentAtMillis
import io.github.ssenerg.gochat.core.model.title
import io.github.ssenerg.gochat.core.state.GoChatApp
import io.github.ssenerg.gochat.core.state.VoiceCache
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

class ModelTest {
    // Time UUIDs a second apart: the low time fields come first.
    private val older = "d0f1c000-b1a2-11f0-8000-000000000001"
    private val later = "d18a5680-b1a2-11f0-8000-000000000001"

    @Test
    fun timeUuidsOrderByTime() {
        assertTrue(newer(later, older))
        assertFalse(newer(older, later))
        assertTrue(newer(older, ""))
    }

    @Test
    fun unreadFollowsTheLastRead() {
        assertFalse(Channel().isUnread())
        assertTrue(Channel(last_message_id = later).isUnread())
        assertTrue(Channel(last_message_id = later, last_read_id = older).isUnread())
        assertFalse(Channel(last_message_id = older, last_read_id = later).isUnread())
    }

    @Test
    fun sentAtComesFromTheIdWithoutATimestamp() {
        // 0x1f0b1a2d0f1c000 in 100ns since 1582 is 2025-10-26.
        val ms = Message(id = older).sentAtMillis()
        assertTrue(ms in 1_761_000_000_000..1_762_000_000_000, "got $ms")
    }

    @Test
    fun mentionsAreFoundOutsideCode() {
        val m = parseMentions("hi @Bob and @alice. also `@carol` and @here\n```\n@dave\n```")
        assertEquals(listOf("bob", "alice"), m.usernames)
        assertTrue(m.here)
        assertFalse(m.channel)
        assertTrue(parseMentions("ping @all").channel)
        assertEquals(emptyList(), parseMentions("mail me@example.com").usernames)
        val me = User(id = "1", username = "bob")
        assertTrue(Message(content = "@bob look").mentions(me))
        assertFalse(Message(content = "@bobby look").mentions(me))
    }

    @Test
    fun namesAndTitles() {
        assertEquals("bob", User(username = "bob").displayName())
        assertEquals("Bob B", User(username = "bob", display_name = "Bob B").displayName())
        assertEquals("BB", User(display_name = "Bob Builder").initials())
        val dm = Channel(type = ChannelType.CHANNEL_TYPE_DIRECT, peer = User(username = "ann"))
        assertEquals("ann", dm.title())
        val group = Channel(type = ChannelType.CHANNEL_TYPE_GROUP, peers = listOf(User(username = "a"), User(display_name = "B")))
        assertEquals("a, B", group.title())
    }

    @Test
    fun emojiShortcodes() {
        assertEquals("ship it 🚀!", Emojis.replaceShortcodes("ship it :rocket:!"))
        assertEquals(":nope:", Emojis.replaceShortcodes(":nope:"))
        assertEquals("👍", Emojis.char("+1"))
    }

    @Test
    fun serverAddressesAndInvites() {
        assertEquals("https://chat.example.com", normalizeServerUrl(" chat.example.com/ "))
        assertEquals("http://localhost:8080", normalizeServerUrl("http://localhost:8080"))
        val token = "AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abcd"
        assertEquals(token, GoChatApp.inviteToken("https://chat.example.com/join/$token"))
        assertEquals(token, GoChatApp.inviteToken(token))
    }

    @Test
    fun theVoiceCacheKeepsWhatPlayedLast() {
        val cache = VoiceCache(maxBytes = 10, maxEntries = 2)
        cache["a"] = ByteArray(4)
        cache["b"] = ByteArray(4)
        // Played again: b is now the oldest, and three are one too many.
        assertNotNull(cache["a"])
        cache["c"] = ByteArray(4)
        assertNull(cache["b"])
        // Too many bytes: the older ones go.
        cache["d"] = ByteArray(8)
        assertEquals(1, cache.size)
        // Larger than the whole cache: not kept.
        cache["e"] = ByteArray(11)
        assertNull(cache["e"])
        cache.clear()
        assertEquals(0, cache.size)
    }
}

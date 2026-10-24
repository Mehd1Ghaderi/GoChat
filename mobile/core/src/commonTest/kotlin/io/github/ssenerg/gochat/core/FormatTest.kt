package io.github.ssenerg.gochat.core

import gochat.v1.Message
import gochat.v1.User
import io.github.ssenerg.gochat.core.model.Format
import io.github.ssenerg.gochat.core.model.Markup
import io.github.ssenerg.gochat.core.model.Markup.Block
import io.github.ssenerg.gochat.core.model.Markup.Inline
import kotlinx.datetime.TimeZone
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class FormatTest {
    private val utc = TimeZone.UTC

    // 2026-10-24T14:05:00Z, a Saturday.
    private val now = 1_792_850_700_000L
    private val day = 86_400_000L

    @Test
    fun clocksAndDays() {
        assertEquals("14:05", Format.clock(now, utc))
        assertEquals("Today", Format.day(now - 60_000, now, utc))
        assertEquals("Yesterday", Format.day(now - day, now, utc))
        assertEquals("Wednesday", Format.day(now - 3 * day, now, utc))
        assertEquals("Monday, October 12", Format.day(now - 12 * day, now, utc))
        assertEquals("October 24, 2025", Format.day(now - 365 * day, now, utc))
        assertEquals("14:05", Format.short(now, now, utc))
        assertEquals("Oct 12", Format.short(now - 12 * day, now, utc))
    }

    @Test
    fun durationsAndSizes() {
        assertEquals("0:00", Format.duration(0))
        assertEquals("0:42", Format.duration(41_600))
        assertEquals("12:05", Format.duration(725_000))
        assertEquals("1:02:05", Format.duration(3_725_000))
        assertEquals("812 B", Format.size(812))
        assertEquals("4.2 KB", Format.size(4_300))
        assertEquals("1.3 MB", Format.size(1_363_149))
        assertEquals("153 MB", Format.size(160_000_000))
    }

    @Test
    fun messagesGroupByAuthorAndTime() {
        val ann = User(id = "a")
        val first = Message(id = "1", author = ann, created_at = com.squareup.wire.ofEpochSecond(now / 1000, 0))
        val soon = Message(id = "2", author = ann, created_at = com.squareup.wire.ofEpochSecond(now / 1000 + 120, 0))
        val late = Message(id = "3", author = ann, created_at = com.squareup.wire.ofEpochSecond(now / 1000 + 3600, 0))
        val other = Message(id = "4", author = User(id = "b"), created_at = com.squareup.wire.ofEpochSecond(now / 1000 + 130, 0))
        assertTrue(Format.continues(soon, first, utc))
        assertFalse(Format.continues(late, first, utc))
        assertFalse(Format.continues(other, soon, utc))
        assertFalse(Format.continues(first, null, utc))
    }

    @Test
    fun inlineMarkdown() {
        assertEquals(
            listOf(Inline.Text("a "), Inline.Bold(listOf(Inline.Text("bold"))), Inline.Text(" and "), Inline.Italic(listOf(Inline.Text("it"))), Inline.Text(" "), Inline.Code("x*y*")),
            Markup.inlines("a **bold** and _it_ `x*y*`"),
        )
        assertEquals(listOf(Inline.Text("snake_case_name stays")), Markup.inlines("snake_case_name stays"))
        assertEquals(listOf(Inline.Strike(listOf(Inline.Text("gone")))), Markup.inlines("~~gone~~"))
        assertEquals(listOf(Inline.Text("hi "), Inline.Mention("bob"), Inline.Text(". 🎉")), Markup.inlines("hi @Bob. :tada:"))
        assertEquals(listOf(Inline.Text("mail me@example.com")), Markup.inlines("mail me@example.com"))
    }

    @Test
    fun linksAreSafe() {
        val see = Markup.inlines("see https://example.com/a?b=1, or [docs](https://docs.example.com).")
        assertEquals(Inline.Link("https://example.com/a?b=1", listOf(Inline.Text("https://example.com/a?b=1"))), see[1])
        assertEquals(Inline.Link("https://docs.example.com", listOf(Inline.Text("docs"))), see[3])
        // Only web and mail links: anything else stays text.
        assertEquals(listOf(Inline.Text("[x](javascript:alert(1))")), Markup.inlines("[x](javascript:alert(1))"))
    }

    @Test
    fun blocks() {
        val blocks = Markup.parse("hello\nworld\n\n> quoted **text**\n- one\n2. two\n```\ncode *raw*\n```\n# Big")
        assertEquals(
            listOf(
                Block.Paragraph(listOf(Inline.Text("hello\nworld"))),
                Block.Quote(listOf(Inline.Text("quoted "), Inline.Bold(listOf(Inline.Text("text"))))),
                Block.Item("•", listOf(Inline.Text("one"))),
                Block.Item("2.", listOf(Inline.Text("two"))),
                Block.Code("code *raw*"),
                Block.Paragraph(listOf(Inline.Bold(listOf(Inline.Text("Big"))))),
            ),
            blocks,
        )
        assertEquals("a @b c", Markup.plain(Markup.inlines("a @b *c*")))
    }
}

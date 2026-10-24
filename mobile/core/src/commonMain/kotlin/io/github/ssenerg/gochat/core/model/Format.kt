package io.github.ssenerg.gochat.core.model

import gochat.v1.Message
import kotlinx.datetime.LocalDate
import kotlinx.datetime.TimeZone
import kotlinx.datetime.toLocalDateTime
import kotlin.time.Instant

/** How times, lengths and sizes read in the apps. */
object Format {
    private val months = listOf(
        "January", "February", "March", "April", "May", "June",
        "July", "August", "September", "October", "November", "December",
    )
    private val weekdays = listOf("Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday")

    private fun date(ms: Long, tz: TimeZone): LocalDate = Instant.fromEpochMilliseconds(ms).toLocalDateTime(tz).date

    /** The time of day, as 14:05. */
    fun clock(ms: Long, tz: TimeZone = TimeZone.currentSystemDefault()): String {
        val t = Instant.fromEpochMilliseconds(ms).toLocalDateTime(tz)
        return "${t.hour.toString().padStart(2, '0')}:${t.minute.toString().padStart(2, '0')}"
    }

    fun sameDay(a: Long, b: Long, tz: TimeZone = TimeZone.currentSystemDefault()) = date(a, tz) == date(b, tz)

    /** A day as the separators between messages name it: Today, Yesterday, Monday, October 21... */
    fun day(ms: Long, nowMs: Long, tz: TimeZone = TimeZone.currentSystemDefault()): String {
        val d = date(ms, tz)
        val today = date(nowMs, tz)
        val days = today.toEpochDays() - d.toEpochDays()
        val name = "${months[d.month.ordinal]} ${d.day}"
        return when {
            days == 0L -> "Today"
            days == 1L -> "Yesterday"
            days in 2L..6L -> weekdays[d.dayOfWeek.ordinal]
            d.year == today.year -> "${weekdays[d.dayOfWeek.ordinal]}, $name"
            else -> "$name, ${d.year}"
        }
    }

    /** A short time for lists: the time today, else Yesterday or the date. */
    fun short(ms: Long, nowMs: Long, tz: TimeZone = TimeZone.currentSystemDefault()): String {
        val d = date(ms, tz)
        val today = date(nowMs, tz)
        val days = today.toEpochDays() - d.toEpochDays()
        val name = "${months[d.month.ordinal].take(3)} ${d.day}"
        return when {
            days == 0L -> clock(ms, tz)
            days == 1L -> "Yesterday"
            d.year == today.year -> name
            else -> "$name, ${d.year}"
        }
    }

    /** A recording's length, as 0:42 or 1:02:05. */
    fun duration(ms: Long): String {
        val total = (ms.coerceAtLeast(0) + 500) / 1000
        val h = total / 3600
        val m = total % 3600 / 60
        val s = (total % 60).toString().padStart(2, '0')
        return if (h > 0) "$h:${m.toString().padStart(2, '0')}:$s" else "$m:$s"
    }

    /** A file's size, as 812 B, 4.2 KB or 1.3 MB. */
    fun size(bytes: Long): String {
        if (bytes < 1024) return "$bytes B"
        val units = listOf("KB", "MB", "GB")
        var v = bytes / 1024.0
        var i = 0
        while (v >= 1024 && i < units.lastIndex) {
            v /= 1024
            i++
        }
        val tenths = (v * 10 + 0.5).toLong()
        return if (tenths >= 100) "${(v + 0.5).toLong()} ${units[i]}" else "${tenths / 10}.${tenths % 10} ${units[i]}"
    }

    private const val GROUP_MS = 5 * 60_000L

    /**
     * Whether [m] shows under the author line of [previous], the message just
     * before it: the same author, the same day, within a few minutes.
     */
    fun continues(m: Message, previous: Message?, tz: TimeZone = TimeZone.currentSystemDefault()): Boolean {
        if (previous == null || previous.author?.id != m.author?.id) return false
        val a = previous.sentAtMillis()
        val b = m.sentAtMillis()
        return b - a in 0..GROUP_MS && sameDay(a, b, tz)
    }
}

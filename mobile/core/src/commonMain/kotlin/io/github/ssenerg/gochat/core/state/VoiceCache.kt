package io.github.ssenerg.gochat.core.state

/**
 * The voice messages played last, kept to replay without fetching them
 * again: at most [maxEntries] of them and [maxBytes] in all, the least
 * recently played going first.
 */
class VoiceCache(private val maxBytes: Long = 16L * 1024 * 1024, private val maxEntries: Int = 32) {
    // Kept in the order they were played, the oldest first.
    private val entries = LinkedHashMap<String, ByteArray>()
    private var bytes = 0L

    val size: Int get() = entries.size

    operator fun get(id: String): ByteArray? = entries.remove(id)?.also { entries[id] = it }

    operator fun set(id: String, data: ByteArray) {
        entries.remove(id)?.let { bytes -= it.size }
        if (data.size > maxBytes) return
        entries[id] = data
        bytes += data.size
        val oldest = entries.entries.iterator()
        while ((bytes > maxBytes || entries.size > maxEntries) && oldest.hasNext()) {
            bytes -= oldest.next().value.size
            oldest.remove()
        }
    }

    fun clear() {
        entries.clear()
        bytes = 0
    }
}

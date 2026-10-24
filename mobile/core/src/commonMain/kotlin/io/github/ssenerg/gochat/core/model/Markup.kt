package io.github.ssenerg.gochat.core.model

/**
 * The Markdown of messages, as far as the apps show it: the same subset the
 * web renders that matters in a chat (emphasis, code, links, quotes, lists,
 * mentions and :emoji:), without raw HTML or images.
 */
object Markup {
    sealed interface Inline {
        data class Text(val text: String) : Inline

        data class Bold(val children: List<Inline>) : Inline

        data class Italic(val children: List<Inline>) : Inline

        data class Strike(val children: List<Inline>) : Inline

        data class Code(val text: String) : Inline

        /** A link to an http(s) or mailto address. */
        data class Link(val url: String, val children: List<Inline>) : Inline

        /** An @name, lowercase, without the @. */
        data class Mention(val name: String) : Inline
    }

    sealed interface Block {
        data class Paragraph(val inlines: List<Inline>) : Block

        data class Code(val text: String) : Block

        data class Quote(val inlines: List<Inline>) : Block

        /** A list item; [marker] is "•" or the item's number. */
        data class Item(val marker: String, val inlines: List<Inline>) : Block
    }

    private val fence = Regex("^\\s*(```|~~~)")
    private val bullet = Regex("^\\s*[-*+]\\s+(.*)$")
    private val numbered = Regex("^\\s*(\\d{1,3})[.)]\\s+(.*)$")
    private val heading = Regex("^\\s{0,3}#{1,6}\\s+(.*)$")

    /** Splits a message into blocks. */
    fun parse(text: String): List<Block> {
        val out = mutableListOf<Block>()
        val para = mutableListOf<String>()
        fun flush() {
            if (para.isNotEmpty()) {
                out += Block.Paragraph(inlines(para.joinToString("\n")))
                para.clear()
            }
        }
        val lines = text.replace("\r\n", "\n").split('\n')
        var i = 0
        while (i < lines.size) {
            val line = lines[i]
            val f = fence.find(line)
            when {
                f != null -> {
                    flush()
                    val marker = f.groupValues[1]
                    val code = mutableListOf<String>()
                    i++
                    while (i < lines.size && !lines[i].trimStart().startsWith(marker)) code += lines[i++]
                    out += Block.Code(code.joinToString("\n"))
                }
                line.trimStart().startsWith(">") -> {
                    flush()
                    val quote = mutableListOf<String>()
                    while (i < lines.size && lines[i].trimStart().startsWith(">")) {
                        quote += lines[i].trimStart().removePrefix(">").removePrefix(" ")
                        i++
                    }
                    out += Block.Quote(inlines(quote.joinToString("\n")))
                    continue
                }
                bullet.matches(line) -> {
                    flush()
                    out += Block.Item("•", inlines(bullet.find(line)!!.groupValues[1]))
                }
                numbered.matches(line) -> {
                    flush()
                    val m = numbered.find(line)!!
                    out += Block.Item(m.groupValues[1] + ".", inlines(m.groupValues[2]))
                }
                heading.matches(line) -> {
                    // Headings shout in a chat; they read as strong lines.
                    flush()
                    out += Block.Paragraph(listOf(Inline.Bold(inlines(heading.find(line)!!.groupValues[1]))))
                }
                line.isBlank() -> flush()
                else -> para += line
            }
            i++
        }
        flush()
        return out
    }

    private fun isNameChar(c: Char) = c in 'a'..'z' || c in 'A'..'Z' || c in '0'..'9' || c == '.' || c == '_' || c == '-'

    private fun isWordChar(c: Char) = c.isLetterOrDigit()

    private val urlEnd = setOf(' ', '\n', '\t', '<', '>', '"')

    /** Whether a link may go to [url]: the web's links open only these. */
    fun safeUrl(url: String): Boolean {
        val u = url.lowercase()
        return u.startsWith("https://") || u.startsWith("http://") || u.startsWith("mailto:")
    }

    /** Parses the inline Markdown of one block. */
    fun inlines(text: String): List<Inline> {
        val out = mutableListOf<Inline>()
        val buf = StringBuilder()
        fun flush() {
            if (buf.isNotEmpty()) {
                out += Inline.Text(Emojis.replaceShortcodes(buf.toString()))
                buf.clear()
            }
        }
        var i = 0
        while (i < text.length) {
            val c = text[i]
            val prev = if (i > 0) text[i - 1] else ' '
            when {
                c == '\\' && i + 1 < text.length && text[i + 1] in "\\`*_~[]()@:#>-" -> {
                    buf.append(text[i + 1])
                    i += 2
                    continue
                }
                c == '`' -> {
                    val end = text.indexOf('`', i + 1)
                    if (end > i + 1) {
                        flush()
                        out += Inline.Code(text.substring(i + 1, end))
                        i = end + 1
                        continue
                    }
                }
                (c == '*' || c == '_') && text.startsWith("$c$c", i) && !(c == '_' && isWordChar(prev)) -> {
                    val end = text.indexOf("$c$c", i + 2)
                    if (end > i + 2) {
                        flush()
                        out += Inline.Bold(inlines(text.substring(i + 2, end)))
                        i = end + 2
                        continue
                    }
                }
                (c == '*' || c == '_') && !(c == '_' && isWordChar(prev)) && i + 1 < text.length && !text[i + 1].isWhitespace() -> {
                    var end = text.indexOf(c, i + 1)
                    while (end > 0 && c == '_' && end + 1 < text.length && isWordChar(text[end + 1])) end = text.indexOf(c, end + 1)
                    if (end > i + 1 && !text[end - 1].isWhitespace()) {
                        flush()
                        out += Inline.Italic(inlines(text.substring(i + 1, end)))
                        i = end + 1
                        continue
                    }
                }
                c == '~' && text.startsWith("~~", i) -> {
                    val end = text.indexOf("~~", i + 2)
                    if (end > i + 2) {
                        flush()
                        out += Inline.Strike(inlines(text.substring(i + 2, end)))
                        i = end + 2
                        continue
                    }
                }
                c == '[' -> {
                    val close = text.indexOf("](", i + 1)
                    val end = if (close > 0) text.indexOf(')', close + 2) else -1
                    if (close > i && end > close + 2) {
                        val url = text.substring(close + 2, end).trim()
                        if (safeUrl(url)) {
                            flush()
                            out += Inline.Link(url, inlines(text.substring(i + 1, close)))
                            i = end + 1
                            continue
                        }
                    }
                }
                (c == 'h' || c == 'H') && !isWordChar(prev) && (text.startsWith("https://", i, true) || text.startsWith("http://", i, true)) -> {
                    var end = i
                    while (end < text.length && text[end] !in urlEnd) end++
                    // Trailing punctuation ends the sentence, not the link.
                    while (end > i && text[end - 1] in ".,;:!?)'\"") end--
                    flush()
                    val url = text.substring(i, end)
                    out += Inline.Link(url, listOf(Inline.Text(url)))
                    i = end
                    continue
                }
                c == '@' && !isNameChar(prev) && prev != '@' -> {
                    var end = i + 1
                    while (end < text.length && isNameChar(text[end])) end++
                    val name = text.substring(i + 1, end).trimEnd('.', '_', '-')
                    if (name.isNotEmpty() && name.length <= 32) {
                        flush()
                        out += Inline.Mention(name.lowercase())
                        i += 1 + name.length
                        continue
                    }
                }
            }
            buf.append(c)
            i++
        }
        flush()
        return out
    }

    /** The plain text of inlines, for copying and accessibility. */
    fun plain(inlines: List<Inline>): String = buildString {
        for (n in inlines) {
            when (n) {
                is Inline.Text -> append(n.text)
                is Inline.Bold -> append(plain(n.children))
                is Inline.Italic -> append(plain(n.children))
                is Inline.Strike -> append(plain(n.children))
                is Inline.Code -> append(n.text)
                is Inline.Link -> append(plain(n.children))
                is Inline.Mention -> append("@").append(n.name)
            }
        }
    }
}

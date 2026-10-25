package io.github.ssenerg.gochat.core.model

/** An emoji people react with and write as :name:. */
data class Emoji(val name: String, val char: String)

/** The server's emoji, in the order the picker shows them (internal/core/emoji). */
object Emojis {
    val all: List<Emoji> = listOf(
        "thumbsup" to "👍", "thumbsdown" to "👎", "heart" to "❤️", "joy" to "😂", "tada" to "🎉", "eyes" to "👀",
        "white_check_mark" to "✅", "pray" to "🙏", "fire" to "🔥", "rocket" to "🚀", "clap" to "👏", "raised_hands" to "🙌",
        "smile" to "😄", "laughing" to "😆", "rofl" to "🤣", "slightly_smiling_face" to "🙂", "wink" to "😉", "blush" to "😊",
        "heart_eyes" to "😍", "sunglasses" to "😎", "upside_down" to "🙃", "sweat_smile" to "😅", "relieved" to "😌", "nerd" to "🤓",
        "thinking" to "🤔", "neutral_face" to "😐", "confused" to "😕", "open_mouth" to "😮", "scream" to "😱", "cry" to "😢",
        "sob" to "😭", "angry" to "😠", "face_palm" to "🤦", "shrug" to "🤷", "sleepy" to "😪", "skull" to "💀",
        "muscle" to "💪", "ok_hand" to "👌", "wave" to "👋", "point_up" to "☝️", "handshake" to "🤝", "crossed_fingers" to "🤞",
        "100" to "💯", "sparkles" to "✨", "star" to "⭐", "zap" to "⚡", "bulb" to "💡", "dart" to "🎯",
        "trophy" to "🏆", "gift" to "🎁", "cake" to "🎂", "coffee" to "☕", "pizza" to "🍕", "beers" to "🍻",
        "memo" to "📝", "bug" to "🐛", "warning" to "⚠️", "x" to "❌", "question" to "❓", "exclamation" to "❗",
        "rainbow" to "🌈", "sunny" to "☀️", "crescent_moon" to "🌙", "robot" to "🤖", "ghost" to "👻", "see_no_evil" to "🙈",
        "speech_balloon" to "💬", "calendar" to "📅", "headphones" to "🎧", "house" to "🏠", "palm_tree" to "🌴", "face_with_thermometer" to "🤒",
    ).map { (name, char) -> Emoji(name, char) }

    private val byName: Map<String, String> =
        all.associate { it.name to it.char } + mapOf("+1" to "👍", "-1" to "👎")

    /** The emoji of [name], or null for a name the server does not know. */
    fun char(name: String): String? = byName[name]

    /** The quick reactions offered first. */
    val quick: List<Emoji> = all.take(6)

    private val shortcode = Regex(":([a-z0-9_+-]{1,32}):")

    /** Writes each :name: of a known emoji as the emoji, as the web does. */
    fun replaceShortcodes(text: String): String =
        if (':' !in text) text else shortcode.replace(text) { m -> char(m.groupValues[1]) ?: m.value }
}

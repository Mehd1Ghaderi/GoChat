package io.github.ssenerg.gochat.ui

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.graphics.Color
import gochat.v1.Status

/** The colors the web uses beyond Material's, for the same look. */
@Immutable
data class ChatColors(
    val sidebar: Color,
    val sidebarText: Color,
    val sidebarMuted: Color,
    val sidebarActive: Color,
    val rail: Color,
    val muted: Color,
    val online: Color,
    val away: Color,
    val dnd: Color,
    val unread: Color,
    val unreadInk: Color,
    val mention: Color,
    val mentionInk: Color,
    val link: Color,
    val codeBackground: Color,
)

private val LightChat = ChatColors(
    sidebar = Color(0xFFEEEDF8), sidebarText = Color(0xFF3B3E60), sidebarMuted = Color(0xFF5F6387),
    sidebarActive = Color(0xFFDCDFFC), rail = Color(0xFFE6E5F5), muted = Color(0xFF676B8C),
    online = Color(0xFF6CC79E), away = Color(0xFFE8BB6C), dnd = Color(0xFFE48394),
    unread = Color(0xFFE5839E), unreadInk = Color(0xFF3D0F1F), mention = Color(0xFFEFF0FE), mentionInk = Color(0xFF343C9C),
    link = Color(0xFF4F58C4), codeBackground = Color(0xFFF4F4FB),
)

private val DarkChat = ChatColors(
    sidebar = Color(0xFF1E2033), sidebarText = Color(0xFFCFD1E8), sidebarMuted = Color(0xFF9194B5),
    sidebarActive = Color(0xFF343869), rail = Color(0xFF181A2B), muted = Color(0xFFA3A6C4),
    online = Color(0xFF7FCFA9), away = Color(0xFFF0CB8C), dnd = Color(0xFFF2A0AD),
    unread = Color(0xFFF2A7B8), unreadInk = Color(0xFF3D0F1F), mention = Color(0xFF252946), mentionInk = Color(0xFFC9CEFC),
    link = Color(0xFFB7BEFA), codeBackground = Color(0xFF1D1F30),
)

private val LightScheme = lightColorScheme(
    primary = Color(0xFF4F58C4),
    onPrimary = Color.White,
    primaryContainer = Color(0xFFDCDFFD),
    onPrimaryContainer = Color(0xFF2E3584),
    secondaryContainer = Color(0xFFEFF0FE),
    onSecondaryContainer = Color(0xFF343C9C),
    background = Color(0xFFFBFBFE),
    onBackground = Color(0xFF2A2D45),
    surface = Color(0xFFFBFBFE),
    onSurface = Color(0xFF2A2D45),
    surfaceVariant = Color(0xFFF4F4FB),
    onSurfaceVariant = Color(0xFF676B8C),
    surfaceContainer = Color(0xFFF4F4FB),
    surfaceContainerHigh = Color(0xFFEDEDF8),
    outline = Color(0xFFC9CAE0),
    outlineVariant = Color(0xFFE4E4F2),
    error = Color(0xFFA33F48),
    errorContainer = Color(0xFFFBE4E6),
)

private val DarkScheme = darkColorScheme(
    primary = Color(0xFFA9B1F7),
    onPrimary = Color(0xFF1B1E3D),
    primaryContainer = Color(0xFF343869),
    onPrimaryContainer = Color(0xFFE8EAFF),
    secondaryContainer = Color(0xFF252946),
    onSecondaryContainer = Color(0xFFC9CEFC),
    background = Color(0xFF171826),
    onBackground = Color(0xFFE6E7F4),
    surface = Color(0xFF171826),
    onSurface = Color(0xFFE6E7F4),
    surfaceVariant = Color(0xFF1D1F30),
    onSurfaceVariant = Color(0xFFA3A6C4),
    surfaceContainer = Color(0xFF1D1F30),
    surfaceContainerHigh = Color(0xFF26283C),
    outline = Color(0xFF3B3F5C),
    outlineVariant = Color(0xFF2D3047),
    error = Color(0xFFF0A3A8),
    errorContainer = Color(0xFF3A2430),
)

val LocalChatColors = staticCompositionLocalOf { LightChat }

@Composable
fun GoChatTheme(dark: Boolean = isSystemInDarkTheme(), content: @Composable () -> Unit) {
    CompositionLocalProvider(LocalChatColors provides if (dark) DarkChat else LightChat) {
        MaterialTheme(colorScheme = if (dark) DarkScheme else LightScheme, content = content)
    }
}

/** The color of a presence dot. */
@Composable
fun statusColor(status: Status): Color {
    val c = LocalChatColors.current
    return when (status) {
        Status.STATUS_ONLINE -> c.online
        Status.STATUS_AWAY -> c.away
        Status.STATUS_DND -> c.dnd
        else -> c.muted.copy(alpha = 0.5f)
    }
}

fun statusLabel(status: Status): String = when (status) {
    Status.STATUS_ONLINE -> "Online"
    Status.STATUS_AWAY -> "Away"
    Status.STATUS_DND -> "Do not disturb"
    else -> "Offline"
}

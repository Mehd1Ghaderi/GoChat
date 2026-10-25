package io.github.ssenerg.gochat.ui.voice

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.CallEnd
import androidx.compose.material.icons.filled.GraphicEq
import androidx.compose.material.icons.filled.Headset
import androidx.compose.material.icons.filled.HeadsetOff
import androidx.compose.material.icons.filled.Mic
import androidx.compose.material.icons.filled.MicOff
import androidx.compose.material3.Button
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.FilledIconButton
import androidx.compose.material3.FilledTonalIconButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.IconButtonDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import gochat.v1.Channel
import gochat.v1.VoiceState
import io.github.ssenerg.gochat.core.model.displayName
import io.github.ssenerg.gochat.core.model.moderatedBy
import io.github.ssenerg.gochat.core.model.title
import io.github.ssenerg.gochat.core.state.ChatSession
import io.github.ssenerg.gochat.core.voice.VoiceCallState
import io.github.ssenerg.gochat.ui.Avatar
import io.github.ssenerg.gochat.ui.BackButton
import io.github.ssenerg.gochat.ui.EmptyState
import io.github.ssenerg.gochat.ui.LocalChatColors
import io.github.ssenerg.gochat.ui.main.LocalNavigator
import io.github.ssenerg.gochat.ui.main.LocalToasts
import io.github.ssenerg.gochat.ui.main.Route
import io.github.ssenerg.gochat.ui.rememberMicrophoneAccess
import kotlinx.coroutines.launch

/** Returns what joins a voice channel, once the microphone is allowed. */
@Composable
fun rememberJoinVoice(session: ChatSession): (Channel) -> Unit {
    val microphone = rememberMicrophoneAccess()
    val toasts = LocalToasts.current
    val scope = rememberCoroutineScope()
    return remember(session, microphone) {
        { ch ->
            if (!session.voice.available) {
                toasts.show("Talking in voice channels comes to this app next; join from the web for now.")
            } else {
                scope.launch {
                    if (microphone()) session.voice.join(ch) else toasts.show("Allow GoChat to use the microphone to talk in voice channels.")
                }
            }
        }
    }
}

/** What the call says of itself. */
private fun VoiceCallState.status(): String = if (connected) "Voice connected" else "Connecting…"

/** A voice channel: who talks in it, and the call's controls. */
@Composable
fun VoiceRoomScreen(session: ChatSession, channelId: String) {
    val st by session.state.collectAsState()
    val call by session.voice.state.collectAsState()
    val nav = LocalNavigator.current
    val toasts = LocalToasts.current
    val scope = rememberCoroutineScope()
    val join = rememberJoinVoice(session)
    val ch = st.channel(channelId)
    val here = call?.takeIf { it.channelId == channelId }
    val members = st.voice[channelId]?.states.orEmpty()
    val moderator = ch?.moderatedBy(st.me, st.team) == true
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(ch?.title() ?: here?.name ?: "Voice channel", maxLines = 1, overflow = TextOverflow.Ellipsis) },
                navigationIcon = { BackButton { nav.pop() } },
            )
        },
        bottomBar = {
            Surface(tonalElevation = 3.dp) {
                Box(Modifier.fillMaxWidth().navigationBarsPadding().padding(16.dp), contentAlignment = Alignment.Center) {
                    when {
                        here != null -> CallControls(session, here)
                        !session.voice.available -> Text(
                            "Talking in voice channels comes to this app next; join from the web for now.",
                            textAlign = TextAlign.Center,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        ch != null -> Button(onClick = { join(ch) }, modifier = Modifier.fillMaxWidth().height(48.dp)) {
                            Icon(Icons.Filled.Mic, contentDescription = null)
                            Spacer(Modifier.width(8.dp))
                            Text(if (call != null) "Move here" else "Join voice")
                        }
                    }
                }
            }
        },
    ) { padding ->
        if (members.isEmpty()) {
            EmptyState(if (here != null) "Connecting…" else "No one is here yet.", Modifier.padding(padding))
        } else {
            LazyVerticalGrid(
                GridCells.Adaptive(140.dp),
                Modifier.padding(padding).fillMaxSize(),
                contentPadding = PaddingValues(16.dp),
                horizontalArrangement = Arrangement.spacedBy(12.dp),
                verticalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                items(members, key = { it.user?.id ?: "" }) { vs ->
                    val id = vs.user?.id.orEmpty()
                    VoiceTile(
                        vs,
                        speaking = here != null && id in here.speaking,
                        onDisconnect = if (moderator && id != st.me.id) {
                            {
                                scope.launch { session.disconnectVoice(channelId, id)?.let(toasts::show) }
                            }
                        } else {
                            null
                        },
                    )
                }
            }
        }
    }
}

@Composable
private fun VoiceTile(vs: VoiceState, speaking: Boolean, onDisconnect: (() -> Unit)?) {
    val colors = LocalChatColors.current
    var menu by remember { mutableStateOf(false) }
    Box {
        Column(
            Modifier.fillMaxWidth().clip(RoundedCornerShape(16.dp))
                .background(MaterialTheme.colorScheme.surfaceContainerHigh)
                .then(if (onDisconnect != null) Modifier.clickable { menu = true } else Modifier)
                .padding(vertical = 20.dp, horizontal = 12.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            // A ring around whoever talks, as on the web.
            Box(
                Modifier.size(76.dp).border(3.dp, if (speaking) colors.online else Color.Transparent, CircleShape).padding(6.dp),
                contentAlignment = Alignment.Center,
            ) {
                Avatar(vs.user, size = 64.dp)
            }
            Spacer(Modifier.height(10.dp))
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(vs.user.displayName(), fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false))
                if (vs.self_deaf) {
                    Spacer(Modifier.width(4.dp))
                    Icon(Icons.Filled.HeadsetOff, contentDescription = "Deafened", tint = colors.muted, modifier = Modifier.size(16.dp))
                } else if (vs.self_mute) {
                    Spacer(Modifier.width(4.dp))
                    Icon(Icons.Filled.MicOff, contentDescription = "Muted", tint = colors.muted, modifier = Modifier.size(16.dp))
                }
            }
        }
        if (onDisconnect != null) {
            DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                DropdownMenuItem(text = { Text("Disconnect from voice") }, onClick = {
                    menu = false
                    onDisconnect()
                })
            }
        }
    }
}

/** Mute, deafen and leave, under the voice room. */
@Composable
private fun CallControls(session: ChatSession, call: VoiceCallState) {
    val colors = LocalChatColors.current
    val muted by session.voice.muted.collectAsState()
    val deafened by session.voice.deafened.collectAsState()
    Column(horizontalAlignment = Alignment.CenterHorizontally) {
        Text(call.status(), color = if (call.connected) colors.online else colors.away, fontWeight = FontWeight.SemiBold)
        Spacer(Modifier.height(12.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(20.dp), verticalAlignment = Alignment.CenterVertically) {
            FilledTonalIconButton(onClick = { session.voice.toggleMute() }, modifier = Modifier.size(56.dp)) {
                Icon(if (muted || deafened) Icons.Filled.MicOff else Icons.Filled.Mic, contentDescription = if (muted || deafened) "Unmute" else "Mute")
            }
            FilledTonalIconButton(onClick = { session.voice.toggleDeafen() }, modifier = Modifier.size(56.dp)) {
                Icon(if (deafened) Icons.Filled.HeadsetOff else Icons.Filled.Headset, contentDescription = if (deafened) "Undeafen" else "Deafen")
            }
            FilledIconButton(
                onClick = { session.voice.leave() },
                modifier = Modifier.size(56.dp),
                colors = IconButtonDefaults.filledIconButtonColors(
                    containerColor = MaterialTheme.colorScheme.error,
                    contentColor = MaterialTheme.colorScheme.onError,
                ),
            ) {
                Icon(Icons.Filled.CallEnd, contentDescription = "Leave voice")
            }
        }
    }
}

/** The call, at the foot of the sidebar wherever the person is, as in Discord. */
@Composable
fun VoicePanel(session: ChatSession) {
    val call = session.voice.state.collectAsState().value ?: return
    val muted by session.voice.muted.collectAsState()
    val deafened by session.voice.deafened.collectAsState()
    val colors = LocalChatColors.current
    val nav = LocalNavigator.current
    val tint = if (call.connected) colors.online else colors.away
    Row(
        Modifier.fillMaxWidth().background(colors.rail.copy(alpha = 0.6f))
            .clickable { nav.open(Route.Voice(call.channelId)) }
            .padding(start = 12.dp, end = 4.dp, top = 4.dp, bottom = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(Icons.Filled.GraphicEq, contentDescription = null, tint = tint, modifier = Modifier.size(22.dp))
        Spacer(Modifier.width(10.dp))
        Column(Modifier.weight(1f)) {
            Text(call.status(), color = tint, fontWeight = FontWeight.SemiBold, fontSize = 13.sp)
            Text(call.name, color = colors.sidebarMuted, fontSize = 12.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
        IconButton(onClick = { session.voice.toggleMute() }) {
            Icon(if (muted || deafened) Icons.Filled.MicOff else Icons.Filled.Mic, contentDescription = if (muted || deafened) "Unmute" else "Mute", tint = colors.sidebarText)
        }
        IconButton(onClick = { session.voice.toggleDeafen() }) {
            Icon(if (deafened) Icons.Filled.HeadsetOff else Icons.Filled.Headset, contentDescription = if (deafened) "Undeafen" else "Deafen", tint = colors.sidebarText)
        }
        IconButton(onClick = { session.voice.leave() }) {
            Icon(Icons.Filled.CallEnd, contentDescription = "Leave voice", tint = MaterialTheme.colorScheme.error)
        }
    }
}

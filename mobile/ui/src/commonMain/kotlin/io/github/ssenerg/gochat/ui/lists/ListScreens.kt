package io.github.ssenerg.gochat.ui.lists

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import gochat.v1.Channel
import gochat.v1.Message
import gochat.v1.SearchHit
import gochat.v1.ThreadSummary
import io.github.ssenerg.gochat.core.api.userMessage
import io.github.ssenerg.gochat.core.model.Format
import io.github.ssenerg.gochat.core.model.displayName
import io.github.ssenerg.gochat.core.model.excerpt
import io.github.ssenerg.gochat.core.model.isConversation
import io.github.ssenerg.gochat.core.model.sentAtMillis
import io.github.ssenerg.gochat.core.model.title
import io.github.ssenerg.gochat.core.state.ChatSession
import io.github.ssenerg.gochat.ui.Avatar
import io.github.ssenerg.gochat.ui.BackButton
import io.github.ssenerg.gochat.ui.EmptyState
import io.github.ssenerg.gochat.ui.ErrorState
import io.github.ssenerg.gochat.ui.LocalChatColors
import io.github.ssenerg.gochat.ui.Loading
import io.github.ssenerg.gochat.ui.main.LocalNavigator
import io.github.ssenerg.gochat.ui.main.Navigator
import io.github.ssenerg.gochat.ui.main.Route
import kotlinx.coroutines.launch

/** What a list screen loads: nothing yet, its items, or why not. */
private sealed interface Load<out T> {
    data object Loading : Load<Nothing>

    data class Done<T>(val value: T) : Load<T>

    data class Failed(val message: String) : Load<Nothing>
}

@Composable
private fun <T> rememberLoad(vararg keys: Any?, load: suspend () -> T): Pair<Load<T>, () -> Unit> {
    var state by remember(*keys) { mutableStateOf<Load<T>>(Load.Loading) }
    var attempt by remember(*keys) { mutableStateOf(0) }
    LaunchedEffect(attempt, *keys) {
        state = Load.Loading
        state = try {
            Load.Done(load())
        } catch (e: Exception) {
            Load.Failed(e.userMessage())
        }
    }
    return state to { attempt++ }
}

@Composable
private fun ListScaffold(title: String, content: @Composable (Modifier) -> Unit) {
    val nav = LocalNavigator.current
    Scaffold(topBar = { TopAppBar(title = { Text(title) }, navigationIcon = { BackButton { nav.pop() } }) }) { padding ->
        content(Modifier.padding(padding).fillMaxSize())
    }
}

private fun where(ch: Channel?): String = when {
    ch == null -> ""
    ch.isConversation -> ch.title()
    else -> "#${ch.title()}"
}

/** Opens a message where it lives: its thread, or its channel. */
private fun open(nav: Navigator, m: Message) {
    when {
        m.root_id.isNotEmpty() -> nav.open(Route.Thread(m.channel_id, m.root_id))
        m.reply_count > 0 -> nav.open(Route.Thread(m.channel_id, m.id))
        else -> nav.openChannel(m.channel_id)
    }
}

/** A message in a list: who, where, when and what. */
@Composable
private fun MessageSummary(m: Message, place: String, unread: Boolean = false, extra: String? = null, onClick: () -> Unit) {
    val colors = LocalChatColors.current
    Row(Modifier.fillMaxWidth().clickable(onClick = onClick).padding(horizontal = 16.dp, vertical = 12.dp)) {
        Avatar(m.author, size = 36.dp)
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    m.author.displayName(),
                    fontWeight = if (unread) FontWeight.Bold else FontWeight.SemiBold,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f, fill = false),
                )
                if (place.isNotEmpty()) {
                    Text(" · $place", color = colors.muted, fontSize = 13.sp, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false))
                }
                Spacer(Modifier.weight(1f))
                Text(Format.short(m.sentAtMillis(), Format.nowMillis()), color = colors.muted, fontSize = 12.sp)
            }
            Text(m.excerpt(), maxLines = 3, overflow = TextOverflow.Ellipsis)
            if (extra != null) Text(extra, color = MaterialTheme.colorScheme.primary, fontSize = 13.sp, fontWeight = FontWeight.SemiBold)
        }
        if (unread) {
            Spacer(Modifier.width(8.dp))
            Box(Modifier.padding(top = 6.dp).size(8.dp).clip(CircleShape).background(colors.unread))
        }
    }
}

@Composable
fun ThreadsScreen(session: ChatSession) {
    val nav = LocalNavigator.current
    val chat by session.state.collectAsState()
    val teamId = chat.teamId ?: return
    val (load, retry) = rememberLoad(teamId) { session.api.threads(teamId) }
    ListScaffold("Threads") { modifier ->
        when (val l = load) {
            Load.Loading -> Loading(modifier)
            is Load.Failed -> ErrorState(l.message, retry, modifier)
            is Load.Done -> if (l.value.threads.isEmpty()) {
                EmptyState("Threads you start or reply in show here.", modifier)
            } else {
                LazyColumn(modifier) {
                    items(l.value.threads.filter { it.root != null }.distinctBy { it.root?.id }, key = { it.root?.id.orEmpty() }) { t: ThreadSummary ->
                        val root = t.root ?: return@items
                        MessageSummary(
                            root, where(t.channel), unread = t.unread,
                            extra = if (root.reply_count == 1) "1 reply" else "${root.reply_count} replies",
                        ) { nav.open(Route.Thread(root.channel_id, root.id)) }
                        HorizontalDivider()
                    }
                }
            }
        }
    }
}

@Composable
fun SavedScreen(session: ChatSession) {
    val nav = LocalNavigator.current
    val chat by session.state.collectAsState()
    val teamId = chat.teamId ?: return
    val (load, retry) = rememberLoad(teamId, chat.saved.size) { session.api.saved(teamId) }
    ListScaffold("Saved") { modifier ->
        when (val l = load) {
            Load.Loading -> Loading(modifier)
            is Load.Failed -> ErrorState(l.message, retry, modifier)
            is Load.Done -> HitList(l.value, "Save messages to find them here: hold a message and choose Save.", modifier) { open(nav, it) }
        }
    }
}

@Composable
fun PinsScreen(session: ChatSession, channelId: String) {
    val nav = LocalNavigator.current
    val chat by session.state.collectAsState()
    val pins = chat.channel(channelId)?.pinned_count ?: 0
    val (load, retry) = rememberLoad(channelId, pins) { session.api.pinned(channelId) }
    ListScaffold("Pinned") { modifier ->
        when (val l = load) {
            Load.Loading -> Loading(modifier)
            is Load.Failed -> ErrorState(l.message, retry, modifier)
            is Load.Done -> if (l.value.isEmpty()) {
                EmptyState("Nothing is pinned in this channel.", modifier)
            } else {
                LazyColumn(modifier) {
                    items(l.value, key = { it.id }) { m ->
                        MessageSummary(m, "") { open(nav, m) }
                        HorizontalDivider()
                    }
                }
            }
        }
    }
}

/** The hits with a message, each once: their ids key the list. */
private fun List<SearchHit>.shown() = filter { it.message != null }.distinctBy { it.message?.id }

@Composable
private fun HitList(hits: List<SearchHit>, empty: String, modifier: Modifier, onOpen: (Message) -> Unit) {
    if (hits.isEmpty()) {
        EmptyState(empty, modifier)
        return
    }
    LazyColumn(modifier) {
        items(hits.shown(), key = { it.message?.id.orEmpty() }) { hit ->
            val m = hit.message ?: return@items
            MessageSummary(m, where(hit.channel)) { onOpen(m) }
            HorizontalDivider()
        }
    }
}

@Composable
fun SearchScreen(session: ChatSession) {
    val nav = LocalNavigator.current
    val chat by session.state.collectAsState()
    val teamId = chat.teamId ?: return
    val scope = rememberCoroutineScope()
    var query by remember { mutableStateOf("") }
    var hits by remember { mutableStateOf<List<SearchHit>?>(null) }
    var cursor by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    val focus = remember { FocusRequester() }
    LaunchedEffect(Unit) { focus.requestFocus() }

    fun search(more: Boolean) {
        if (query.isBlank() || busy) return
        busy = true
        error = null
        scope.launch {
            try {
                val res = session.api.search(teamId, query.trim(), cursor = if (more) cursor else "")
                // A later page can repeat a hit when messages arrive meanwhile.
                hits = (if (more) (hits ?: emptyList()) + res.hits else res.hits).shown()
                cursor = res.next_cursor
            } catch (e: Exception) {
                error = e.userMessage()
            }
            busy = false
        }
    }

    ListScaffold("Search") { modifier ->
        Column(modifier) {
            OutlinedTextField(
                value = query,
                onValueChange = { query = it },
                placeholder = { Text("Words, from:name, in:channel") },
                leadingIcon = { Icon(Icons.Filled.Search, contentDescription = null) },
                singleLine = true,
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
                keyboardActions = KeyboardActions(onSearch = { search(false) }),
                modifier = Modifier.fillMaxWidth().padding(12.dp).focusRequester(focus),
            )
            val list = hits
            when {
                error != null -> ErrorState(error ?: "", onRetry = { search(false) })
                list == null && busy -> Loading()
                list == null -> EmptyState("Search the team's channels and your conversations.")
                list.isEmpty() -> EmptyState("Nothing matches.")
                else -> LazyColumn(Modifier.fillMaxSize()) {
                    items(list, key = { it.message?.id.orEmpty() }) { hit ->
                        val m = hit.message ?: return@items
                        MessageSummary(m, where(hit.channel)) { open(nav, m) }
                        HorizontalDivider()
                    }
                    if (cursor.isNotEmpty()) {
                        item {
                            Box(Modifier.fillMaxWidth().padding(8.dp), contentAlignment = Alignment.Center) {
                                if (busy) CircularProgressIndicator(Modifier.size(24.dp)) else TextButton(onClick = { search(true) }) { Text("More results") }
                            }
                        }
                    }
                }
            }
        }
    }
}

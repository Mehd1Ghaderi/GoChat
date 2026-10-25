package io.github.ssenerg.gochat.ui

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import coil3.ImageLoader
import coil3.compose.LocalPlatformContext
import coil3.network.ktor3.KtorNetworkFetcherFactory
import io.github.ssenerg.gochat.core.state.AppState
import io.github.ssenerg.gochat.core.state.GoChatApp
import io.github.ssenerg.gochat.ui.auth.AuthScreens
import io.github.ssenerg.gochat.ui.main.MainScreen

/** The whole app: signing in, then the chat. */
@Composable
fun GoChatRoot(app: GoChatApp, platform: Platform) {
    GoChatTheme {
        CompositionLocalProvider(LocalPlatform provides platform) {
            Surface(Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) {
                val state by app.state.collectAsState()
                when (val s = state) {
                    AppState.Starting -> Loading()
                    is AppState.SignedOut, is AppState.Verifying -> AuthScreens(app, s)
                    is AppState.SignedIn -> {
                        val context = LocalPlatformContext.current
                        val images = remember(s.session) {
                            val client = s.session.api.client
                            Images(
                                ImageLoader.Builder(context)
                                    .components { add(KtorNetworkFetcherFactory(httpClient = { client.http })) }
                                    .build(),
                                client,
                            )
                        }
                        CompositionLocalProvider(LocalImages provides images) {
                            MainScreen(app, s.session)
                        }
                    }
                }
            }
        }
    }
}

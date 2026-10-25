package io.github.ssenerg.gochat.ui

import androidx.compose.ui.window.ComposeUIViewController
import io.github.ssenerg.gochat.core.state.GoChatApp
import kotlinx.coroutines.MainScope
import platform.UIKit.UIViewController

private val scope = MainScope()
private val platform = IosPlatform(scope)
private val app = GoChatApp(platform.store, scope, rtc = platform.rtc).also { it.start() }

/** The app's screen, for the Swift side to show. */
fun MainViewController(): UIViewController = ComposeUIViewController { GoChatRoot(app, platform) }

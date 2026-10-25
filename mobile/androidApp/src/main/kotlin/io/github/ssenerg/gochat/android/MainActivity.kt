package io.github.ssenerg.gochat.android

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import io.github.ssenerg.gochat.ui.GoChatRoot

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        val application = application as GoChatApplication
        setContent { GoChatRoot(application.app, application.platform) }
    }
}

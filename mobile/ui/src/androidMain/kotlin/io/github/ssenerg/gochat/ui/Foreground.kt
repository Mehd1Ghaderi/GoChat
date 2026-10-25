package io.github.ssenerg.gochat.ui

import android.app.Activity
import android.app.Application
import android.os.Bundle
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/** Whether one of the app's activities is started, as the person sees it. */
internal class Foreground(application: Application) {
    private val _shown = MutableStateFlow(false)
    val shown: StateFlow<Boolean> = _shown.asStateFlow()
    private var started = 0

    init {
        application.registerActivityLifecycleCallbacks(
            object : Application.ActivityLifecycleCallbacks {
                override fun onActivityStarted(activity: Activity) {
                    started++
                    _shown.value = true
                }

                override fun onActivityStopped(activity: Activity) {
                    started = maxOf(0, started - 1)
                    // A rotation stops the old activity after starting the new.
                    _shown.value = started > 0
                }

                override fun onActivityCreated(activity: Activity, savedInstanceState: Bundle?) {}

                override fun onActivityResumed(activity: Activity) {}

                override fun onActivityPaused(activity: Activity) {}

                override fun onActivitySaveInstanceState(activity: Activity, outState: Bundle) {}

                override fun onActivityDestroyed(activity: Activity) {}
            },
        )
    }
}

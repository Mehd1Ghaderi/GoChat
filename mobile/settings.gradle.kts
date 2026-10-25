rootProject.name = "GoChat"

pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}

dependencyResolutionManagement {
    repositories {
        google()
        mavenCentral()
    }
}

// core: the protocol, the API client and the app's state, in plain Kotlin.
// ui: the Compose Multiplatform screens, shared by Android and iOS.
// androidApp: the Android app around them; iosApp is the Xcode project.
include(":core", ":ui", ":androidApp")

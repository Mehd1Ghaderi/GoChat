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
include(":core")

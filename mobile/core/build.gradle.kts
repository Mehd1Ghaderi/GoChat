plugins {
    alias(libs.plugins.kotlin.multiplatform)
    alias(libs.plugins.wire)
}

kotlin {
    // The JVM build serves Android, which takes JVM libraries, and the tests.
    jvm()
    iosArm64()
    iosSimulatorArm64()

    sourceSets {
        all {
            languageSettings.optIn("kotlin.time.ExperimentalTime")
        }
        commonMain.dependencies {
            api(libs.kotlinx.coroutines.core)
            api(libs.kotlinx.datetime)
            api(libs.wire.runtime)
            api(libs.ktor.client.core)
            implementation(libs.ktor.client.websockets)
        }
        jvmMain.dependencies {
            implementation(libs.ktor.client.okhttp)
        }
        iosMain.dependencies {
            implementation(libs.ktor.client.darwin)
        }
        commonTest.dependencies {
            implementation(kotlin("test"))
            implementation(libs.kotlinx.coroutines.test)
            implementation(libs.ktor.client.mock)
        }
    }
}

// The app speaks the server's own protobuf API: Wire generates its Kotlin
// from the same .proto files the Go code is generated from.
wire {
    sourcePath {
        srcDir("../../api/protobuf/proto")
    }
    kotlin {
        rpcRole = "none"
    }
}

tasks.withType<Test>().configureEach {
    // GOCHAT_E2E_URL and the others point the integration test at a
    // running stack, as for the Go end-to-end suite.
    listOf("GOCHAT_E2E_URL", "GOCHAT_E2E_ADMIN", "GOCHAT_E2E_ADMIN_PASSWORD", "GOCHAT_E2E_TEAM").forEach { name ->
        System.getenv(name)?.let { environment(name, it) }
    }
}

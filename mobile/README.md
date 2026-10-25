# GoChat for Android and iOS

Native apps for GoChat, written once in Kotlin with
[Kotlin Multiplatform](https://kotlinlang.org/docs/multiplatform.html) and
[Compose Multiplatform](https://www.jetbrains.com/compose-multiplatform/).
They talk to a GoChat server through its [client API](../README.md#client-api-for-native-apps)
at `/api/v1`, in the same protobuf messages the web gateway uses.

## Layout

```
core/        the protocol, the API client and the app's state, in plain Kotlin (JVM and iOS)
ui/          the Compose screens, shared by Android and iOS, and each platform's services
androidApp/  the Android app: the Application, the Activity, the manifest and the icon
iosApp/      the Xcode project: a SwiftUI shell that shows the shared screens
```

- **core** generates its protobuf types with [Wire](https://square.github.io/wire/)
  from the server's own `api/protobuf/proto`. `ApiClient` makes calls over
  [Ktor](https://ktor.io); `Gateway` keeps the event socket open and
  reconnects; `GoChatApp` restores the session and signs people in;
  `ChatSession` holds teams, channels, presence and voice states and applies
  every event the server sends; `Timeline` and `ThreadView` hold a channel's
  and a thread's messages. Formatting and the message Markdown live here too,
  so the screens only draw.
- **ui** draws the app with Material 3 in the web's colors, light and dark.
  Each platform supplies storage (Android's private preferences, the iOS
  Keychain), voice recording and playback (`MediaRecorder`/`MediaPlayer`,
  `AVAudioRecorder`/`AVAudioPlayer`), the file picker and sharing.

## What the apps do

- Sign in to any GoChat server, with two-factor codes, or sign up with an
  invite link.
- Teams, channels, voice channels (who talks in them), direct and group
  conversations, with presence, unread marks and mention counts, all live.
- Messages with Markdown, mentions, emoji, images, files, reactions,
  threads, edits, deletes, pins and saves; typing indicators; search;
  followed threads; saved messages.
- Voice messages: record, send and play. Both apps record AAC in MP4,
  which the server takes and every platform plays.
- Browse, create, mute and leave channels; moderators rename them, change
  their privacy, turn voice messages on or off and archive them.
- Profile name and photo, status with do not disturb, custom status,
  password, sign-out.

Not in the apps yet:

- **Talking in voice channels.** The apps show who talks; joining needs
  WebRTC on both platforms and is next.
- **Push notifications while the app is closed.** The apps show notices
  while they run. Web Push reaches browsers only: the apps would need
  Firebase Cloud Messaging on Android and APNs on iOS.
- Voice messages that Chrome or Firefox recorded (WebM) do not play on iOS,
  whose player has no WebM. Android plays them.
- Two-factor setup, team settings and team photos stay on the web.

## Building

You need JDK 17 or later.

**Android**: Android Studio, or the Android SDK with platform 36.

```bash
cd mobile
./gradlew :androidApp:installDebug   # on a connected device or a running emulator
```

**iOS**: a Mac with Xcode 16 or later. Open `iosApp/iosApp.xcodeproj`, choose
your team under Signing & Capabilities, and run. The project's first build
phase builds the Kotlin framework with Gradle.

**Tests**: the core's tests run anywhere:

```bash
cd mobile
./gradlew :core:jvmTest
```

With a running stack, the same command also drives it as the apps do: it
signs in, receives events from another device, sends, reacts, replies,
uploads a voice message, edits, deletes, opens a conversation and signs out.

```bash
GOCHAT_E2E_URL=http://localhost:8080 GOCHAT_E2E_ADMIN=admin \
GOCHAT_E2E_ADMIN_PASSWORD=... GOCHAT_E2E_TEAM=acme ./gradlew :core:jvmTest
```

CI (`.github/workflows/mobile.yml`) runs the core's tests on the JVM and the
iOS simulator, builds the Android app and builds the iOS app.

## Servers

The apps take the server's address on the sign-in screen: `chat.example.com`
means `https://chat.example.com`. They speak HTTPS only, except to a
development machine:

- the Android emulator reaches the host as `http://10.0.2.2:8080`;
- a device on USB reaches it as `http://localhost:8080` after
  `adb reverse tcp:8080 tcp:8080`;
- the iOS simulator reaches it as `http://localhost:8080`.

The session token stays on the device: in the Keychain on iOS, and on
Android in the app's private storage, which backups and device transfers
leave out.

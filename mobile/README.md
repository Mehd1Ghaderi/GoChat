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
  and a thread's messages; `VoiceCall` talks in a voice channel, speaking the
  voice server's signaling protocol as the web's voice client does, over the
  platform's WebRTC (`RtcEngine`). Formatting and the message Markdown live
  here too, so the screens only draw.
- **ui** draws the app with Material 3 in the web's colors, light and dark.
  Each platform supplies storage (Android's private preferences, the iOS
  Keychain), voice recording and playback (`MediaRecorder`/`MediaPlayer`,
  `AVAudioRecorder`/`AVAudioPlayer`), the file picker and sharing. Android
  supplies WebRTC too, from [Stream's build](https://github.com/GetStream/webrtc-android)
  of Google's library, and a foreground service that keeps a call going
  with the screen off.

## What the apps do

- Sign in to any GoChat server, with two-factor codes, or sign up with an
  invite link.
- Teams, channels, voice channels, direct and group conversations, with
  presence, unread marks and mention counts, all live.
- Voice channels, on Android: join, talk, mute, deafen and leave, see who
  is talking, and, as a moderator, disconnect people. The call goes on in the
  background, on the loudspeaker or a headset, rejoins after a network
  change, and can be left from its notification. The iOS app shows who talks.
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

- **Talking in voice channels on iOS.** It needs WebRTC for iOS linked into
  the app; the core's `VoiceCall` is ready for it.
- **Push notifications while the app is closed.** The apps show notices
  while they run. Web Push reaches browsers only: the apps would need
  Firebase Cloud Messaging on Android and APNs on iOS.
- Voice messages that Chrome or Firefox recorded (WebM) do not play on iOS,
  whose player has no WebM. Android plays them.
- Two-factor setup, team settings and team photos stay on the web.

## Building

You need JDK 17 or later.

**Android**: Android Studio, or the Android SDK with platform 37.

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

## Trying it on a phone

1. **The APK.** Each push to `feature/mobile` publishes a debug build as the
   `android-preview` pre-release on GitHub (Releases), once GitHub Actions
   is enabled for the repository. Open it on the phone, download
   `GoChat-debug.apk` and install it (Android asks to allow installs from
   the browser once). Or build it with `./gradlew :androidApp:assembleDebug`
   and find it in `androidApp/build/outputs/apk/debug/`.
2. **A server with the client API**, that is, built from this branch:
   - your server, deployed from it, at its HTTPS address; or
   - the Docker stack on your computer (Linux, or Docker Desktop on Windows
     or macOS), opened to the local network as below: `just up`, then sign
     in on the phone, on the same Wi-Fi, to `http://<the computer's IP>:8080`.
     Debug builds take plain HTTP to any address for this; release builds
     take HTTPS only.

### The Docker stack, for a phone

The phone reaches the computer at its address on the Wi-Fi, such as
`192.168.1.20`: `ipconfig` shows it on Windows (the Wi-Fi adapter's IPv4
address), `ipconfig getifaddr en0` on macOS. By default the stack only
accepts this computer, and announces its voice server at `localhost`, which
on a phone is the phone. In `.env`, with your address:

```bash
WEB_BIND=0.0.0.0
# Voice channels: the signaling websocket and the voice itself, at this
# computer's address.
VOICE_BIND=0.0.0.0
VOICE_PUBLIC_URL=ws://192.168.1.20:8090/rtc
WEB_VOICE_ORIGINS=ws://192.168.1.20:8090
VOICE_PUBLIC_IP=192.168.1.20
```

Then `just up` again. Keep `WEB_PUBLIC_URL=http://localhost:8080`: browsers
on the computer still open the chat there, and talk too. The phone needs
ports 8080 and 8090 (TCP) and 7882 (UDP and TCP, the voice itself): allow
Docker in the firewall when asked, and on Windows mark the Wi-Fi as a
private network. If the address changes, change it here too.

When voice does not connect, the app says why:

- *The server announces its voice server at localhost*: `VOICE_PUBLIC_URL`
  is still the default.
- *Cannot reach the voice server*: port 8090 is closed, or `VOICE_BIND` is
  still `127.0.0.1`.
- *The voice connection failed: your network may block it*: the phone
  cannot reach port 7882 at `VOICE_PUBLIC_IP`; check the address and the
  firewall. Some guest and office Wi-Fi networks keep devices apart.

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

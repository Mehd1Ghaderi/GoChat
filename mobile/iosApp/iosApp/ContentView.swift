import GoChatUI
import SwiftUI
import UIKit

/// The shared Compose Multiplatform screens, from the GoChatUI framework.
struct ComposeView: UIViewControllerRepresentable {
    /// WebRTC for voice channels, one for the app.
    private static let voice = VoiceRtc()

    func makeUIViewController(context: Context) -> UIViewController {
        MainViewControllerKt.MainViewController(rtc: ComposeView.voice)
    }

    func updateUIViewController(_ uiViewController: UIViewController, context: Context) {}
}

struct ContentView: View {
    var body: some View {
        // Compose lays out around the bars and the keyboard itself.
        ComposeView().ignoresSafeArea()
    }
}

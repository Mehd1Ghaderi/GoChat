import AVFoundation
import GoChatUI
import WebRTC

/// Voice channels on iOS: Google's WebRTC, as stasel/WebRTC builds it, behind
/// the protocols the shared Kotlin code talks to. One connection at a time
/// sends the microphone and plays the others, on the loudspeaker unless a
/// headset is connected.
final class VoiceRtc: NSObject, IosRtcEngine {
    private lazy var factory: RTCPeerConnectionFactory = {
        RTCInitializeSSL()
        // WebRTC sets up the audio session when a call starts: as voice chat
        // apps do, on the loudspeaker, or a Bluetooth or wired headset.
        let audio = RTCAudioSessionConfiguration.webRTC()
        audio.categoryOptions = [.defaultToSpeaker, .allowBluetooth]
        RTCAudioSessionConfiguration.setWebRTC(audio)
        return RTCPeerConnectionFactory()
    }()

    func connect(iceServers: [IosIceServer], events: IosRtcEvents) -> IosRtcPeer {
        let config = RTCConfiguration()
        config.iceServers = iceServers.map { RTCIceServer(urlStrings: $0.urls, username: $0.username, credential: $0.credential) }
        config.sdpSemantics = .unifiedPlan
        config.bundlePolicy = .maxBundle
        config.rtcpMuxPolicy = .require
        // A phone that changes networks offers its new addresses, and the
        // voice may carry on over them.
        config.continualGatheringPolicy = .gatherContinually
        return VoicePeer(factory: factory, config: config, events: events)
    }
}

private final class VoicePeer: NSObject, IosRtcPeer, RTCPeerConnectionDelegate {
    private let events: IosRtcEvents
    private var pc: RTCPeerConnection?
    private var mic: RTCAudioTrack?
    private var meter: Timer?
    private var deafened = false

    init(factory: RTCPeerConnectionFactory, config: RTCConfiguration, events: IosRtcEvents) {
        self.events = events
        super.init()
        let none = RTCMediaConstraints(mandatoryConstraints: nil, optionalConstraints: nil)
        guard let pc = factory.peerConnection(with: config, constraints: none, delegate: self) else {
            events.state(state: "failed")
            return
        }
        // WebRTC's echo cancellation, noise suppression and gain control
        // are on, as getUserMedia's are on the web.
        let track = factory.audioTrack(with: factory.audioSource(with: none), trackId: "mic")
        pc.add(track, streamIds: ["mic"])
        self.pc = pc
        mic = track
        // How loud the microphone is, from WebRTC's statistics, as often as
        // the web measures it.
        meter = Timer.scheduledTimer(withTimeInterval: 0.06, repeats: true) { [weak self] _ in self?.measure() }
    }

    func answer(offer: String, reply: IosRtcAnswer) {
        guard let pc = pc else {
            reply.failed(reason: "The connection is closed.")
            return
        }
        pc.setRemoteDescription(RTCSessionDescription(type: .offer, sdp: offer)) { [weak self] error in
            if let error = error {
                reply.failed(reason: error.localizedDescription)
                return
            }
            // An offer may bring in another voice, which deafening silences too.
            self?.applyDeafened()
            pc.answer(for: RTCMediaConstraints(mandatoryConstraints: nil, optionalConstraints: nil)) { sdp, error in
                guard let sdp = sdp else {
                    reply.failed(reason: error?.localizedDescription ?? "WebRTC made no answer.")
                    return
                }
                pc.setLocalDescription(sdp) { error in
                    if let error = error {
                        reply.failed(reason: error.localizedDescription)
                    } else {
                        reply.answered(sdp: sdp.sdp)
                    }
                }
            }
        }
    }

    func addCandidate(sdp: String, sdpMid: String?, sdpMLineIndex: Int32) {
        pc?.add(RTCIceCandidate(sdp: sdp, sdpMLineIndex: max(sdpMLineIndex, 0), sdpMid: sdpMid)) { _ in }
    }

    func setMuted(muted: Bool) {
        mic?.isEnabled = !muted
    }

    func setDeafened(deafened: Bool) {
        self.deafened = deafened
        applyDeafened()
    }

    func close() {
        meter?.invalidate()
        meter = nil
        pc?.close()
        pc = nil
        mic = nil
    }

    /// A voice that comes in plays while its track is enabled.
    private func applyDeafened() {
        let on = !deafened
        pc?.transceivers.compactMap { $0.receiver.track as? RTCAudioTrack }.forEach { $0.isEnabled = on }
    }

    private func measure() {
        pc?.statistics { [weak self] report in
            var level = 0.0
            for stats in report.statistics.values where stats.type == "media-source" {
                if let value = stats.values["audioLevel"] as? NSNumber {
                    level = max(level, value.doubleValue)
                }
            }
            self?.events.level(level: Float(level))
        }
    }

    private func report(_ state: String) {
        events.state(state: state)
    }

    // RTCPeerConnectionDelegate

    func peerConnection(_ peerConnection: RTCPeerConnection, didChange stateChanged: RTCSignalingState) {}

    // The voices that come in play by themselves.
    func peerConnection(_ peerConnection: RTCPeerConnection, didAdd stream: RTCMediaStream) {}

    func peerConnection(_ peerConnection: RTCPeerConnection, didRemove stream: RTCMediaStream) {}

    func peerConnectionShouldNegotiate(_ peerConnection: RTCPeerConnection) {}

    func peerConnection(_ peerConnection: RTCPeerConnection, didChange newState: RTCIceConnectionState) {
        // The connection's own state says more; this one stands in where
        // it is late.
        switch newState {
        case .connected, .completed: report("connected")
        case .failed: report("failed")
        default: break
        }
    }

    func peerConnection(_ peerConnection: RTCPeerConnection, didChange newState: RTCIceGatheringState) {}

    func peerConnection(_ peerConnection: RTCPeerConnection, didGenerate candidate: RTCIceCandidate) {
        events.candidate(sdp: candidate.sdp, sdpMid: candidate.sdpMid, sdpMLineIndex: candidate.sdpMLineIndex)
    }

    func peerConnection(_ peerConnection: RTCPeerConnection, didRemove candidates: [RTCIceCandidate]) {}

    func peerConnection(_ peerConnection: RTCPeerConnection, didOpen dataChannel: RTCDataChannel) {}

    func peerConnection(_ peerConnection: RTCPeerConnection, didChange newState: RTCPeerConnectionState) {
        switch newState {
        case .new, .connecting: report("connecting")
        case .connected: report("connected")
        case .disconnected: report("disconnected")
        case .failed: report("failed")
        case .closed: break
        @unknown default: break
        }
    }
}

package voiceserver

import "github.com/pion/webrtc/v4"

// The signaling protocol between a browser and the voice server, one JSON
// object per websocket message, as Discord's voice gateway uses opcodes.
//
// The browser sends identify first, with the token the chat server gave
// it, and answers the offers the server makes; the server never takes an
// offer, so the two never offer at once. Both send ICE candidates as they
// find them. speaking says whether the browser hears its own voice, and
// the server tells the others.
const (
	opIdentify  = "identify"
	opReady     = "ready"
	opOffer     = "offer"
	opAnswer    = "answer"
	opCandidate = "candidate"
	opSpeaking  = "speaking"
)

// The close codes a browser may see.
const (
	closeIdentify     = 4001 // no valid identify in time
	closeDisconnected = 4002 // the chat server ended the talk; the reason says why
	closeFull         = 4003 // the voice channel is full
	closeUnavailable  = 4004 // the voice server cannot serve now
	closeFailed       = 4005 // the connection failed
)

type inbound struct {
	Op        string                   `json:"op"`
	Token     string                   `json:"token,omitempty"`
	SDP       string                   `json:"sdp,omitempty"`
	Candidate *webrtc.ICECandidateInit `json:"candidate,omitempty"`
	Speaking  bool                     `json:"speaking,omitempty"`
}

type outbound struct {
	Op         string                   `json:"op"`
	UserID     string                   `json:"user_id,omitempty"`
	ChannelID  string                   `json:"channel_id,omitempty"`
	Session    string                   `json:"session,omitempty"`
	ICEServers []iceServer              `json:"ice_servers,omitempty"`
	SDP        string                   `json:"sdp,omitempty"`
	Candidate  *webrtc.ICECandidateInit `json:"candidate,omitempty"`
	Speaking   *bool                    `json:"speaking,omitempty"`
}

// iceServer is an RTCIceServer as browsers take it.
type iceServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

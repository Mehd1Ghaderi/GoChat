// Package audio recognizes the recordings browsers make for voice messages.
package audio

import "bytes"

// The containers MediaRecorder writes audio in: WebM in Chromium, Ogg or
// WebM in Firefox, MP4 in Safari.
var (
	webm = []byte{0x1a, 0x45, 0xdf, 0xa3}
	ogg  = []byte("OggS")
	ftyp = []byte("ftyp")
)

// Sniff returns the content type of a recording from its first bytes, and
// false when they start no container a browser records voice in. The
// containers can carry video as well, which a voice message's player
// ignores.
func Sniff(head []byte) (string, bool) {
	switch {
	case bytes.HasPrefix(head, webm):
		return "audio/webm", true
	case bytes.HasPrefix(head, ogg):
		return "audio/ogg", true
	case len(head) >= 8 && bytes.Equal(head[4:8], ftyp):
		return "audio/mp4", true
	}
	return "", false
}

// Playable reports whether a browser may play a file of contentType in
// place: the types Sniff gives voice messages.
func Playable(contentType string) bool {
	switch contentType {
	case "audio/webm", "audio/ogg", "audio/mp4":
		return true
	}
	return false
}

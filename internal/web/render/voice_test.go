package render

import (
	"bytes"
	"strings"
	"testing"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
)

func TestClockMS(t *testing.T) {
	cases := map[uint32]string{0: "0:00", 499: "0:00", 500: "0:01", 2759: "0:03", 65_000: "1:05", 600_000: "10:00"}
	for ms, want := range cases {
		if got := ClockMS(ms); got != want {
			t.Errorf("ClockMS(%d) = %q, want %q", ms, got, want)
		}
	}
}

func TestVoiceMessage(t *testing.T) {
	r, err := New(16)
	if err != nil {
		t.Fatal(err)
	}
	voice := &pb.Attachment{Id: "a1", Name: "voice-message.webm", ContentType: "audio/webm", Size: 4000, Voice: true, DurationMs: 65_000}
	m := r.Message(&pb.Message{Id: "id1", ChannelId: "c1", Author: &pb.User{Id: "u", Username: "u"}, Attachments: []*pb.Attachment{voice}}, &pb.User{Id: "v"}, false)
	var buf bytes.Buffer
	if err := r.Fragment(&buf, "message", m); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{`class="voice-player" data-duration="65000"`, `src="/files/a1/inline"`, `preload="none"`, ">1:05<", `href="/files/a1"`} {
		if !strings.Contains(out, want) {
			t.Errorf("voice message lacks %s: %s", want, out)
		}
	}
	if got := Excerpt(m.Message); got != "Sent a voice message" {
		t.Errorf("excerpt = %q", got)
	}

	// Other audio files stay downloads.
	voice.Voice = false
	buf.Reset()
	if err := r.Fragment(&buf, "message", r.Message(m.Message, &pb.User{Id: "v"}, false)); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); strings.Contains(out, "voice-player") || !strings.Contains(out, "voice-message.webm") {
		t.Errorf("a plain audio file rendered as a voice message: %s", out)
	}
}

func TestVoiceSlot(t *testing.T) {
	r, err := New(16)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := r.Fragment(&buf, "voice-slot", map[string]any{"Channel": "c1", "Enabled": true}); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); !strings.Contains(out, "voice-record") || strings.Contains(out, "hx-swap-oob") {
		t.Errorf("enabled slot: %s", out)
	}
	buf.Reset()
	if err := r.Fragment(&buf, "voice-slot", map[string]any{"Channel": "c1", "Enabled": false, "OOB": true}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "voice-record") || !strings.Contains(out, `hx-swap-oob="outerHTML:.voice-slot[data-voice-for='c1']"`) {
		t.Errorf("disabled update: %s", out)
	}
}

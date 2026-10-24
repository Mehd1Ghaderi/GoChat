package channels

import (
	"testing"

	"github.com/ssenerg/gochat/internal/core/models"
	ierrors "github.com/ssenerg/gochat/internal/errors"
)

func TestCheckVoice(t *testing.T) {
	file := &models.Attachment{Name: "notes.pdf"}
	voice := &models.Attachment{Name: "voice.webm", Voice: true}
	cases := []struct {
		name     string
		disabled bool
		atts     []*models.Attachment
		code     ierrors.Code
	}{
		{"no attachments", false, nil, 0},
		{"files where voice is off", true, []*models.Attachment{file, file}, 0},
		{"one voice message", false, []*models.Attachment{voice, file}, 0},
		{"voice where it is off", true, []*models.Attachment{voice}, ierrors.CodeFailedPrecondition},
		{"two voice messages", false, []*models.Attachment{voice, voice}, ierrors.CodeInvalidArgument},
	}
	for _, c := range cases {
		a := &channel{ch: &models.Channel{VoiceDisabled: c.disabled}}
		err := a.checkVoice(c.atts)
		switch {
		case c.code == 0 && err != nil:
			t.Errorf("%s: unexpected error %v", c.name, err)
		case c.code != 0 && !ierrors.Is(err, c.code):
			t.Errorf("%s: got %v, want code %s", c.name, err, c.code)
		}
	}
}

func TestVoiceChannelsCarryNoMessages(t *testing.T) {
	voice := &channel{ch: &models.Channel{Kind: models.ChannelVoice}}
	text := &channel{ch: &models.Channel{Kind: models.ChannelPublic}}
	for _, req := range []Request{Post{}, Edit{}, Delete{}, History{}, GetMessage{}, Thread{}, Typing{}, React{}, Mute{}, MarkRead{}, SetVoice{}, Join{}, AddMember{}, Settings{}, Privacy{}, Archive{}} {
		if err := voice.voiceRefuses(req); !ierrors.Is(err, ierrors.CodeFailedPrecondition) {
			t.Errorf("a voice channel took %T: %v", req, err)
		}
		if err := text.voiceRefuses(req); err != nil {
			t.Errorf("a text channel refused %T: %v", req, err)
		}
	}
	for _, req := range []Request{Access{}, View{}, Leave{}, Members{}, VoiceAccess{}} {
		if err := voice.voiceRefuses(req); err != nil {
			t.Errorf("a voice channel refused %T: %v", req, err)
		}
	}
	if !voice.open() || !text.open() || (&channel{ch: &models.Channel{Kind: models.ChannelPrivate}}).open() {
		t.Error("only public and voice channels are open to their team")
	}
}

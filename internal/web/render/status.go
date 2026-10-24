package render

import (
	"time"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/core/emoji"
)

// StatusClass is the presence dot's class for s.
func StatusClass(s pb.Status) string {
	switch s {
	case pb.Status_STATUS_ONLINE:
		return "on"
	case pb.Status_STATUS_AWAY:
		return "away"
	case pb.Status_STATUS_DND:
		return "dnd"
	}
	return ""
}

func StatusLabel(s pb.Status) string {
	switch s {
	case pb.Status_STATUS_ONLINE:
		return "Online"
	case pb.Status_STATUS_AWAY:
		return "Away"
	case pb.Status_STATUS_DND:
		return "Do not disturb"
	}
	return "Offline"
}

// Chosen is the status u chose, as it stands now.
func Chosen(u *pb.User) pb.Status {
	s := u.GetStatus()
	switch {
	case s == pb.Status_STATUS_UNSPECIFIED:
		return pb.Status_STATUS_ONLINE
	case s == pb.Status_STATUS_DND && u.DndUntil != nil && !time.Now().Before(u.DndUntil.AsTime()):
		return pb.Status_STATUS_ONLINE
	}
	return s
}

func EmojiChar(name string) string {
	c, _ := emoji.Char(name)
	return c
}

type (
	StatusChoice struct {
		Value, Label, Note string
		Status             pb.Status
		On                 bool
	}
	Option struct {
		Value int
		Label string
	}
	StatusPreset struct{ Emoji, Text string }
	StatusForm   struct {
		Choices []StatusChoice
		DNDFor  []Option
		ClearIn []Option
		Presets []StatusPreset
		Emoji   []emoji.Emoji
		// The custom status set now.
		CustomEmoji, CustomText string
	}
)

var (
	statusChoices = []StatusChoice{
		{Value: "online", Label: "Online", Status: pb.Status_STATUS_ONLINE},
		{Value: "away", Label: "Away", Status: pb.Status_STATUS_AWAY},
		{Value: "dnd", Label: "Do not disturb", Note: "No notifications", Status: pb.Status_STATUS_DND},
		{Value: "offline", Label: "Appear offline", Note: "Others see you offline", Status: pb.Status_STATUS_OFFLINE},
	}
	// DNDFor and ClearIn are the durations offered, in minutes.
	DNDFor  = []Option{{0, "Until I turn it off"}, {30, "30 minutes"}, {60, "1 hour"}, {120, "2 hours"}, {240, "4 hours"}, {480, "8 hours"}, {1440, "24 hours"}}
	ClearIn = []Option{{0, "Don't clear"}, {30, "30 minutes"}, {60, "1 hour"}, {240, "4 hours"}, {1440, "24 hours"}, {10080, "A week"}}
	presets = []StatusPreset{
		{"calendar", "In a meeting"}, {"house", "Working remotely"}, {"headphones", "Focusing"},
		{"face_with_thermometer", "Out sick"}, {"palm_tree", "On vacation"},
	}
)

// NewStatusForm prepares the status form for u.
func NewStatusForm(u *pb.User) *StatusForm {
	f := &StatusForm{DNDFor: DNDFor, ClearIn: ClearIn, Presets: presets, Emoji: emoji.All}
	chosen := Chosen(u)
	for _, c := range statusChoices {
		c.On = c.Status == chosen
		f.Choices = append(f.Choices, c)
	}
	if cs := u.GetCustomStatus(); cs != nil {
		f.CustomEmoji, f.CustomText = cs.Emoji, cs.Text
	}
	return f
}

// ParseStatus reads a status the form offers.
func ParseStatus(v string) (pb.Status, bool) {
	for _, c := range statusChoices {
		if c.Value == v {
			return c.Status, true
		}
	}
	return 0, false
}

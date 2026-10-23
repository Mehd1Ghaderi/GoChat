package render

import (
	"html/template"
	"time"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
)

type Message struct {
	*pb.Message
	Body      template.HTML
	Time      time.Time
	Edited    bool
	Mine      bool
	CanDelete bool
	OOB       bool
}

// Message prepares m for the viewer meID; moderators may delete any message.
func (r *Renderer) Message(m *pb.Message, meID string, moderator bool) *Message {
	mine := m.GetAuthor().GetId() == meID
	return &Message{
		Message:   m,
		Body:      r.Body(m),
		Time:      m.GetCreatedAt().AsTime(),
		Edited:    m.EditedAt != nil,
		Mine:      mine,
		CanDelete: mine || moderator,
	}
}

func (r *Renderer) Messages(list []*pb.Message, meID string, moderator bool) []*Message {
	out := make([]*Message, len(list))
	for i, m := range list {
		out[i] = r.Message(m, meID, moderator)
	}
	return out
}

type Rail struct {
	Teams []*pb.Team
	Team  *pb.Team
	OOB   bool
}

type Lists struct {
	Team     *pb.Team
	Channels []*pb.Channel
	DMs      []*pb.Channel
	Active   string
	Online   map[string]bool
	OOB      bool
}

// Split separates a team's channels from the direct ones.
func Split(all []*pb.Channel) (channels, direct []*pb.Channel) {
	for _, ch := range all {
		if ch.Type == pb.ChannelType_CHANNEL_TYPE_DIRECT {
			direct = append(direct, ch)
		} else {
			channels = append(channels, ch)
		}
	}
	return channels, direct
}

// Peers returns the other sides of direct channels.
func Peers(direct []*pb.Channel) []string {
	out := make([]string, 0, len(direct))
	for _, ch := range direct {
		if ch.Peer != nil {
			out = append(out, ch.Peer.Id)
		}
	}
	return out
}

type ChannelView struct {
	Team       *pb.Team
	Channel    *pb.Channel
	Title      string
	Member     bool
	Messages   []*Message
	HasMore    bool
	Oldest     string
	Newest     string
	Moderator  bool
	PeerOnline bool
}

func Title(ch *pb.Channel) string {
	if ch.Peer != nil {
		return DisplayName(ch.Peer)
	}
	return ch.DisplayName
}

type Typing struct {
	ChannelID string
	Name      string
}

type Presence struct {
	ID     string
	Online bool
}

type Toast struct {
	Kind string
	Text string
}

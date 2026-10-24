package render

import (
	"fmt"
	"html/template"
	"slices"
	"strings"
	"time"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/core/emoji"
	"github.com/ssenerg/gochat/internal/core/mention"
)

type Message struct {
	*pb.Message
	Body      template.HTML
	Time      time.Time
	Edited    bool
	Mine      bool
	CanDelete bool
	// Mentioned is set when the message calls out its reader, Focus when it
	// is the one a link opened.
	Mentioned bool
	Focus     bool
	OOB       bool
	bar       ReactionsView
}

// Message prepares m for the viewer meID; moderators may delete any message.
func (r *Renderer) Message(m *pb.Message, me *pb.User, moderator bool) *Message {
	mine := m.GetAuthor().GetId() == me.GetId()
	return &Message{
		Message:   m,
		Body:      r.Body(m),
		Time:      m.GetCreatedAt().AsTime(),
		Edited:    m.EditedAt != nil,
		Mine:      mine,
		CanDelete: mine || moderator,
		Mentioned: !mine && Mentions(m, me),
		bar:       Reactions(m.GetChannelId(), m.GetId(), m.GetRootId(), m.GetReactions(), me, false),
	}
}

func (r *Renderer) Messages(list []*pb.Message, me *pb.User, moderator bool) []*Message {
	out := make([]*Message, len(list))
	for i, m := range list {
		out[i] = r.Message(m, me, moderator)
	}
	return out
}

// Mentions reports whether m calls out me, by name or as one of everyone.
func Mentions(m *pb.Message, me *pb.User) bool {
	found := mention.Parse(m.GetContent())
	return found.Channel || found.Here || found.Of(me.GetUsername())
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
	// OpenThread is the root of a thread to open with the channel.
	OpenThread string
	// Anchored is set when newer messages than those shown exist: the view
	// shows an older part of the channel and takes no new messages.
	Anchored bool
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

// ReactionView is one emoji on a message as a reader sees it.
type ReactionView struct {
	Name  string
	Char  string
	Count int
	Mine  bool
	Who   string
}

// ReactionsView is a message's reaction bar.
type ReactionsView struct {
	ID   string
	URL  string
	List []ReactionView
	OOB  bool
}

// ReactURL is where a message's reactions are added and taken back.
func ReactURL(channelID, messageID, rootID string) string {
	u := "/m/" + channelID + "/" + messageID + "/react"
	if rootID != "" {
		u += "?root=" + rootID
	}
	return u
}

// Reactions prepares a message's reactions for me.
func Reactions(channelID, messageID, rootID string, list []*pb.Reaction, me *pb.User, oob bool) ReactionsView {
	out := ReactionsView{ID: messageID, URL: ReactURL(channelID, messageID, rootID), OOB: oob}
	for _, r := range list {
		char, ok := emoji.Char(r.Emoji)
		if !ok || len(r.UserIds) == 0 {
			continue
		}
		v := ReactionView{Name: r.Emoji, Char: char, Count: len(r.UserIds), Mine: slices.Contains(r.UserIds, me.GetId())}
		switch {
		case v.Mine && v.Count == 1:
			v.Who = "You reacted with :" + r.Emoji + ":"
		case v.Mine:
			v.Who = fmt.Sprintf("You and %d more reacted with :%s:", v.Count-1, r.Emoji)
		default:
			v.Who = fmt.Sprintf("%d reacted with :%s:", v.Count, r.Emoji)
		}
		out.List = append(out.List, v)
	}
	return out
}

// Bar is the message's reaction bar.
func (m *Message) Bar() ReactionsView {
	return m.bar
}

// key tells the reaction bars of two readers apart, for caching.
func (v ReactionsView) key() string {
	var b strings.Builder
	for _, r := range v.List {
		fmt.Fprintf(&b, "%s:%d:%t,", r.Name, r.Count, r.Mine)
	}
	return b.String()
}

// Notice is a desktop notification a tab shows for a message.
type Notice struct {
	Title, Body, URL, Tag string
}

const excerptRunes = 140

// Excerpt is a message's text, short and on one line, for a notification.
func Excerpt(m *pb.Message) string {
	text := strings.Join(strings.Fields(m.GetContent()), " ")
	if r := []rune(text); len(r) > excerptRunes {
		text = string(r[:excerptRunes]) + "…"
	}
	if text == "" {
		if n := len(m.GetAttachments()); n == 1 {
			text = "Sent a file"
		} else if n > 1 {
			text = fmt.Sprintf("Sent %d files", n)
		}
	}
	return text
}

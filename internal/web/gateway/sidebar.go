package gateway

import (
	"time"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/core/cache"
	"github.com/ssenerg/gochat/internal/core/secure"
	"github.com/ssenerg/gochat/internal/web/render"
)

// Sidebar is what a session keeps of a sidebar: the channels it lists and
// how it marks them.
type Sidebar struct {
	userID, teamID string
	items          []sidebarItem
}

type sidebarItem struct {
	id, name, peer string
	mentions       int
	muted, direct  bool
	// unread is set when the sidebar marks the channel unread.
	unread bool
}

// NewSidebar takes the state of the sidebar lists render for userID.
func NewSidebar(userID string, lists *render.Lists) *Sidebar {
	sb := &Sidebar{userID: userID, teamID: lists.Team.GetId(), items: make([]sidebarItem, 0, len(lists.Channels)+len(lists.DMs))}
	for _, list := range [][]*pb.Channel{lists.Channels, lists.DMs} {
		for _, ch := range list {
			it := sidebarItem{
				id: ch.Id, mentions: int(ch.MentionCount), muted: ch.Muted, direct: render.Conversation(ch),
				unread: render.Unread(ch) && ch.Id != lists.Active,
			}
			if !it.direct || ch.Type == pb.ChannelType_CHANNEL_TYPE_GROUP {
				it.name = render.Title(ch)
			}
			if ch.Peer != nil {
				it.peer = ch.Peer.Id
			}
			sb.items = append(sb.items, it)
		}
	}
	return sb
}

const (
	sidebarsKept = 2048
	sidebarTTL   = 30 * time.Second
)

// Sidebars holds the sidebars pages were rendered with, until their socket
// takes them rather than load them again.
type Sidebars struct {
	kept *cache.Cache[string, *Sidebar]
}

func NewSidebars() *Sidebars {
	return &Sidebars{kept: cache.New[string, *Sidebar](sidebarsKept, sidebarTTL)}
}

// Put keeps sb, returning the key the page hands its socket.
func (s *Sidebars) Put(sb *Sidebar) string {
	key := secure.Token()
	s.kept.Set(key, sb)
	return key
}

// take returns the sidebar of key when it is userID's of teamID, once.
func (s *Sidebars) take(key, userID, teamID string) *Sidebar {
	if s == nil || !secure.ValidToken(key) {
		return nil
	}
	sb, ok := s.kept.Take(key)
	if !ok || sb.userID != userID || sb.teamID != teamID {
		return nil
	}
	return sb
}

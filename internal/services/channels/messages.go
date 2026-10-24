package channels

import (
	"github.com/gocql/gocql"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/core/models"
)

// Request is a message for one channel's actor.
type Request interface {
	channel() gocql.UUID
}

type Target struct {
	P         *models.Principal
	ChannelID gocql.UUID
}

func (t Target) channel() gocql.UUID { return t.ChannelID }

// Access replies with *AccessResult: the channel and the caller's role in it.
type Access struct{ Target }

type AccessResult struct {
	Channel *models.Channel
	// Role is empty when the caller is not a member.
	Role models.ChannelRole
}

// View replies with the caller's *pb.Channel.
type View struct{ Target }

// Post writes a message, or with RootID a reply in that message's thread.
type Post struct {
	Target
	Content       string
	AttachmentIDs []string
	RootID        string
}

// Edit, Delete and GetMessage name a reply by its RootID as well.
type Edit struct {
	Target
	MessageID string
	RootID    string
	Content   string
}

type Delete struct {
	Target
	MessageID string
	RootID    string
}

// History replies with *HistoryResult: the messages before BeforeID, or
// with AfterID those just after it, newest first either way.
type History struct {
	Target
	BeforeID string
	AfterID  string
	Limit    int
}

type HistoryResult struct {
	Messages []*pb.Message
	HasMore  bool
}

type GetMessage struct {
	Target
	MessageID string
	RootID    string
}

// Thread replies with *ThreadResult.
type Thread struct {
	Target
	RootID   string
	BeforeID string
	Limit    int
}

type ThreadResult struct {
	Root *pb.Message
	// Replies are oldest first.
	Replies []*pb.Message
	HasMore bool
}

// React adds the caller's reaction to a message, or with Remove takes it
// back. It replies with the message's reactions.
type React struct {
	Target
	MessageID string
	RootID    string
	Emoji     string
	Remove    bool
}

// Mute mutes the channel for the caller, or with Muted false unmutes it.
type Mute struct {
	Target
	Muted bool
}

type Typing struct{ Target }

type MarkRead struct {
	Target
	MessageID string
}

// Join adds the caller to a public channel. System joins skip the permission
// check but still require team membership.
type Join struct {
	Target
	System bool
}

type Leave struct{ Target }

type AddMember struct {
	Target
	UserID string
}

// RemoveMember removes UserID. System removals skip the permission check.
type RemoveMember struct {
	Target
	UserID gocql.UUID
	System bool
}

// Members replies with []*pb.ChannelMember.
type Members struct{ Target }

type Done struct{}

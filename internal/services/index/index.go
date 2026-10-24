// Package index files every message under the words it holds and answers
// searches over them. A message is filed under its team, or under each
// person of a direct conversation, by month: a search reads a team's and
// the searcher's own partitions, newest month first, and keeps only what
// the searcher may read.
package index

import (
	"context"
	"hash/fnv"
	"strconv"
	"time"

	"github.com/anthdm/hollywood/actor"
	"github.com/gocql/gocql"
	"go.uber.org/zap"

	"github.com/ssenerg/gochat/internal/actors"
	"github.com/ssenerg/gochat/internal/core/models"
	"github.com/ssenerg/gochat/internal/core/search"
	"github.com/ssenerg/gochat/internal/repos/scylla"
	"github.com/ssenerg/gochat/internal/services/users"
)

type Repo interface {
	AddPostings(ctx context.Context, scopes []gocql.UUID, terms []string, p scylla.Posting) error
	RemovePostings(ctx context.Context, scopes []gocql.UUID, terms []string, messageID gocql.UUID) error
	Postings(ctx context.Context, scope gocql.UUID, term string, month int32, before gocql.UUID, limit int) ([]scylla.Posting, error)
	HasPostings(ctx context.Context, scope gocql.UUID, term string, month int32, messageIDs []gocql.UUID) (map[gocql.UUID]bool, error)
	GetTeam(ctx context.Context, id gocql.UUID) (*models.Team, error)
	GetTeamMember(ctx context.Context, teamID, userID gocql.UUID) (*models.TeamMember, error)
	ListUserChannels(ctx context.Context, userID, teamID gocql.UUID) ([]*models.ChannelMember, error)
	ListChannelMembers(ctx context.Context, channelID gocql.UUID) ([]*models.ChannelMember, error)
	GetChannels(ctx context.Context, ids []gocql.UUID) ([]*models.Channel, error)
	GetChannelIDByName(ctx context.Context, teamID gocql.UUID, name string) (gocql.UUID, error)
	GetUserIDByUsername(ctx context.Context, username string) (gocql.UUID, error)
	GetMessage(ctx context.Context, channelID, id gocql.UUID) (*models.Message, error)
	GetReply(ctx context.Context, channelID, rootID, id gocql.UUID) (*models.Message, error)
	ListMessages(ctx context.Context, channelID, before gocql.UUID, limit int) ([]*models.Message, bool, error)
	ListReplies(ctx context.Context, channelID, rootID, before gocql.UUID, limit int) ([]*models.Message, bool, error)
	ChannelsAfter(ctx context.Context, after gocql.UUID, limit int) ([]*models.Channel, error)
	GetState(ctx context.Context, key string) (string, error)
	SetState(ctx context.Context, key, value string) error
}

type Deps struct {
	Logger *zap.Logger
	Cfg    *Config
	Repo   Repo
	Users  *users.Cache
	limits *limiter
}

// Doc is a message as the index files it.
type Doc struct {
	Scopes  []gocql.UUID
	Posting scylla.Posting
	Content string
}

func (d Doc) id() gocql.UUID { return d.Posting.MessageID }

// Add files a new message, Update an edited one that held Old, and Remove
// takes a deleted one out.
type (
	Add    struct{ Doc }
	Update struct {
		Doc
		Old string
	}
	Remove struct{ Doc }
)

type filed interface{ id() gocql.UUID }

// Scopes are where a channel's messages are filed: its team, or each person
// of a direct conversation.
func Scopes(ch *models.Channel, members []gocql.UUID) []gocql.UUID {
	if ch.Kind == models.ChannelDirect {
		return members
	}
	return []gocql.UUID{ch.TeamID}
}

// Writer files messages. Each message goes to one filer by its id, so the
// changes to it apply in the order they were made.
func Writer(d *Deps) actor.Producer {
	return func() actor.Receiver { return &writer{Deps: d} }
}

type writer struct {
	*Deps
	filers []*actor.PID
}

func (w *writer) Receive(c *actor.Context) {
	switch msg := c.Message().(type) {
	case actor.Started:
		w.filers = make([]*actor.PID, w.Cfg.Writers)
		for i := range w.filers {
			w.filers[i] = c.SpawnChild(func() actor.Receiver { return &filer{Deps: w.Deps} }, "filer",
				actor.WithID(strconv.Itoa(i)), actor.WithMiddleware(actors.Recover(w.Logger)))
		}
	case filed:
		if len(w.filers) == 0 {
			return
		}
		h := fnv.New32a()
		id := msg.id()
		_, _ = h.Write(id[:])
		c.Send(w.filers[h.Sum32()%uint32(len(w.filers))], msg) //nolint:gosec // writers is small
	}
}

type filer struct{ *Deps }

func (f *filer) Receive(c *actor.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), f.Cfg.OpTimeout)
	defer cancel()
	var err error
	switch msg := c.Message().(type) {
	case Add:
		err = f.Repo.AddPostings(ctx, msg.Scopes, search.Terms(msg.Content), msg.Posting)
	case Update:
		terms := search.Terms(msg.Content)
		if err = f.Repo.AddPostings(ctx, msg.Scopes, terms, msg.Posting); err == nil {
			err = f.Repo.RemovePostings(ctx, msg.Scopes, gone(search.Terms(msg.Old), terms), msg.Posting.MessageID)
		}
	case Remove:
		err = f.Repo.RemovePostings(ctx, msg.Scopes, search.Terms(msg.Content), msg.Posting.MessageID)
	default:
		return
	}
	if err != nil {
		f.Logger.Warn("failed to file a message for search", zap.Error(err))
	}
}

// gone returns the terms of old missing from now.
func gone(old, now []string) []string {
	have := make(map[string]bool, len(now))
	for _, t := range now {
		have[t] = true
	}
	var out []string
	for _, t := range old {
		if !have[t] {
			out = append(out, t)
		}
	}
	return out
}

// sleep waits d, or less when ctx ends, and reports whether ctx is live.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

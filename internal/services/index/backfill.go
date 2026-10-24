package index

import (
	"context"
	"time"

	"github.com/anthdm/hollywood/actor"
	"github.com/gocql/gocql"
	"go.uber.org/zap"

	"github.com/ssenerg/gochat/internal/core/ids"
	"github.com/ssenerg/gochat/internal/core/models"
	"github.com/ssenerg/gochat/internal/core/search"
	"github.com/ssenerg/gochat/internal/repos/scylla"
)

// backfillKey holds "done", or the last channel filed by a backfill under
// way. A change of what search.Term makes of words needs a new key.
const (
	backfillKey  = "search.backfill.v1"
	backfillDone = "done"
	retryAfter   = 30 * time.Second
)

// Backfill files the messages written before search existed, a channel at
// a time, once. Messages written meanwhile are filed as they come.
func Backfill(d *Deps) actor.Producer {
	return func() actor.Receiver { return &backfill{Deps: d} }
}

type backfill struct {
	*Deps
	cancel context.CancelFunc
}

func (b *backfill) Receive(c *actor.Context) {
	switch c.Message().(type) {
	case actor.Started:
		ctx, cancel := context.WithCancel(context.Background())
		b.cancel = cancel
		go b.run(ctx)
	case actor.Stopped:
		if b.cancel != nil {
			b.cancel()
		}
	}
}

func (b *backfill) op(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, b.Cfg.OpTimeout)
}

func (b *backfill) run(ctx context.Context) {
	var state string
	for {
		octx, cancel := b.op(ctx)
		s, err := b.Repo.GetState(octx, backfillKey)
		cancel()
		if err == nil {
			state = s
			break
		}
		b.Logger.Warn("failed to read the search backfill's state", zap.Error(err))
		if !sleep(ctx, retryAfter) {
			return
		}
	}
	if state == backfillDone {
		return
	}
	after, _ := ids.Parse(state)
	b.Logger.Info("filing earlier messages for search")
	for ctx.Err() == nil {
		octx, cancel := b.op(ctx)
		page, err := b.Repo.ChannelsAfter(octx, after, 50)
		cancel()
		if err != nil {
			b.Logger.Warn("failed to list channels to file", zap.Error(err))
			sleep(ctx, retryAfter)
			continue
		}
		if len(page) == 0 {
			b.save(ctx, backfillDone)
			b.Logger.Info("earlier messages filed for search")
			return
		}
		for _, ch := range page {
			if err := b.channel(ctx, ch); err != nil {
				if ctx.Err() == nil {
					b.Logger.Warn("failed to file a channel's messages", zap.String("channel", ch.ID.String()), zap.Error(err))
					sleep(ctx, retryAfter)
				}
				break
			}
			after = ch.ID
			b.save(ctx, after.String())
		}
	}
}

func (b *backfill) save(ctx context.Context, value string) {
	octx, cancel := b.op(ctx)
	defer cancel()
	if err := b.Repo.SetState(octx, backfillKey, value); err != nil {
		b.Logger.Warn("failed to save the search backfill's state", zap.Error(err))
	}
}

func (b *backfill) channel(ctx context.Context, ch *models.Channel) error {
	octx, cancel := b.op(ctx)
	members, err := b.Repo.ListChannelMembers(octx, ch.ID)
	cancel()
	if err != nil {
		return err
	}
	ids := make([]gocql.UUID, len(members))
	for i, m := range members {
		ids[i] = m.UserID
	}
	scopes := Scopes(ch, ids)
	var before gocql.UUID
	for {
		octx, cancel := b.op(ctx)
		list, more, err := b.Repo.ListMessages(octx, ch.ID, before, pageSize)
		cancel()
		if err != nil {
			return err
		}
		for _, m := range list {
			if err := b.file(ctx, scopes, m); err != nil {
				return err
			}
			if m.ReplyCount > 0 {
				if err := b.thread(ctx, scopes, m); err != nil {
					return err
				}
			}
		}
		if !more || len(list) == 0 {
			return nil
		}
		before = list[len(list)-1].ID
		if !sleep(ctx, 10*time.Millisecond) {
			return ctx.Err()
		}
	}
}

func (b *backfill) thread(ctx context.Context, scopes []gocql.UUID, root *models.Message) error {
	var before gocql.UUID
	for {
		octx, cancel := b.op(ctx)
		list, more, err := b.Repo.ListReplies(octx, root.ChannelID, root.ID, before, pageSize)
		cancel()
		if err != nil {
			return err
		}
		for _, m := range list {
			if err := b.file(ctx, scopes, m); err != nil {
				return err
			}
		}
		if !more || len(list) == 0 {
			return nil
		}
		before = list[len(list)-1].ID
	}
}

func (b *backfill) file(ctx context.Context, scopes []gocql.UUID, m *models.Message) error {
	octx, cancel := b.op(ctx)
	defer cancel()
	return b.Repo.AddPostings(octx, scopes, search.Terms(m.Content), scylla.Posting{
		MessageID: m.ID, ChannelID: m.ChannelID, RootID: m.RootID, UserID: m.UserID,
	})
}

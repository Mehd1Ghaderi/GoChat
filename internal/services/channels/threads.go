package channels

import (
	"context"
	"time"

	"github.com/anthdm/hollywood/actor"
	"github.com/gocql/gocql"
	"go.uber.org/zap"

	"github.com/ssenerg/gochat/internal/core/ids"
	ierrors "github.com/ssenerg/gochat/internal/errors"
)

// touch records, off the actor, a reply to rootID, made at the time given,
// for the thread's followers; the replier has read it.
func (a *channel) touch(followers []gocql.UUID, rootID gocql.UUID, at time.Time, replier gocql.UUID) {
	teamID := a.ch.TeamID
	go func() {
		ctx, cancel := a.ctx()
		defer cancel()
		if err := a.Repo.TouchThread(ctx, followers, a.id, rootID, teamID, at, replier); err != nil {
			a.Logger.Warn("failed to note a thread's reply", zap.String("channel", a.id.String()), zap.Error(err))
		}
	}()
}

func (a *channel) threadRead(c *actor.Context, msg ThreadRead) error {
	if _, err := a.member(msg.P); err != nil {
		return err
	}
	rootID, ok := ids.ParseTime(msg.RootID)
	if !ok {
		return ierrors.InvalidArgument("invalid message id")
	}
	userID := msg.P.UserID
	a.async(c, func(ctx context.Context) (any, error) {
		return Done{}, a.Repo.MarkThreadRead(ctx, userID, a.id, rootID, time.Now().UTC())
	})
	return nil
}

func (a *channel) follow(msg Follow) (Done, error) {
	if _, err := a.member(msg.P); err != nil {
		return Done{}, err
	}
	ctx, cancel := a.ctx()
	defer cancel()
	root, err := a.messageOf(ctx, msg.RootID, "")
	if err != nil {
		return Done{}, err
	}
	if !msg.Follow {
		return Done{}, a.Repo.UnfollowThreads(ctx, []gocql.UUID{msg.P.UserID}, a.id, root.ID)
	}
	last := root.LastReplyAt
	if last.IsZero() {
		last = root.ID.Time()
	}
	return Done{}, a.Repo.TouchThread(ctx, []gocql.UUID{msg.P.UserID}, a.id, root.ID, a.ch.TeamID, last, msg.P.UserID)
}

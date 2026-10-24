package directory

import (
	"context"

	"github.com/gocql/gocql"
	"golang.org/x/sync/errgroup"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/actors"
	"github.com/ssenerg/gochat/internal/core/ids"
	ierrors "github.com/ssenerg/gochat/internal/errors"
	"github.com/ssenerg/gochat/internal/repos/scylla"
	"github.com/ssenerg/gochat/internal/services/convert"
	"github.com/ssenerg/gochat/internal/services/hub"
)

const maxPresence = 200

// presence answers for those of msg's people the caller shares a
// conversation or a team with, and no one else.
func (w *worker) presence(ctx context.Context, msg Presence) (map[string]pb.Status, error) {
	if len(msg.UserIDs) > maxPresence {
		return nil, ierrors.InvalidArgument("too many users")
	}
	want := make(map[gocql.UUID]bool, len(msg.UserIDs))
	for _, raw := range msg.UserIDs {
		id, ok := ids.Parse(raw)
		if !ok {
			return nil, ierrors.InvalidArgument("invalid user id")
		}
		want[id] = true
	}
	known, err := w.acquainted(ctx, msg.P.UserID, want)
	if err != nil {
		return nil, err
	}
	if len(known) == 0 {
		return map[string]pb.Status{}, nil
	}
	return actors.Ask[map[string]pb.Status](ctx, w.Engine, w.Hub, hub.Statuses{UserIDs: known})
}

// acquainted returns those of want userID shares a conversation or a team
// with. Conversations go first: they are whom pages ask about.
func (w *worker) acquainted(ctx context.Context, userID gocql.UUID, want map[gocql.UUID]bool) ([]string, error) {
	out := make([]string, 0, len(want))
	take := func(id gocql.UUID) {
		if want[id] {
			delete(want, id)
			out = append(out, id.String())
		}
	}
	take(userID)
	if len(want) == 0 {
		return out, nil
	}
	convs, err := w.Repo.ListUserChannels(ctx, userID, scylla.NoTeam)
	if err != nil {
		return nil, err
	}
	chIDs := make([]gocql.UUID, len(convs))
	for i, m := range convs {
		chIDs[i] = m.ChannelID
	}
	for start := 0; start < len(chIDs) && len(want) > 0; start += 100 {
		chans, err := w.Repo.GetChannels(ctx, chIDs[start:min(start+100, len(chIDs))])
		if err != nil {
			return nil, err
		}
		for _, ch := range chans {
			for _, id := range convert.PeerIDs(ch, userID) {
				take(id)
			}
		}
	}
	if len(want) == 0 {
		return out, nil
	}
	mine, err := w.Repo.ListUserTeams(ctx, userID)
	if err != nil {
		return nil, err
	}
	teams := make(map[gocql.UUID]bool, len(mine))
	for _, m := range mine {
		teams[m.TeamID] = true
	}
	rest := make([]gocql.UUID, 0, len(want))
	for id := range want {
		rest = append(rest, id)
	}
	shared := make([]bool, len(rest))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(8)
	for i, id := range rest {
		g.Go(func() error {
			theirs, err := w.Repo.ListUserTeams(gctx, id)
			for _, m := range theirs {
				shared[i] = shared[i] || teams[m.TeamID]
			}
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	for i, id := range rest {
		if shared[i] {
			take(id)
		}
	}
	return out, nil
}

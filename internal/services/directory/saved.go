package directory

import (
	"context"
	"slices"

	"github.com/gocql/gocql"
	"golang.org/x/sync/errgroup"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/core/ids"
	"github.com/ssenerg/gochat/internal/core/models"
	ierrors "github.com/ssenerg/gochat/internal/errors"
	"github.com/ssenerg/gochat/internal/repos/scylla"
	"github.com/ssenerg/gochat/internal/services/convert"
)

const (
	// maxSaved bounds the saves read for one person.
	maxSaved     = 1000
	maxSavedShow = 100
)

// listSaved lists what the caller saved in a team's channels and in their
// conversations, newest first, keeping only what they may still read.
func (w *worker) listSaved(ctx context.Context, msg ListSaved) (*SavedResult, error) {
	saves, err := w.Repo.ListSaved(ctx, msg.P.UserID, maxSaved)
	if err != nil {
		return nil, err
	}
	if msg.IDsOnly {
		out := &SavedResult{IDs: make([]string, len(saves))}
		for i, s := range saves {
			out.IDs[i] = s.MessageID.String()
		}
		return out, nil
	}
	teamID, err := parseID(msg.TeamID, "team")
	if err != nil {
		return nil, err
	}
	if _, err := w.membership(ctx, teamID, msg.P.UserID); err != nil {
		return nil, err
	}
	mine, err := w.Repo.ListUserChannels(ctx, msg.P.UserID, teamID)
	if err != nil {
		return nil, err
	}
	direct, err := w.Repo.ListUserChannels(ctx, msg.P.UserID, scylla.NoTeam)
	if err != nil {
		return nil, err
	}
	member := make(map[gocql.UUID]bool, len(mine)+len(direct))
	for _, m := range append(mine, direct...) {
		member[m.ChannelID] = true
	}
	var page []*scylla.Saved
	for _, s := range saves {
		if member[s.ChannelID] && (s.TeamID == teamID || s.TeamID == scylla.NoTeam) {
			if page = append(page, s); len(page) == maxSavedShow {
				break
			}
		}
	}
	list := make([]*models.Message, len(page))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(8)
	for i, s := range page {
		g.Go(func() error {
			var (
				m   *models.Message
				err error
			)
			if ids.IsZero(s.RootID) {
				m, err = w.Repo.GetMessage(gctx, s.ChannelID, s.MessageID)
			} else {
				m, err = w.Repo.GetReply(gctx, s.ChannelID, s.RootID, s.MessageID)
			}
			if ierrors.Is(err, ierrors.CodeNotFound) {
				return nil
			}
			list[i] = m
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	var chIDs, userIDs []gocql.UUID
	for _, m := range list {
		if m == nil {
			continue
		}
		if !slices.Contains(chIDs, m.ChannelID) {
			chIDs = append(chIDs, m.ChannelID)
		}
		userIDs = append(userIDs, m.UserID)
	}
	chans, err := w.Repo.GetChannels(ctx, chIDs)
	if err != nil {
		return nil, err
	}
	for _, ch := range chans {
		userIDs = append(userIDs, convert.PeerIDs(ch, msg.P.UserID)...)
	}
	people, err := w.Users.GetMany(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	views := make(map[gocql.UUID]*pb.Channel, len(chans))
	for _, ch := range chans {
		views[ch.ID] = convert.Conversation(ch, "", gocql.UUID{}, msg.P.UserID, people)
	}
	out := &SavedResult{}
	for _, m := range list {
		if m == nil || views[m.ChannelID] == nil {
			continue
		}
		hit := &pb.SearchHit{Message: convert.Message(m, convert.PublicUser(people[m.UserID]), nil), Channel: views[m.ChannelID]}
		hit.Message.Saved = true
		out.Hits = append(out.Hits, hit)
	}
	return out, nil
}

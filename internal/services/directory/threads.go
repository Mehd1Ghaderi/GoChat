package directory

import (
	"context"
	"slices"

	"github.com/gocql/gocql"
	"golang.org/x/sync/errgroup"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/core/models"
	ierrors "github.com/ssenerg/gochat/internal/errors"
	"github.com/ssenerg/gochat/internal/repos/scylla"
	"github.com/ssenerg/gochat/internal/services/convert"
)

const (
	// maxFollows bounds the threads read to list one person's.
	maxFollows       = 2000
	maxThreadPage    = 50
	shownParticipant = 4
)

// listThreads lists the threads the caller follows in a team and in their
// conversations, latest reply first, with how many are unread.
func (w *worker) listThreads(ctx context.Context, msg ListThreads) (*ThreadsResult, error) {
	teamID, err := parseID(msg.TeamID, "team")
	if err != nil {
		return nil, err
	}
	if _, err := w.membership(ctx, teamID, msg.P.UserID); err != nil {
		return nil, err
	}
	follows, err := w.Repo.ListThreadFollows(ctx, msg.P.UserID, maxFollows)
	if err != nil {
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
	kept := follows[:0]
	out := &ThreadsResult{}
	for _, f := range follows {
		if f.LastReplyAt.IsZero() || !member[f.ChannelID] || (f.TeamID != teamID && f.TeamID != scylla.NoTeam) {
			continue
		}
		kept = append(kept, f)
		if f.LastReplyAt.After(f.ReadAt) {
			out.Unread++
		}
	}
	if msg.Limit <= 0 {
		return out, nil
	}
	slices.SortFunc(kept, func(a, b *scylla.ThreadFollow) int { return b.LastReplyAt.Compare(a.LastReplyAt) })
	start := min(max(msg.Offset, 0), len(kept))
	end := min(start+min(msg.Limit, maxThreadPage), len(kept))
	if end < len(kept) {
		out.Next = end
	}
	page := kept[start:end]
	roots := make([]*models.Message, len(page))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(8)
	for i, f := range page {
		g.Go(func() error {
			m, err := w.Repo.GetMessage(gctx, f.ChannelID, f.RootID)
			if ierrors.Is(err, ierrors.CodeNotFound) {
				return nil
			}
			roots[i] = m
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	var chIDs, userIDs []gocql.UUID
	for _, m := range roots {
		if m == nil {
			continue
		}
		if !slices.Contains(chIDs, m.ChannelID) {
			chIDs = append(chIDs, m.ChannelID)
		}
		userIDs = append(append(userIDs, m.UserID), m.Participants[:min(len(m.Participants), shownParticipant)]...)
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
	for i, m := range roots {
		if m == nil || views[m.ChannelID] == nil {
			continue
		}
		s := &pb.ThreadSummary{
			Root:    convert.Message(m, convert.PublicUser(people[m.UserID]), nil),
			Channel: views[m.ChannelID],
			Unread:  page[i].LastReplyAt.After(page[i].ReadAt),
		}
		seen := map[gocql.UUID]bool{}
		for _, id := range append([]gocql.UUID{m.UserID}, m.Participants...) {
			if u := people[id]; u != nil && !seen[id] && len(s.Participants) < shownParticipant {
				seen[id] = true
				s.Participants = append(s.Participants, convert.PublicUser(u))
			}
		}
		out.Threads = append(out.Threads, s)
	}
	return out, nil
}

package directory

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/anthdm/hollywood/actor"
	"github.com/gocql/gocql"
	"go.uber.org/zap"

	"github.com/ssenerg/gochat/internal/actors"
	"github.com/ssenerg/gochat/internal/core/ids"
	"github.com/ssenerg/gochat/internal/core/models"
	"github.com/ssenerg/gochat/internal/services/channels"
)

// adoptKey holds "done", or the last channel an adoption under way looked
// at.
const (
	adoptKey   = "conversations.teams.v1"
	adoptDone  = "done"
	adoptRetry = 30 * time.Second
	adoptPage  = 50
)

// Adopt gives each conversation from before teams owned them to one team,
// once: the team holding the most of its people, then the one they have
// shared longest. Its people outside that team leave it. One that no team
// holds two of its people in stays out of every team.
func Adopt(d *Deps) actor.Producer {
	return func() actor.Receiver { return &adopter{Deps: d} }
}

type adopter struct {
	*Deps
	cancel context.CancelFunc
}

func (a *adopter) Receive(c *actor.Context) {
	switch c.Message().(type) {
	case actor.Started:
		ctx, cancel := context.WithCancel(context.Background())
		a.cancel = cancel
		go a.run(ctx)
	case actor.Stopped:
		if a.cancel != nil {
			a.cancel()
		}
	}
}

func (a *adopter) op(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, a.Cfg.OpTimeout)
}

func (a *adopter) run(ctx context.Context) {
	var state string
	for {
		octx, cancel := a.op(ctx)
		s, err := a.Repo.GetState(octx, adoptKey)
		cancel()
		if err == nil {
			state = s
			break
		}
		a.Logger.Warn("failed to read the conversations' adoption state", zap.Error(err))
		if !sleep(ctx, adoptRetry) {
			return
		}
	}
	if state == adoptDone {
		return
	}
	after, _ := ids.Parse(state)
	a.Logger.Info("giving earlier conversations to teams")
	for ctx.Err() == nil {
		octx, cancel := a.op(ctx)
		page, err := a.Repo.ChannelsAfter(octx, after, adoptPage)
		cancel()
		if err != nil {
			a.Logger.Warn("failed to list channels to adopt", zap.Error(err))
			sleep(ctx, adoptRetry)
			continue
		}
		if len(page) == 0 {
			a.save(ctx, adoptDone)
			a.Logger.Info("earlier conversations given to teams")
			return
		}
		for _, ch := range page {
			if err := a.adopt(ctx, ch); err != nil {
				if ctx.Err() == nil {
					a.Logger.Warn("failed to adopt a conversation", zap.String("channel", ch.ID.String()), zap.Error(err))
					sleep(ctx, adoptRetry)
				}
				break
			}
			after = ch.ID
		}
		if !ids.IsZero(after) {
			a.save(ctx, after.String())
		}
	}
}

func (a *adopter) save(ctx context.Context, value string) {
	octx, cancel := a.op(ctx)
	defer cancel()
	if err := a.Repo.SetState(octx, adoptKey, value); err != nil {
		a.Logger.Warn("failed to save the conversations' adoption state", zap.Error(err))
	}
}

func (a *adopter) adopt(ctx context.Context, ch *models.Channel) error {
	if !ch.Kind.Conversation() || !ids.IsZero(ch.TeamID) {
		return nil
	}
	octx, cancel := a.op(ctx)
	defer cancel()
	members, err := a.Repo.ListChannelMembers(octx, ch.ID)
	if err != nil {
		return err
	}
	people := make(map[gocql.UUID][]*models.TeamMember, len(members))
	for _, m := range members {
		if people[m.UserID], err = a.Repo.ListUserTeams(octx, m.UserID); err != nil {
			return err
		}
	}
	for _, teamID := range homeTeams(people) {
		holder, err := a.Repo.ClaimDirectKey(octx, conversationKey(teamID, ch.Name), ch.ID)
		if err != nil {
			return err
		}
		// Its people opened a new one there meanwhile.
		if holder != ch.ID {
			continue
		}
		move := channels.Move{Target: channels.Target{ChannelID: ch.ID}, TeamID: teamID}
		_, err = actors.Ask[channels.Done](octx, a.Engine, a.Channels, move)
		return err
	}
	a.Logger.Info("left a conversation out of every team", zap.String("channel", ch.ID.String()))
	return nil
}

// homeTeams orders the teams holding two or more of a conversation's
// people, given each person's teams: the most of them first, then the one
// they have shared longest.
func homeTeams(people map[gocql.UUID][]*models.TeamMember) []gocql.UUID {
	type home struct {
		id    gocql.UUID
		count int
		// since is when the last of them joined.
		since time.Time
	}
	byTeam := make(map[gocql.UUID]*home)
	for _, teams := range people {
		for _, m := range teams {
			h := byTeam[m.TeamID]
			if h == nil {
				h = &home{id: m.TeamID}
				byTeam[m.TeamID] = h
			}
			h.count++
			if m.JoinedAt.After(h.since) {
				h.since = m.JoinedAt
			}
		}
	}
	var homes []*home
	for _, h := range byTeam {
		if h.count >= 2 {
			homes = append(homes, h)
		}
	}
	slices.SortFunc(homes, func(a, b *home) int {
		if c := cmp.Compare(b.count, a.count); c != 0 {
			return c
		}
		if c := a.since.Compare(b.since); c != 0 {
			return c
		}
		return strings.Compare(a.id.String(), b.id.String())
	})
	out := make([]gocql.UUID, len(homes))
	for i, h := range homes {
		out[i] = h.id
	}
	return out
}

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

package web

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"strings"

	"golang.org/x/sync/errgroup"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/web/render"
)

const maxSwitch = 12

// switchItem is one place the quick switcher can go: a channel or
// conversation to open, a person to message or a channel to join.
type switchItem struct {
	Title    string
	Channel  *pb.Channel
	User     *pb.User
	URL      string
	PostURL  string
	Hint     string
	Mentions int32
	Unread   bool
	rank     int
}

type switcherView struct {
	Team  *pb.Team
	Items []switchItem
}

func (h *Handler) switcher(w http.ResponseWriter, r *http.Request) {
	u, team, ctx, cancel := h.teamFor(w, r)
	if team == nil {
		return
	}
	defer cancel()
	items, err := h.switchItems(ctx, u, team, "")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.fragment(w, r, http.StatusOK, "switcher", switcherView{Team: team, Items: items})
}

func (h *Handler) switcherResults(w http.ResponseWriter, r *http.Request) {
	u, team, ctx, cancel := h.teamFor(w, r)
	if team == nil {
		return
	}
	defer cancel()
	items, err := h.switchItems(ctx, u, team, r.URL.Query().Get("q"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.fragment(w, r, http.StatusOK, "switch-results", switcherView{Team: team, Items: items})
}

// rankOf scores how well q names title: lower is better, -1 not at all.
func rankOf(title, q string) int {
	t := strings.ToLower(title)
	switch {
	case strings.HasPrefix(t, q):
		return 0
	case strings.Contains(t, " "+q) || strings.Contains(t, "-"+q):
		return 1
	case strings.Contains(t, q):
		return 2
	}
	return -1
}

// best is the better of two ranks, -1 when neither matched.
func best(a, b int) int {
	switch {
	case a < 0:
		return b
	case b < 0:
		return a
	}
	return min(a, b)
}

// switchItems lists where q leads: the caller's channels and conversations,
// then, for a query, people to message and public channels to join.
func (h *Handler) switchItems(ctx context.Context, u *pb.User, team *pb.Team, q string) ([]switchItem, error) {
	q = strings.ToLower(strings.TrimSpace(strings.TrimLeft(q, "#@~")))
	if len(q) > 64 {
		q = q[:64]
	}
	g, gctx := errgroup.WithContext(ctx)
	var (
		mine   []*pb.Channel
		people []*pb.TeamMember
		open   []*pb.Channel
	)
	g.Go(func() error {
		res, err := h.client.Channels.ListChannels(gctx, &pb.ListChannelsRequest{TeamId: team.Id})
		if err == nil {
			mine = res.Channels
		}
		return err
	})
	if q != "" {
		g.Go(func() (err error) {
			people, err = h.searchPeople(gctx, team, q, u.Id)
			return
		})
		g.Go(func() error {
			res, err := h.client.Channels.BrowseChannels(gctx, &pb.BrowseChannelsRequest{TeamId: team.Id})
			if err == nil {
				open = res.Channels
			}
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	base := "/t/" + team.Name
	var items []switchItem
	direct := map[string]bool{}
	for _, ch := range mine {
		if ch.Peer != nil {
			direct[ch.Peer.Id] = true
		}
		title := render.Title(ch)
		rank := 0
		if q != "" {
			// A conversation's name is its people's ids: only its title counts.
			if rank = rankOf(title, q); !render.Conversation(ch) {
				rank = best(rank, rankOf(ch.Name, q))
			}
			if rank < 0 {
				continue
			}
		} else if ch.MentionCount == 0 && !render.Unread(ch) {
			rank = 1
		}
		items = append(items, switchItem{
			Title: title, Channel: ch, User: ch.Peer, URL: base + "/ch/" + ch.Id,
			Mentions: ch.MentionCount, Unread: render.Unread(ch) && !ch.Muted, rank: rank,
		})
	}
	slices.SortStableFunc(items, func(a, b switchItem) int {
		if c := cmp.Compare(a.rank, b.rank); c != 0 {
			return c
		}
		return cmp.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title))
	})
	for _, m := range people {
		if !direct[m.User.Id] {
			items = append(items, switchItem{Title: render.DisplayName(m.User), User: m.User, PostURL: base + "/dm", Hint: "@" + m.User.Username})
		}
	}
	for _, ch := range open {
		if best(rankOf(ch.DisplayName, q), rankOf(ch.Name, q)) >= 0 {
			items = append(items, switchItem{Title: ch.DisplayName, Channel: ch, PostURL: base + "/ch/" + ch.Id + "/join", Hint: "Join"})
		}
	}
	return items[:min(len(items), maxSwitch)], nil
}

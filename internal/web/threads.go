package web

import (
	"context"
	"net/http"
	"strconv"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/web/render"
)

const threadsPage = 30

type threadsView struct {
	Team    *pb.Team
	Threads []render.ThreadCard
	MoreURL string
}

func (h *Handler) loadThreads(ctx context.Context, team *pb.Team, offset int) (*threadsView, error) {
	res, err := h.client.Messages.ListThreads(ctx, &pb.ListThreadsRequest{TeamId: team.Id, Offset: uint32(offset), Limit: threadsPage}) //nolint:gosec // offset is small
	if err != nil {
		return nil, err
	}
	view := &threadsView{Team: team, Threads: render.ThreadCards(res.Threads)}
	if res.NextOffset > 0 {
		view.MoreURL = "/t/" + team.Name + "/threads?offset=" + strconv.FormatUint(uint64(res.NextOffset), 10)
	}
	return view, nil
}

// threadsPage shows the threads the person follows: the whole page, the
// view for #main, a further page with ?offset, or the list with ?list.
func (h *Handler) threadsPage(w http.ResponseWriter, r *http.Request) {
	u := h.signedIn(w, r)
	if u == nil {
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	q := r.URL.Query()
	offset, _ := strconv.Atoi(q.Get("offset"))
	if offset < 0 || offset > 10_000 {
		offset = 0
	}
	team, err := h.team(ctx, r.PathValue("team"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	view, err := h.loadThreads(ctx, team, offset)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	switch {
	case q.Has("offset"):
		h.fragment(w, r, http.StatusOK, "threads-page", view)
		return
	case q.Get("list") == "1":
		h.fragment(w, r, http.StatusOK, "threads-list", view)
		return
	case isHTMX(r) && r.Header.Get("HX-Target") == "main" && r.Header.Get("HX-History-Restore-Request") != "true":
		h.fragment(w, r, http.StatusOK, "threads", view)
		return
	}
	res, err := h.client.Teams.ListTeams(ctx, &pb.ListTeamsRequest{})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	lists, err := h.lists(ctx, u, team, "threads")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.page(w, r, http.StatusOK, "app", appPage{
		Page:    Page{Title: "Threads", CSRF: h.csrfToken(w, r), User: u},
		Teams:   res.Teams,
		Team:    team,
		Lists:   lists,
		Threads: view,
	})
}

// followThread follows a thread, or with follow=0 stops, and answers with
// the button that toggles it.
func (h *Handler) followThread(w http.ResponseWriter, r *http.Request) {
	if h.signedIn(w, r) == nil {
		return
	}
	chID, rootID := r.PathValue("channel"), r.PathValue("message")
	if !uuidRe.MatchString(chID) || !uuidRe.MatchString(rootID) {
		h.notFound(w, r)
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	follow := r.PostFormValue("follow") == "1"
	if _, err := h.client.Messages.FollowThread(ctx, &pb.FollowThreadRequest{ChannelId: chID, RootId: rootID, Follow: follow}); err != nil {
		h.fail(w, r, err)
		return
	}
	h.fragment(w, r, http.StatusOK, "follow-button", map[string]any{"Channel": chID, "Root": rootID, "Following": follow})
}

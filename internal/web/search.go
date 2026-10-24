package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/client/drpc"
	ierrors "github.com/ssenerg/gochat/internal/errors"
	"github.com/ssenerg/gochat/internal/web/render"
)

const (
	searchPage     = 20
	maxSearchBytes = 256
)

type searchView struct {
	Team    *pb.Team
	Query   string
	Hits    []render.Hit
	MoreURL string
	Error   string
}

// searchPanel shows the search pane, and with ?q the first results.
func (h *Handler) searchPanel(w http.ResponseWriter, r *http.Request) {
	_, team, ctx, cancel := h.teamFor(w, r)
	if team == nil {
		return
	}
	defer cancel()
	view := searchView{Team: team, Query: strings.TrimSpace(r.URL.Query().Get("q"))}
	if view.Query != "" {
		if err := h.search(ctx, &view, ""); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	h.fragment(w, r, http.StatusOK, "search", view)
}

// searchMore answers with the next page of results.
func (h *Handler) searchMore(w http.ResponseWriter, r *http.Request) {
	_, team, ctx, cancel := h.teamFor(w, r)
	if team == nil {
		return
	}
	defer cancel()
	q, cursor := strings.TrimSpace(r.URL.Query().Get("q")), r.URL.Query().Get("cursor")
	if q == "" || !uuidRe.MatchString(cursor) {
		h.notFound(w, r)
		return
	}
	view := searchView{Team: team, Query: q}
	if err := h.search(ctx, &view, cursor); err != nil {
		h.fail(w, r, err)
		return
	}
	h.fragment(w, r, http.StatusOK, "search-page", view)
}

// search fills view with a page of results; a search the server refuses
// becomes the view's error.
func (h *Handler) search(ctx context.Context, view *searchView, cursor string) error {
	if len(view.Query) > maxSearchBytes {
		view.Error = "The search is too long."
		return nil
	}
	res, err := h.client.Messages.SearchMessages(ctx, &pb.SearchMessagesRequest{
		TeamId: view.Team.Id, Query: view.Query, Cursor: cursor, Limit: searchPage,
	})
	if err != nil {
		switch e := drpc.Err(err); e.Code {
		case ierrors.CodeInvalidArgument, ierrors.CodeResourceExhausted:
			view.Error = sentence(e.Message)
			return nil
		}
		return err
	}
	view.Hits = render.Hits(view.Team, res.Hits, res.Terms)
	if res.NextCursor != "" {
		view.MoreURL = "/t/" + view.Team.Name + "/search/more?" + url.Values{"q": {view.Query}, "cursor": {res.NextCursor}}.Encode()
	}
	return nil
}

func sentence(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:] + "."
}

// msgParam is the valid ?msg of a request, or empty.
func msgParam(r *http.Request) string {
	if m := r.URL.Query().Get("msg"); uuidRe.MatchString(m) {
		return m
	}
	return ""
}

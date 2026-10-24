package web

import (
	"net/http"
	"slices"
	"strconv"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	ierrors "github.com/ssenerg/gochat/internal/errors"
	"github.com/ssenerg/gochat/internal/web/render"
)

func (h *Handler) statusModal(w http.ResponseWriter, r *http.Request) {
	u := h.signedIn(w, r)
	if u == nil {
		return
	}
	h.fragment(w, r, http.StatusOK, "status-modal", render.NewStatusForm(u))
}

// setStatus saves the availability and custom status the caller chose.
func (h *Handler) setStatus(w http.ResponseWriter, r *http.Request) {
	if h.signedIn(w, r) == nil {
		return
	}
	status, ok := render.ParseStatus(r.PostFormValue("status"))
	dnd, okDND := minutes(r.PostFormValue("dnd"), render.DNDFor)
	clearIn, okClear := minutes(r.PostFormValue("clear"), render.ClearIn)
	if !ok || !okDND || !okClear {
		h.fail(w, r, ierrors.InvalidArgument("unknown choice"))
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	if _, err := h.client.Users.SetStatus(ctx, &pb.SetStatusRequest{Status: status, DndMinutes: dnd}); err != nil {
		h.fail(w, r, err)
		return
	}
	res, err := h.client.Users.SetCustomStatus(ctx, &pb.SetCustomStatusRequest{
		Emoji: r.PostFormValue("emoji"), Text: r.PostFormValue("text"), Minutes: clearIn,
	})
	h.me.Delete(h.sessionToken(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("HX-Trigger", "closeModal")
	h.fragment(w, r, http.StatusOK, "status-saved", res.User)
}

// minutes reads one of the durations offered.
func minutes(v string, offered []render.Option) (int32, bool) {
	n, err := strconv.Atoi(v)
	if err != nil || !slices.ContainsFunc(offered, func(o render.Option) bool { return o.Value == n }) {
		return 0, false
	}
	return int32(n), true //nolint:gosec // one of the offered values
}

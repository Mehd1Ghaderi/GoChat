package web

import (
	"encoding/json"
	"net/http"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	ierrors "github.com/ssenerg/gochat/internal/errors"
)

// joinVoice answers the page's voice client with where to connect and the
// token to identify with there.
func (h *Handler) joinVoice(w http.ResponseWriter, r *http.Request) {
	if h.signedIn(w, r) == nil {
		return
	}
	chID := r.PostFormValue("channel_id")
	if !uuidRe.MatchString(chID) {
		h.fail(w, r, ierrors.InvalidArgument("invalid channel id"))
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	res, err := h.client.Voice.JoinVoice(ctx, &pb.JoinVoiceRequest{ChannelId: chID})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Endpoint  string `json:"endpoint"`
		Token     string `json:"token"`
		ChannelID string `json:"channel_id"`
		Name      string `json:"name"`
	}{res.Endpoint, res.Token, res.Channel.GetId(), res.Channel.GetDisplayName()})
}

func (h *Handler) leaveVoice(w http.ResponseWriter, r *http.Request) {
	if h.signedIn(w, r) == nil {
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	if _, err := h.client.Voice.LeaveVoice(ctx, &pb.LeaveVoiceRequest{}); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) voiceState(w http.ResponseWriter, r *http.Request) {
	if h.signedIn(w, r) == nil {
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	req := &pb.UpdateVoiceStateRequest{
		SelfMute: r.PostFormValue("self_mute") == "1", SelfDeaf: r.PostFormValue("self_deaf") == "1",
		SelfVideo: r.PostFormValue("self_video") == "1", SelfStream: r.PostFormValue("self_stream") == "1",
	}
	if _, err := h.client.Voice.UpdateVoiceState(ctx, req); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// disconnectVoice takes someone out of a voice channel's talk, for its
// moderators.
func (h *Handler) disconnectVoice(w http.ResponseWriter, r *http.Request) {
	if h.signedIn(w, r) == nil {
		return
	}
	chID, userID := r.PathValue("channel"), r.PathValue("user")
	if !uuidRe.MatchString(chID) || !uuidRe.MatchString(userID) {
		h.notFound(w, r)
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	if _, err := h.client.Voice.DisconnectVoiceMember(ctx, &pb.DisconnectVoiceMemberRequest{ChannelId: chID, UserId: userID}); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

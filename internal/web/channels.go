package web

import (
	"net/http"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/client/drpc"
	ierrors "github.com/ssenerg/gochat/internal/errors"
	"github.com/ssenerg/gochat/internal/web/render"
)

type channelSettingsData struct {
	Team      *pb.Team
	Channel   *pb.Channel
	TeamAdmin bool
	Status    string
	Error     string
}

func teamAdmin(u *pb.User, team *pb.Team) bool {
	return team.GetRole() == pb.TeamRole_TEAM_ROLE_ADMIN || u.GetRole() == pb.SystemRole_SYSTEM_ROLE_ADMIN
}

// channelSettings shows the settings of a channel to those who manage it.
func (h *Handler) channelSettings(w http.ResponseWriter, r *http.Request) {
	u, team, ctx, cancel := h.teamFor(w, r)
	if team == nil {
		return
	}
	defer cancel()
	chID, ok := h.channelTarget(w, r)
	if !ok {
		return
	}
	res, err := h.client.Channels.GetChannel(ctx, &pb.GetChannelRequest{ChannelId: chID})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if !moderator(u, team, res.Channel) || render.Conversation(res.Channel) || res.Channel.GetType() == pb.ChannelType_CHANNEL_TYPE_VOICE {
		h.fail(w, r, ierrors.PermissionDenied())
		return
	}
	h.fragment(w, http.StatusOK, "channel-settings", channelSettingsData{Team: team, Channel: res.Channel, TeamAdmin: teamAdmin(u, team)})
}

func (h *Handler) updateChannel(w http.ResponseWriter, r *http.Request) {
	u, team, ctx, cancel := h.teamFor(w, r)
	if team == nil {
		return
	}
	defer cancel()
	chID, ok := h.channelTarget(w, r)
	if !ok {
		return
	}
	req := &pb.UpdateChannelRequest{ChannelId: chID, DisplayName: r.PostFormValue("display_name"), Name: r.PostFormValue("name"), Purpose: r.PostFormValue("purpose")}
	res, err := h.client.Channels.UpdateChannel(ctx, req)
	if err != nil {
		// What was typed is shown again with why; a refusal is not a form's.
		e := drpc.Err(err)
		if e.Code != ierrors.CodeInvalidArgument && e.Code != ierrors.CodeAlreadyExists && e.Code != ierrors.CodeFailedPrecondition {
			h.fail(w, r, err)
			return
		}
		msg := e.Message
		ch := &pb.Channel{Id: chID, DisplayName: req.DisplayName, Name: req.Name, Purpose: req.Purpose}
		if cur, gerr := h.client.Channels.GetChannel(ctx, &pb.GetChannelRequest{ChannelId: chID}); gerr == nil {
			ch.Type = cur.Channel.Type
		}
		h.fragment(w, http.StatusOK, "channel-settings", channelSettingsData{Team: team, Channel: ch, TeamAdmin: teamAdmin(u, team), Error: msg})
		return
	}
	h.fragment(w, http.StatusOK, "channel-settings", channelSettingsData{Team: team, Channel: res.Channel, TeamAdmin: teamAdmin(u, team), Status: "Saved."})
}

// setChannelVoice lets the channel take voice messages, or stops it.
func (h *Handler) setChannelVoice(w http.ResponseWriter, r *http.Request) {
	u, team, ctx, cancel := h.teamFor(w, r)
	if team == nil {
		return
	}
	defer cancel()
	chID, ok := h.channelTarget(w, r)
	if !ok {
		return
	}
	res, err := h.client.Channels.SetChannelVoice(ctx, &pb.SetChannelVoiceRequest{ChannelId: chID, Enabled: r.PostFormValue("enabled") == "1"})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.fragment(w, http.StatusOK, "channel-settings", channelSettingsData{Team: team, Channel: res.Channel, TeamAdmin: teamAdmin(u, team), Status: "Saved."})
}

func (h *Handler) channelPrivacy(w http.ResponseWriter, r *http.Request) {
	u, team, ctx, cancel := h.teamFor(w, r)
	if team == nil {
		return
	}
	defer cancel()
	chID, ok := h.channelTarget(w, r)
	if !ok {
		return
	}
	private := r.PostFormValue("private") == "1"
	res, err := h.client.Channels.SetChannelPrivacy(ctx, &pb.SetChannelPrivacyRequest{ChannelId: chID, Private: private})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	status := "The channel is public now."
	if private {
		status = "The channel is private now."
	}
	h.fragment(w, http.StatusOK, "channel-settings", channelSettingsData{Team: team, Channel: res.Channel, TeamAdmin: teamAdmin(u, team), Status: status})
}

// archiveChannel archives or restores a channel and shows it again.
func (h *Handler) archiveChannel(w http.ResponseWriter, r *http.Request) {
	_, team, ctx, cancel := h.teamFor(w, r)
	if team == nil {
		return
	}
	defer cancel()
	chID, ok := h.channelTarget(w, r)
	if !ok {
		return
	}
	if _, err := h.client.Channels.ArchiveChannel(ctx, &pb.ArchiveChannelRequest{ChannelId: chID, Archived: r.PostFormValue("archived") == "1"}); err != nil {
		h.fail(w, r, err)
		return
	}
	h.navigate(w, r, "/t/"+team.Name+"/ch/"+chID, true)
}

package web

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/client/drpc"
	ierrors "github.com/ssenerg/gochat/internal/errors"
	"github.com/ssenerg/gochat/internal/web/render"
)

const (
	pageSize       = 50
	defaultChannel = "town-square"
)

var (
	uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,49}$`)
)

type appPage struct {
	Page
	Teams   []*pb.Team
	Team    *pb.Team
	OOB     bool
	Lists   *render.Lists
	Channel *render.ChannelView
}

// signedIn returns the user, or answers with a sign-in redirect.
func (h *Handler) signedIn(w http.ResponseWriter, r *http.Request) *pb.User {
	u, err := h.currentUser(r)
	switch {
	case err != nil:
		h.fail(w, r, err)
	case u == nil:
		h.fail(w, r, ierrors.Unauthenticated())
	}
	return u
}

func (h *Handler) home(w http.ResponseWriter, r *http.Request) {
	if h.signedIn(w, r) == nil {
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	res, err := h.client.Teams.ListTeams(ctx, &pb.ListTeamsRequest{})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if len(res.Teams) == 0 {
		http.Redirect(w, r, "/teams/new", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/t/"+res.Teams[0].Name, http.StatusSeeOther)
}

type teamForm struct {
	Name, DisplayName, Description string
}

type noTeamData struct {
	Page
	CanCreate bool
	HasTeams  bool
	Form      teamForm
}

func (h *Handler) newTeamPage(w http.ResponseWriter, r *http.Request) {
	u := h.signedIn(w, r)
	if u == nil {
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	res, err := h.client.Teams.ListTeams(ctx, &pb.ListTeamsRequest{})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.page(w, r, http.StatusOK, "noteam", noTeamData{
		Page: Page{Title: "Create a team", CSRF: h.csrfToken(w, r), User: u}, CanCreate: true, HasTeams: len(res.Teams) > 0,
	})
}

func (h *Handler) createTeam(w http.ResponseWriter, r *http.Request) {
	u := h.signedIn(w, r)
	if u == nil {
		return
	}
	form := teamForm{Name: r.PostFormValue("name"), DisplayName: r.PostFormValue("display_name"), Description: r.PostFormValue("description")}
	ctx, cancel := h.ctx(r)
	defer cancel()
	res, err := h.client.Teams.CreateTeam(ctx, &pb.CreateTeamRequest{Name: form.Name, DisplayName: form.DisplayName, Description: form.Description})
	if err != nil {
		msg, ok := formError(err)
		if !ok {
			h.fail(w, r, err)
			return
		}
		h.page(w, r, statusOf(drpc.Err(err).Code), "noteam", noTeamData{
			Page: Page{Title: "Create a team", CSRF: h.csrfToken(w, r), User: u, Error: msg}, CanCreate: true, Form: form,
		})
		return
	}
	http.Redirect(w, r, "/t/"+res.Team.Name, http.StatusSeeOther)
}

func (h *Handler) team(ctx context.Context, name string) (*pb.Team, error) {
	if !slugRe.MatchString(name) {
		return nil, ierrors.NotFound("team not found")
	}
	res, err := h.client.Teams.GetTeam(ctx, &pb.GetTeamRequest{Key: &pb.GetTeamRequest_Name{Name: name}})
	if err != nil {
		return nil, err
	}
	return res.Team, nil
}

func (h *Handler) defaultChannel(ctx context.Context, team *pb.Team) (string, error) {
	res, err := h.client.Channels.ListChannels(ctx, &pb.ListChannelsRequest{TeamId: team.Id})
	if err != nil {
		return "", err
	}
	var first string
	for _, ch := range res.Channels {
		if ch.Type == pb.ChannelType_CHANNEL_TYPE_DIRECT {
			continue
		}
		if ch.Name == defaultChannel {
			return ch.Id, nil
		}
		if first == "" {
			first = ch.Id
		}
	}
	if first == "" {
		return "", ierrors.NotFound("you are in no channel of this team")
	}
	return first, nil
}

func (h *Handler) teamHome(w http.ResponseWriter, r *http.Request) {
	if h.signedIn(w, r) == nil {
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	team, err := h.team(ctx, r.PathValue("team"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	chID, err := h.defaultChannel(ctx, team)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.navigate(w, r, "/t/"+team.Name+"/ch/"+chID, false)
}

func moderator(u *pb.User, team *pb.Team, ch *pb.Channel) bool {
	return u.GetRole() == pb.SystemRole_SYSTEM_ROLE_ADMIN ||
		team.GetRole() == pb.TeamRole_TEAM_ROLE_ADMIN ||
		ch.GetRole() == pb.ChannelRole_CHANNEL_ROLE_ADMIN
}

// channelView loads one channel and its newest messages as u sees them.
func (h *Handler) channelView(ctx context.Context, u *pb.User, team *pb.Team, chID string) (*render.ChannelView, error) {
	g, gctx := errgroup.WithContext(ctx)
	var (
		ch   *pb.Channel
		msgs *pb.ListMessagesResponse
	)
	g.Go(func() error {
		res, err := h.client.Channels.GetChannel(gctx, &pb.GetChannelRequest{ChannelId: chID})
		if err != nil {
			return err
		}
		ch = res.Channel
		return nil
	})
	g.Go(func() error {
		res, err := h.client.Messages.ListMessages(gctx, &pb.ListMessagesRequest{ChannelId: chID, Limit: pageSize})
		if err != nil {
			if drpc.Err(err).Code == ierrors.CodePermissionDenied {
				return nil
			}
			return err
		}
		msgs = res
		return nil
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}
	if ch.TeamId != "" && ch.TeamId != team.Id {
		return nil, ierrors.NotFound("channel not found")
	}
	view := &render.ChannelView{
		Team:      team,
		Channel:   ch,
		Title:     render.Title(ch),
		Member:    ch.Role != pb.ChannelRole_CHANNEL_ROLE_UNSPECIFIED,
		Moderator: moderator(u, team, ch),
	}
	if msgs != nil {
		view.Messages = h.render.Messages(msgs.Messages, u.Id, view.Moderator)
		view.HasMore = msgs.HasMore
		if n := len(msgs.Messages); n > 0 {
			view.Newest, view.Oldest = msgs.Messages[0].Id, msgs.Messages[n-1].Id
		}
	}
	if ch.Peer != nil {
		if p, err := h.client.Users.GetPresence(ctx, &pb.GetPresenceRequest{UserIds: []string{ch.Peer.Id}}); err == nil {
			view.PeerOnline = len(p.OnlineUserIds) > 0
		}
	}
	return view, nil
}

func (h *Handler) channelPage(w http.ResponseWriter, r *http.Request) {
	u := h.signedIn(w, r)
	if u == nil {
		return
	}
	chID := r.PathValue("channel")
	if !uuidRe.MatchString(chID) {
		h.notFound(w, r)
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	partial := isHTMX(r) && r.Header.Get("HX-Target") == "main" && r.Header.Get("HX-History-Restore-Request") != "true"
	if partial {
		team, err := h.team(ctx, r.PathValue("team"))
		if err != nil {
			h.fail(w, r, err)
			return
		}
		view, err := h.channelView(ctx, u, team, chID)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		h.fragment(w, http.StatusOK, "channel", view)
		return
	}

	var (
		teams []*pb.Team
		team  *pb.Team
		lists *render.Lists
		view  *render.ChannelView
	)
	res, err := h.client.Teams.ListTeams(ctx, &pb.ListTeamsRequest{})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	teams = res.Teams
	for _, t := range teams {
		if t.Name == r.PathValue("team") {
			team = t
		}
	}
	if team == nil {
		h.notFound(w, r)
		return
	}
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		lists, err = h.lists(gctx, team, chID)
		return err
	})
	g.Go(func() error {
		var err error
		view, err = h.channelView(gctx, u, team, chID)
		return err
	})
	if err := g.Wait(); err != nil {
		h.fail(w, r, err)
		return
	}
	h.page(w, r, http.StatusOK, "app", appPage{
		Page:    Page{Title: view.Title, CSRF: h.csrfToken(w, r), User: u},
		Teams:   teams,
		Team:    team,
		Lists:   lists,
		Channel: view,
	})
}

func (h *Handler) lists(ctx context.Context, team *pb.Team, active string) (*render.Lists, error) {
	res, err := h.client.Channels.ListChannels(ctx, &pb.ListChannelsRequest{TeamId: team.Id})
	if err != nil {
		return nil, err
	}
	channels, direct := render.Split(res.Channels)
	online := map[string]bool{}
	if peers := render.Peers(direct); len(peers) > 0 {
		if p, err := h.client.Users.GetPresence(ctx, &pb.GetPresenceRequest{UserIds: peers}); err == nil {
			for _, id := range p.OnlineUserIds {
				online[id] = true
			}
		}
	}
	return &render.Lists{Team: team, Channels: channels, DMs: direct, Active: active, Online: online}, nil
}

func (h *Handler) olderMessages(w http.ResponseWriter, r *http.Request) {
	u := h.signedIn(w, r)
	if u == nil {
		return
	}
	chID, before := r.PathValue("channel"), r.URL.Query().Get("before")
	if !uuidRe.MatchString(chID) || !uuidRe.MatchString(before) {
		h.notFound(w, r)
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	g, gctx := errgroup.WithContext(ctx)
	var (
		team *pb.Team
		ch   *pb.Channel
		msgs *pb.ListMessagesResponse
	)
	g.Go(func() (err error) { team, err = h.team(gctx, r.PathValue("team")); return })
	g.Go(func() error {
		res, err := h.client.Channels.GetChannel(gctx, &pb.GetChannelRequest{ChannelId: chID})
		if err == nil {
			ch = res.Channel
		}
		return err
	})
	g.Go(func() (err error) {
		msgs, err = h.client.Messages.ListMessages(gctx, &pb.ListMessagesRequest{ChannelId: chID, BeforeId: before, Limit: pageSize})
		return
	})
	if err := g.Wait(); err != nil {
		h.fail(w, r, err)
		return
	}
	mod := moderator(u, team, ch)
	view := &render.ChannelView{Team: team, Channel: ch, Title: render.Title(ch), Member: true, Moderator: mod, HasMore: msgs.HasMore,
		Messages: h.render.Messages(msgs.Messages, u.Id, mod)}
	if n := len(msgs.Messages); n > 0 {
		view.Oldest = msgs.Messages[n-1].Id
	}
	h.fragment(w, http.StatusOK, "older-page", view)
}

func (h *Handler) teamFor(w http.ResponseWriter, r *http.Request) (*pb.User, *pb.Team, context.Context, context.CancelFunc) {
	u := h.signedIn(w, r)
	if u == nil {
		return nil, nil, nil, nil
	}
	ctx, cancel := h.ctx(r)
	team, err := h.team(ctx, r.PathValue("team"))
	if err != nil {
		cancel()
		h.fail(w, r, err)
		return nil, nil, nil, nil
	}
	return u, team, ctx, cancel
}

func (h *Handler) newChannelModal(w http.ResponseWriter, r *http.Request) {
	_, team, _, cancel := h.teamFor(w, r)
	if team == nil {
		return
	}
	defer cancel()
	h.fragment(w, http.StatusOK, "new-channel", struct{ Team *pb.Team }{team})
}

func (h *Handler) createChannel(w http.ResponseWriter, r *http.Request) {
	_, team, ctx, cancel := h.teamFor(w, r)
	if team == nil {
		return
	}
	defer cancel()
	kind := pb.ChannelType_CHANNEL_TYPE_PUBLIC
	if r.PostFormValue("type") == "private" {
		kind = pb.ChannelType_CHANNEL_TYPE_PRIVATE
	}
	res, err := h.client.Channels.CreateChannel(ctx, &pb.CreateChannelRequest{
		TeamId: team.Id, Name: r.PostFormValue("name"), DisplayName: r.PostFormValue("display_name"),
		Purpose: r.PostFormValue("purpose"), Type: kind,
	})
	if err != nil {
		if msg, ok := formError(err); ok {
			h.text(w, http.StatusOK, "error", msg)
			return
		}
		h.fail(w, r, err)
		return
	}
	h.navigate(w, r, "/t/"+team.Name+"/ch/"+res.Channel.Id, true)
}

func (h *Handler) browseModal(w http.ResponseWriter, r *http.Request) {
	_, team, ctx, cancel := h.teamFor(w, r)
	if team == nil {
		return
	}
	defer cancel()
	res, err := h.client.Channels.BrowseChannels(ctx, &pb.BrowseChannelsRequest{TeamId: team.Id})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.fragment(w, http.StatusOK, "browse", struct {
		Team     *pb.Team
		Channels []*pb.Channel
	}{team, res.Channels})
}

func (h *Handler) channelTarget(w http.ResponseWriter, r *http.Request) (string, bool) {
	chID := r.PathValue("channel")
	if !uuidRe.MatchString(chID) {
		h.notFound(w, r)
		return "", false
	}
	return chID, true
}

func (h *Handler) joinChannel(w http.ResponseWriter, r *http.Request) {
	_, team, ctx, cancel := h.teamFor(w, r)
	if team == nil {
		return
	}
	defer cancel()
	chID, ok := h.channelTarget(w, r)
	if !ok {
		return
	}
	if _, err := h.client.Channels.JoinChannel(ctx, &pb.JoinChannelRequest{ChannelId: chID}); err != nil {
		h.fail(w, r, err)
		return
	}
	h.navigate(w, r, "/t/"+team.Name+"/ch/"+chID, true)
}

func (h *Handler) leaveChannel(w http.ResponseWriter, r *http.Request) {
	_, team, ctx, cancel := h.teamFor(w, r)
	if team == nil {
		return
	}
	defer cancel()
	chID, ok := h.channelTarget(w, r)
	if !ok {
		return
	}
	if _, err := h.client.Channels.LeaveChannel(ctx, &pb.LeaveChannelRequest{ChannelId: chID}); err != nil {
		h.fail(w, r, err)
		return
	}
	next, err := h.defaultChannel(ctx, team)
	if err != nil {
		h.reload(w, r, "/")
		return
	}
	h.navigate(w, r, "/t/"+team.Name+"/ch/"+next, true)
}

type peopleData struct {
	Heading     string
	SearchURL   string
	ActionURL   string
	ActionLabel string
	Members     []*pb.TeamMember
}

func (h *Handler) searchPeople(ctx context.Context, team *pb.Team, query, exclude string) ([]*pb.TeamMember, error) {
	if len(query) > 64 {
		query = query[:64]
	}
	res, err := h.client.Teams.ListTeamMembers(ctx, &pb.ListTeamMembersRequest{TeamId: team.Id, Query: query, Limit: 25})
	if err != nil {
		return nil, err
	}
	out := res.Members[:0]
	for _, m := range res.Members {
		if m.User.Id != exclude {
			out = append(out, m)
		}
	}
	return out, nil
}

func (h *Handler) peopleModal(w http.ResponseWriter, r *http.Request) {
	u, team, ctx, cancel := h.teamFor(w, r)
	if team == nil {
		return
	}
	defer cancel()
	members, err := h.searchPeople(ctx, team, "", u.Id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.fragment(w, http.StatusOK, "people", peopleData{
		Heading: "Message someone", SearchURL: "/t/" + team.Name + "/people/search",
		ActionURL: "/t/" + team.Name + "/dm", ActionLabel: "Message", Members: members,
	})
}

func (h *Handler) peopleSearch(w http.ResponseWriter, r *http.Request) {
	u, team, ctx, cancel := h.teamFor(w, r)
	if team == nil {
		return
	}
	defer cancel()
	members, err := h.searchPeople(ctx, team, r.URL.Query().Get("q"), u.Id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.fragment(w, http.StatusOK, "people-results", peopleData{ActionURL: "/t/" + team.Name + "/dm", ActionLabel: "Message", Members: members})
}

func (h *Handler) openDirect(w http.ResponseWriter, r *http.Request) {
	_, team, ctx, cancel := h.teamFor(w, r)
	if team == nil {
		return
	}
	defer cancel()
	res, err := h.client.Channels.OpenDirectChannel(ctx, &pb.OpenDirectChannelRequest{UserId: r.PostFormValue("user_id")})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.navigate(w, r, "/t/"+team.Name+"/ch/"+res.Channel.Id, true)
}

type inviteListData struct {
	Team    *pb.Team
	Invites []*pb.InviteSummary
	OOB     bool
}

func (h *Handler) invites(ctx context.Context, team *pb.Team) (inviteListData, error) {
	res, err := h.client.Teams.ListInvites(ctx, &pb.ListInvitesRequest{TeamId: team.Id})
	if err != nil {
		return inviteListData{}, err
	}
	return inviteListData{Team: team, Invites: res.Invites}, nil
}

func (h *Handler) inviteModal(w http.ResponseWriter, r *http.Request) {
	_, team, ctx, cancel := h.teamFor(w, r)
	if team == nil {
		return
	}
	defer cancel()
	data, err := h.invites(ctx, team)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.fragment(w, http.StatusOK, "invite", data)
}

// createInvite shows the new link, once, and refreshes the open invites.
func (h *Handler) createInvite(w http.ResponseWriter, r *http.Request) {
	_, team, ctx, cancel := h.teamFor(w, r)
	if team == nil {
		return
	}
	defer cancel()
	res, err := h.client.Teams.CreateInvite(ctx, &pb.CreateInviteRequest{TeamId: team.Id})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var out strings.Builder
	err = h.render.Fragment(&out, "invite-link", struct {
		URL       string
		ExpiresAt time.Time
	}{h.cfg.PublicURL + "/join/" + res.Token, res.ExpiresAt.AsTime()})
	if err == nil {
		if data, lerr := h.invites(ctx, team); lerr == nil {
			data.OOB = true
			err = h.render.Fragment(&out, "invite-list", data)
		}
	}
	if err != nil {
		h.fail(w, r, ierrors.Internal(err))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, out.String())
}

// revokeInvite answers with the open invites, and why when the invite could
// not be revoked.
func (h *Handler) revokeInvite(w http.ResponseWriter, r *http.Request) {
	_, team, ctx, cancel := h.teamFor(w, r)
	if team == nil {
		return
	}
	defer cancel()
	_, err := h.client.Teams.RevokeInvite(ctx, &pb.RevokeInviteRequest{TeamId: team.Id, InviteId: r.PathValue("invite")})
	if err != nil {
		if code := drpc.Err(err).Code; code != ierrors.CodeFailedPrecondition && code != ierrors.CodeNotFound {
			h.fail(w, r, err)
			return
		}
	}
	data, lerr := h.invites(ctx, team)
	if lerr != nil {
		h.fail(w, r, lerr)
		return
	}
	var out strings.Builder
	rerr := h.render.Fragment(&out, "invite-list", data)
	if rerr == nil && err != nil {
		rerr = h.render.Fragment(&out, "toast", render.Toast{Kind: "error", Text: drpc.Err(err).Message})
	}
	if rerr != nil {
		h.fail(w, r, ierrors.Internal(rerr))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, out.String())
}

type teamMembersData struct {
	Team    *pb.Team
	Admin   bool
	MeID    string
	Members []*pb.TeamMember
}

func (h *Handler) renderTeamMembers(w http.ResponseWriter, r *http.Request, ctx context.Context, u *pb.User, team *pb.Team) {
	res, err := h.client.Teams.ListTeamMembers(ctx, &pb.ListTeamMembersRequest{TeamId: team.Id, Limit: 200})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.fragment(w, http.StatusOK, "team-members", teamMembersData{
		Team: team, Admin: team.Role == pb.TeamRole_TEAM_ROLE_ADMIN || u.Role == pb.SystemRole_SYSTEM_ROLE_ADMIN,
		MeID: u.Id, Members: res.Members,
	})
}

func (h *Handler) teamMembers(w http.ResponseWriter, r *http.Request) {
	u, team, ctx, cancel := h.teamFor(w, r)
	if team == nil {
		return
	}
	defer cancel()
	h.renderTeamMembers(w, r, ctx, u, team)
}

func (h *Handler) removeTeamMember(w http.ResponseWriter, r *http.Request) {
	_, team, ctx, cancel := h.teamFor(w, r)
	if team == nil {
		return
	}
	defer cancel()
	if _, err := h.client.Teams.RemoveTeamMember(ctx, &pb.RemoveTeamMemberRequest{TeamId: team.Id, UserId: r.PathValue("user")}); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) teamMemberRole(w http.ResponseWriter, r *http.Request) {
	u, team, ctx, cancel := h.teamFor(w, r)
	if team == nil {
		return
	}
	defer cancel()
	role := pb.TeamRole_TEAM_ROLE_MEMBER
	if r.PostFormValue("role") == "admin" {
		role = pb.TeamRole_TEAM_ROLE_ADMIN
	}
	if _, err := h.client.Teams.UpdateTeamMemberRole(ctx, &pb.UpdateTeamMemberRoleRequest{TeamId: team.Id, UserId: r.PathValue("user"), Role: role}); err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderTeamMembers(w, r, ctx, u, team)
}

type channelMembersData struct {
	Team      *pb.Team
	ChannelID string
	CanAdd    bool
	CanRemove bool
	Members   []*pb.ChannelMember
}

func (h *Handler) renderChannelMembers(w http.ResponseWriter, r *http.Request, ctx context.Context, u *pb.User, team *pb.Team, chID string) {
	g, gctx := errgroup.WithContext(ctx)
	var (
		ch      *pb.Channel
		members []*pb.ChannelMember
	)
	g.Go(func() error {
		res, err := h.client.Channels.GetChannel(gctx, &pb.GetChannelRequest{ChannelId: chID})
		if err == nil {
			ch = res.Channel
		}
		return err
	})
	g.Go(func() error {
		res, err := h.client.Channels.ListChannelMembers(gctx, &pb.ListChannelMembersRequest{ChannelId: chID})
		if err == nil {
			members = res.Members
		}
		return err
	})
	if err := g.Wait(); err != nil {
		h.fail(w, r, err)
		return
	}
	h.fragment(w, http.StatusOK, "channel-members", channelMembersData{
		Team: team, ChannelID: chID, Members: members,
		CanAdd:    ch.Type != pb.ChannelType_CHANNEL_TYPE_DIRECT,
		CanRemove: ch.Type != pb.ChannelType_CHANNEL_TYPE_DIRECT && moderator(u, team, ch),
	})
}

func (h *Handler) channelMembers(w http.ResponseWriter, r *http.Request) {
	u, team, ctx, cancel := h.teamFor(w, r)
	if team == nil {
		return
	}
	defer cancel()
	chID, ok := h.channelTarget(w, r)
	if !ok {
		return
	}
	h.renderChannelMembers(w, r, ctx, u, team, chID)
}

func (h *Handler) addMemberModal(w http.ResponseWriter, r *http.Request) {
	u, team, ctx, cancel := h.teamFor(w, r)
	if team == nil {
		return
	}
	defer cancel()
	chID, ok := h.channelTarget(w, r)
	if !ok {
		return
	}
	members, err := h.searchPeople(ctx, team, "", u.Id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	base := "/t/" + team.Name + "/ch/" + chID
	h.fragment(w, http.StatusOK, "people", peopleData{
		Heading: "Add people", SearchURL: base + "/add/search", ActionURL: base + "/members", ActionLabel: "Add", Members: members,
	})
}

func (h *Handler) addMemberSearch(w http.ResponseWriter, r *http.Request) {
	u, team, ctx, cancel := h.teamFor(w, r)
	if team == nil {
		return
	}
	defer cancel()
	chID, ok := h.channelTarget(w, r)
	if !ok {
		return
	}
	members, err := h.searchPeople(ctx, team, r.URL.Query().Get("q"), u.Id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.fragment(w, http.StatusOK, "people-results", peopleData{
		ActionURL: "/t/" + team.Name + "/ch/" + chID + "/members", ActionLabel: "Add", Members: members,
	})
}

func (h *Handler) addMember(w http.ResponseWriter, r *http.Request) {
	u, team, ctx, cancel := h.teamFor(w, r)
	if team == nil {
		return
	}
	defer cancel()
	chID, ok := h.channelTarget(w, r)
	if !ok {
		return
	}
	if _, err := h.client.Channels.AddChannelMember(ctx, &pb.AddChannelMemberRequest{ChannelId: chID, UserId: r.PostFormValue("user_id")}); err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("HX-Retarget", "#modal")
	w.Header().Set("HX-Reswap", "innerHTML")
	h.renderChannelMembers(w, r, ctx, u, team, chID)
}

func (h *Handler) removeMember(w http.ResponseWriter, r *http.Request) {
	_, team, ctx, cancel := h.teamFor(w, r)
	if team == nil {
		return
	}
	defer cancel()
	chID, ok := h.channelTarget(w, r)
	if !ok {
		return
	}
	if _, err := h.client.Channels.RemoveChannelMember(ctx, &pb.RemoveChannelMemberRequest{ChannelId: chID, UserId: r.PathValue("user")}); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// messageTarget reads a message's channel and id, and for a reply the id of
// its thread's root from the query.
func (h *Handler) messageTarget(w http.ResponseWriter, r *http.Request) (string, string, string, bool) {
	chID, msgID, rootID := r.PathValue("channel"), r.PathValue("message"), r.URL.Query().Get("root")
	if !uuidRe.MatchString(chID) || !uuidRe.MatchString(msgID) || (rootID != "" && !uuidRe.MatchString(rootID)) {
		h.notFound(w, r)
		return "", "", "", false
	}
	return chID, msgID, rootID, true
}

func (h *Handler) loadMessage(w http.ResponseWriter, r *http.Request) (*pb.User, *pb.Message) {
	u := h.signedIn(w, r)
	if u == nil {
		return nil, nil
	}
	chID, msgID, rootID, ok := h.messageTarget(w, r)
	if !ok {
		return nil, nil
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	res, err := h.client.Messages.GetMessage(ctx, &pb.GetMessageRequest{ChannelId: chID, MessageId: msgID, RootId: rootID})
	if err != nil {
		h.fail(w, r, err)
		return nil, nil
	}
	return u, res.Message
}

func (h *Handler) messageFragment(w http.ResponseWriter, r *http.Request) {
	u, m := h.loadMessage(w, r)
	if m == nil {
		return
	}
	h.fragment(w, http.StatusOK, "message", h.render.Message(m, u.Id, false))
}

func (h *Handler) editMessageForm(w http.ResponseWriter, r *http.Request) {
	u, m := h.loadMessage(w, r)
	if m == nil {
		return
	}
	if m.GetAuthor().GetId() != u.Id {
		h.fail(w, r, ierrors.PermissionDenied())
		return
	}
	h.fragment(w, http.StatusOK, "message-edit", m)
}

func (h *Handler) editMessage(w http.ResponseWriter, r *http.Request) {
	u := h.signedIn(w, r)
	if u == nil {
		return
	}
	chID, msgID, rootID, ok := h.messageTarget(w, r)
	if !ok {
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	res, err := h.client.Messages.EditMessage(ctx, &pb.EditMessageRequest{ChannelId: chID, MessageId: msgID, RootId: rootID, Content: r.PostFormValue("content")})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.fragment(w, http.StatusOK, "message", h.render.Message(res.Message, u.Id, false))
}

func (h *Handler) deleteMessage(w http.ResponseWriter, r *http.Request) {
	if h.signedIn(w, r) == nil {
		return
	}
	chID, msgID, rootID, ok := h.messageTarget(w, r)
	if !ok {
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	if _, err := h.client.Messages.DeleteMessage(ctx, &pb.DeleteMessageRequest{ChannelId: chID, MessageId: msgID, RootId: rootID}); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

type threadView struct {
	Root    *render.Message
	Replies []*render.Message
	HasMore bool
	Oldest  string
	Title   string
	Member  bool
}

// threadPanel shows a thread, or with ?before its older replies.
func (h *Handler) threadPanel(w http.ResponseWriter, r *http.Request) {
	u := h.signedIn(w, r)
	if u == nil {
		return
	}
	chID, rootID, before := r.PathValue("channel"), r.PathValue("message"), r.URL.Query().Get("before")
	if !uuidRe.MatchString(chID) || !uuidRe.MatchString(rootID) || (before != "" && !uuidRe.MatchString(before)) {
		h.notFound(w, r)
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	g, gctx := errgroup.WithContext(ctx)
	var (
		ch     *pb.Channel
		team   *pb.Team
		thread *pb.ListThreadResponse
	)
	g.Go(func() error {
		res, err := h.client.Channels.GetChannel(gctx, &pb.GetChannelRequest{ChannelId: chID})
		if err != nil {
			return err
		}
		ch = res.Channel
		if ch.TeamId != "" {
			t, err := h.client.Teams.GetTeam(gctx, &pb.GetTeamRequest{Key: &pb.GetTeamRequest_Id{Id: ch.TeamId}})
			if err != nil {
				return err
			}
			team = t.Team
		}
		return nil
	})
	g.Go(func() (err error) {
		thread, err = h.client.Messages.ListThread(gctx, &pb.ListThreadRequest{ChannelId: chID, RootId: rootID, BeforeId: before, Limit: pageSize})
		return
	})
	if err := g.Wait(); err != nil {
		h.fail(w, r, err)
		return
	}
	mod := moderator(u, team, ch)
	view := threadView{
		Root:    h.render.Message(thread.Root, u.Id, mod),
		Replies: h.render.Messages(thread.Replies, u.Id, mod),
		HasMore: thread.HasMore,
		Title:   render.Title(ch),
		Member:  ch.Role != pb.ChannelRole_CHANNEL_ROLE_UNSPECIFIED,
	}
	if len(thread.Replies) > 0 {
		view.Oldest = thread.Replies[0].Id
	}
	if before != "" {
		h.fragment(w, http.StatusOK, "older-replies-page", view)
		return
	}
	h.fragment(w, http.StatusOK, "thread", view)
}

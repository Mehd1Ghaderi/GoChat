package channels

import (
	"context"
	"time"

	"github.com/gocql/gocql"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/authz"
	"github.com/ssenerg/gochat/internal/core/models"
	"github.com/ssenerg/gochat/internal/core/validate"
	ierrors "github.com/ssenerg/gochat/internal/errors"
	"github.com/ssenerg/gochat/internal/services/convert"
)

// Settings renames a channel and sets its purpose, Privacy makes it public
// or private, and Archive archives or restores it. Each replies with the
// channel as the caller sees it.
type (
	Settings struct {
		Target
		DisplayName, Name, Purpose string
	}
	Privacy struct {
		Target
		Private bool
	}
	Archive struct {
		Target
		Archived bool
	}
)

var errArchived = ierrors.FailedPrecondition("this channel is archived")

func (a *channel) archived() bool {
	return !a.ch.ArchivedAt.IsZero()
}

// manage checks p may act on the channel itself.
func (a *channel) manage(ctx context.Context, p *models.Principal, act authz.Action) error {
	if a.ch.Kind.Conversation() {
		return ierrors.FailedPrecondition("conversations have no settings")
	}
	role, member := a.members[p.UserID]
	tr, err := a.teamRole(ctx, p.UserID)
	if err != nil {
		return err
	}
	if !member && a.ch.Kind != models.ChannelPublic && p.Role != models.SystemRoleAdmin {
		return ierrors.NotFound("channel not found")
	}
	if !a.Authz.Can(authz.ObjChannel, act, string(role), string(tr), string(p.Role)) {
		return ierrors.PermissionDenied()
	}
	return nil
}

func (a *channel) settings(msg Settings) (*pb.Channel, error) {
	ctx, cancel := a.ctx()
	defer cancel()
	if err := a.manage(ctx, msg.P, authz.ActUpdate); err != nil {
		return nil, err
	}
	if a.archived() {
		return nil, errArchived
	}
	display, err := validate.Line("display name", msg.DisplayName, 1, 64)
	if err != nil {
		return nil, err
	}
	purpose, err := validate.Line("purpose", msg.Purpose, 0, 250)
	if err != nil {
		return nil, err
	}
	name, err := validate.Slug(msg.Name)
	if err != nil {
		return nil, err
	}
	if name != a.ch.Name {
		if a.ch.Name == DefaultChannel {
			return nil, ierrors.FailedPrecondition("the town square keeps its URL name")
		}
		if err := a.Repo.RenameChannel(ctx, a.ch, name); err != nil {
			return nil, err
		}
		a.ch.Name = name
	}
	if err := a.Repo.UpdateChannelInfo(ctx, a.id, display, purpose); err != nil {
		return nil, err
	}
	a.ch.DisplayName, a.ch.Purpose = display, purpose
	return a.changed(msg.P)
}

func (a *channel) privacy(msg Privacy) (*pb.Channel, error) {
	ctx, cancel := a.ctx()
	defer cancel()
	kind, act := models.ChannelPublic, authz.ActMakePublic
	if msg.Private {
		kind, act = models.ChannelPrivate, authz.ActMakePrivate
	}
	if err := a.manage(ctx, msg.P, act); err != nil {
		return nil, err
	}
	if a.ch.Kind == kind {
		return a.channelFor(msg.P.UserID, a.members[msg.P.UserID])
	}
	if a.ch.Name == DefaultChannel {
		return nil, ierrors.FailedPrecondition("the town square stays open to the whole team")
	}
	if err := a.Repo.SetChannelKind(ctx, a.ch, kind); err != nil {
		return nil, err
	}
	a.ch.Kind = kind
	return a.changed(msg.P)
}

func (a *channel) archive(msg Archive) (*pb.Channel, error) {
	ctx, cancel := a.ctx()
	defer cancel()
	if err := a.manage(ctx, msg.P, authz.ActArchive); err != nil {
		return nil, err
	}
	if a.archived() == msg.Archived {
		return a.channelFor(msg.P.UserID, a.members[msg.P.UserID])
	}
	if a.ch.Name == DefaultChannel {
		return nil, ierrors.FailedPrecondition("the town square cannot be archived")
	}
	var at time.Time
	if msg.Archived {
		at = time.Now().UTC()
	}
	if err := a.Repo.SetArchived(ctx, a.id, at); err != nil {
		return nil, err
	}
	a.ch.ArchivedAt = at
	delete(a.typing, msg.P.UserID)
	return a.changed(msg.P)
}

// setVoice lets the channel's members post voice messages, or stops them.
// Messages posted before stay as they are.
func (a *channel) setVoice(msg SetVoice) (*pb.Channel, error) {
	ctx, cancel := a.ctx()
	defer cancel()
	if err := a.manage(ctx, msg.P, authz.ActUpdate); err != nil {
		return nil, err
	}
	if a.archived() {
		return nil, errArchived
	}
	// Each change reaches every member.
	if !a.poster(msg.P.UserID).Allow() {
		return nil, ierrors.ResourceExhausted("you are changing settings too fast")
	}
	disabled := !msg.Enabled
	if disabled == a.ch.VoiceDisabled {
		return a.channelFor(msg.P.UserID, a.members[msg.P.UserID])
	}
	if err := a.Repo.SetVoiceDisabled(ctx, a.id, disabled); err != nil {
		return nil, err
	}
	a.ch.VoiceDisabled = disabled
	return a.changed(msg.P)
}

// changed tells the members about the channel's new state and answers p.
func (a *channel) changed(p *models.Principal) (*pb.Channel, error) {
	ch := convert.Channel(a.ch, "", gocql.UUID{}, nil)
	ch.PinnedCount = int32(a.pins) //nolint:gosec // at most maxPins
	a.broadcast(&pb.Event{Payload: &pb.Event_ChannelUpdated{ChannelUpdated: &pb.ChannelUpdated{Channel: ch}}})
	return a.channelFor(p.UserID, a.members[p.UserID])
}

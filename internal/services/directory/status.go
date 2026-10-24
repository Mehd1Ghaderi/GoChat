package directory

import (
	"context"
	"time"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/core/emoji"
	"github.com/ssenerg/gochat/internal/core/validate"
	ierrors "github.com/ssenerg/gochat/internal/errors"
	"github.com/ssenerg/gochat/internal/services/convert"
	"github.com/ssenerg/gochat/internal/services/hub"
)

const (
	// maxStatusMinutes bounds how long a status lasts before it clears.
	maxStatusMinutes = 7 * 24 * 60
	maxStatusText    = 100
	defaultEmoji     = "speech_balloon"
)

var statusNames = map[pb.Status]string{
	pb.Status_STATUS_UNSPECIFIED: "",
	pb.Status_STATUS_ONLINE:      "",
	pb.Status_STATUS_AWAY:        "away",
	pb.Status_STATUS_DND:         "dnd",
	pb.Status_STATUS_OFFLINE:     "offline",
}

func (w *worker) setStatus(ctx context.Context, msg SetStatus) (*pb.User, error) {
	name, ok := statusNames[msg.Status]
	if !ok {
		return nil, ierrors.InvalidArgument("unknown status")
	}
	if msg.DNDMinutes < 0 || msg.DNDMinutes > maxStatusMinutes {
		return nil, ierrors.InvalidArgument("do not disturb lasts at most a week")
	}
	var until time.Time
	if name == "dnd" && msg.DNDMinutes > 0 {
		until = time.Now().UTC().Add(time.Duration(msg.DNDMinutes) * time.Minute).Truncate(time.Millisecond)
	}
	if err := w.Repo.SetStatus(ctx, msg.P.UserID, name, until); err != nil {
		return nil, err
	}
	w.Users.Invalidate(msg.P.UserID)
	w.Engine.Send(w.Hub, hub.SetStatus{UserID: msg.P.UserID.String(), Status: convert.Status(name), DNDUntil: until})
	u, err := w.Repo.GetUser(ctx, msg.P.UserID)
	if err != nil {
		return nil, err
	}
	return convert.User(u), nil
}

// setCustomStatus sets what the caller says they are up to, or with no
// emoji and no text clears it, and tells everyone.
func (w *worker) setCustomStatus(ctx context.Context, msg SetCustomStatus) (*pb.User, error) {
	text, err := validate.Line("status", msg.Text, 0, maxStatusText)
	if err != nil {
		return nil, err
	}
	name := msg.Emoji
	if name != "" && !emoji.Valid(name) {
		return nil, ierrors.InvalidArgument("unknown emoji")
	}
	if name == "" && text != "" {
		name = defaultEmoji
	}
	if msg.Minutes < 0 || msg.Minutes > maxStatusMinutes {
		return nil, ierrors.InvalidArgument("a status clears itself within a week")
	}
	var until time.Time
	if name != "" && msg.Minutes > 0 {
		until = time.Now().UTC().Add(time.Duration(msg.Minutes) * time.Minute).Truncate(time.Millisecond)
	}
	if err := w.Repo.SetCustomStatus(ctx, msg.P.UserID, name, text, until); err != nil {
		return nil, err
	}
	w.Users.Invalidate(msg.P.UserID)
	u, err := w.Repo.GetUser(ctx, msg.P.UserID)
	if err != nil {
		return nil, err
	}
	w.Engine.Send(w.Hub, hub.Broadcast{Event: &pb.Event{Broadcast: true, Payload: &pb.Event_ProfileChanged{
		ProfileChanged: &pb.ProfileChanged{User: convert.PublicUser(u)},
	}}})
	return convert.User(u), nil
}

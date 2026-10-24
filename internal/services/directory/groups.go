package directory

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/gocql/gocql"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/actors"
	"github.com/ssenerg/gochat/internal/authz"
	"github.com/ssenerg/gochat/internal/core/ids"
	"github.com/ssenerg/gochat/internal/core/models"
	ierrors "github.com/ssenerg/gochat/internal/errors"
	"github.com/ssenerg/gochat/internal/repos/scylla"
	"github.com/ssenerg/gochat/internal/services/channels"
	"github.com/ssenerg/gochat/internal/services/convert"
)

// maxGroup bounds the people of a group conversation, its starter included.
const maxGroup = 8

// openGroup opens the conversation of the caller and the people they chose:
// a direct one with one person, a group one with more. The same people
// always find the same conversation.
func (w *worker) openGroup(ctx context.Context, msg OpenGroup) (*pb.Channel, error) {
	if !w.Authz.Can(authz.ObjDirect, authz.ActCreate, string(msg.P.Role)) {
		return nil, ierrors.PermissionDenied()
	}
	members := []gocql.UUID{msg.P.UserID}
	for _, raw := range msg.UserIDs {
		id, err := parseID(raw, "user")
		if err != nil {
			return nil, err
		}
		if !slices.Contains(members, id) {
			members = append(members, id)
		}
	}
	switch {
	case len(members) == 1:
		return nil, ierrors.InvalidArgument("pick someone to talk to")
	case len(members) == 2:
		return w.openDirect(ctx, OpenDirect{P: msg.P, UserID: members[1].String()})
	case len(members) > maxGroup:
		return nil, ierrors.InvalidArgument("a group conversation holds at most 8 people")
	}
	mine, err := w.teamsOf(ctx, msg.P.UserID)
	if err != nil {
		return nil, err
	}
	for _, id := range members[1:] {
		if err := w.reachable(ctx, msg.P, mine, id); err != nil {
			return nil, err
		}
	}
	keys := make([]string, len(members))
	for i, id := range members {
		keys[i] = id.String()
	}
	slices.Sort(keys)
	key := strings.Join(keys, ":")
	now := time.Now().UTC()
	ch := &models.Channel{
		ID: ids.New(), TeamID: scylla.NoTeam, Name: key, DisplayName: "Group", Kind: models.ChannelGroup,
		CreatedBy: msg.P.UserID, CreatedAt: now,
	}
	cms := make([]*models.ChannelMember, len(members))
	for i, id := range members {
		cms[i] = &models.ChannelMember{ChannelID: ch.ID, UserID: id, TeamID: scylla.NoTeam, Role: models.ChannelRoleMember, JoinedAt: now}
	}
	chID, err := w.Repo.CreateDirectChannel(ctx, key, ch, cms)
	if err != nil {
		return nil, err
	}
	view, err := actors.Ask[*pb.Channel](ctx, w.Engine, w.Channels, channels.View{Target: channels.Target{P: msg.P, ChannelID: chID}})
	if err != nil {
		return nil, err
	}
	if chID == ch.ID {
		if people, err := w.Users.GetMany(ctx, members); err == nil {
			for _, id := range members {
				w.deliver(id, &pb.Event{Payload: &pb.Event_ChannelJoined{ChannelJoined: &pb.ChannelJoined{
					Channel: convert.Conversation(ch, models.ChannelRoleMember, gocql.UUID{}, id, people),
				}}})
			}
		}
	}
	return view, nil
}

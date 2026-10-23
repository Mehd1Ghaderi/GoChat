// Package convert renders stored entities as the API's protobuf messages.
package convert

import (
	"time"

	"github.com/gocql/gocql"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/core/ids"
	"github.com/ssenerg/gochat/internal/core/models"
)

func Time(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func ID(u gocql.UUID) string {
	if ids.IsZero(u) {
		return ""
	}
	return u.String()
}

func SystemRole(r models.SystemRole) pb.SystemRole {
	if r == models.SystemRoleAdmin {
		return pb.SystemRole_SYSTEM_ROLE_ADMIN
	}
	return pb.SystemRole_SYSTEM_ROLE_USER
}

func TeamRole(r models.TeamRole) pb.TeamRole {
	switch r {
	case models.TeamRoleAdmin:
		return pb.TeamRole_TEAM_ROLE_ADMIN
	case models.TeamRoleMember:
		return pb.TeamRole_TEAM_ROLE_MEMBER
	default:
		return pb.TeamRole_TEAM_ROLE_UNSPECIFIED
	}
}

func TeamRoleFrom(r pb.TeamRole) (models.TeamRole, bool) {
	switch r {
	case pb.TeamRole_TEAM_ROLE_ADMIN:
		return models.TeamRoleAdmin, true
	case pb.TeamRole_TEAM_ROLE_MEMBER:
		return models.TeamRoleMember, true
	default:
		return "", false
	}
}

func ChannelRole(r models.ChannelRole) pb.ChannelRole {
	switch r {
	case models.ChannelRoleAdmin:
		return pb.ChannelRole_CHANNEL_ROLE_ADMIN
	case models.ChannelRoleMember:
		return pb.ChannelRole_CHANNEL_ROLE_MEMBER
	default:
		return pb.ChannelRole_CHANNEL_ROLE_UNSPECIFIED
	}
}

func ChannelType(k models.ChannelKind) pb.ChannelType {
	switch k {
	case models.ChannelPublic:
		return pb.ChannelType_CHANNEL_TYPE_PUBLIC
	case models.ChannelPrivate:
		return pb.ChannelType_CHANNEL_TYPE_PRIVATE
	case models.ChannelDirect:
		return pb.ChannelType_CHANNEL_TYPE_DIRECT
	default:
		return pb.ChannelType_CHANNEL_TYPE_UNSPECIFIED
	}
}

func User(u *models.User) *pb.User {
	if u == nil {
		return nil
	}
	return &pb.User{
		Id:          u.ID.String(),
		Username:    u.Username,
		DisplayName: u.DisplayName,
		Role:        SystemRole(u.Role),
		TotpEnabled: u.TOTPEnabled,
		CreatedAt:   Time(u.CreatedAt),
		Deactivated: u.Deactivated,
		Photo:       u.Photo,
	}
}

// PublicUser is User without what only its owner should see.
func PublicUser(u *models.User) *pb.User {
	if u == nil {
		return nil
	}
	return &pb.User{
		Id:          u.ID.String(),
		Username:    u.Username,
		DisplayName: u.DisplayName,
		Role:        SystemRole(u.Role),
		Deactivated: u.Deactivated,
		Photo:       u.Photo,
	}
}

func Team(t *models.Team, role models.TeamRole) *pb.Team {
	return &pb.Team{
		Id:          t.ID.String(),
		Name:        t.Name,
		DisplayName: t.DisplayName,
		Description: t.Description,
		Role:        TeamRole(role),
		CreatedAt:   Time(t.CreatedAt),
		Photo:       t.Photo,
	}
}

func Channel(ch *models.Channel, role models.ChannelRole, lastRead gocql.UUID, peer *pb.User) *pb.Channel {
	return &pb.Channel{
		Id:            ch.ID.String(),
		TeamId:        ID(ch.TeamID),
		Name:          ch.Name,
		DisplayName:   ch.DisplayName,
		Purpose:       ch.Purpose,
		Type:          ChannelType(ch.Kind),
		Role:          ChannelRole(role),
		LastMessageId: ID(ch.LastMessageID),
		LastReadId:    ID(lastRead),
		Peer:          peer,
		CreatedAt:     Time(ch.CreatedAt),
	}
}

func Attachment(a *models.Attachment) *pb.Attachment {
	return &pb.Attachment{Id: a.ID.String(), Name: a.Name, ContentType: a.ContentType, Size: a.Size}
}

func Message(m *models.Message, author *pb.User, atts []*models.Attachment) *pb.Message {
	out := &pb.Message{
		Id:        m.ID.String(),
		ChannelId: m.ChannelID.String(),
		Author:    author,
		Content:   m.Content,
		CreatedAt: Time(m.ID.Time()),
		EditedAt:  Time(m.EditedAt),
	}
	for _, a := range atts {
		out.Attachments = append(out.Attachments, Attachment(a))
	}
	return out
}

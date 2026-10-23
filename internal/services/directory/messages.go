package directory

import (
	"time"

	"github.com/gocql/gocql"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/core/models"
)

type CreateTeam struct {
	P                              *models.Principal
	Name, DisplayName, Description string
}

type ListTeams struct{ P *models.Principal }

// GetTeam finds a team by ID or, when set, Name.
type GetTeam struct {
	P        *models.Principal
	ID, Name string
}

type ListTeamMembers struct {
	P      *models.Principal
	TeamID string
	Query  string
	Limit  int
}

type RemoveTeamMember struct {
	P              *models.Principal
	TeamID, UserID string
}

type UpdateTeamMemberRole struct {
	P              *models.Principal
	TeamID, UserID string
	Role           models.TeamRole
}

type CreateInvite struct {
	P      *models.Principal
	TeamID string
}

type Invite struct {
	Token     string
	ExpiresAt time.Time
}

type ListInvites struct {
	P      *models.Principal
	TeamID string
}

type RevokeInvite struct {
	P                *models.Principal
	TeamID, InviteID string
}

type GetInvite struct{ Token string }

type InviteInfo struct {
	Valid           bool
	TeamDisplayName string
}

type AcceptInvite struct {
	P     *models.Principal
	Token string
}

// JoinTeam admits a user whose invite was already consumed.
type JoinTeam struct {
	UserID, TeamID gocql.UUID
}

type ListChannels struct {
	P      *models.Principal
	TeamID string
}

type BrowseChannels struct {
	P      *models.Principal
	TeamID string
}

type CreateChannel struct {
	P                                  *models.Principal
	TeamID, Name, DisplayName, Purpose string
	Kind                               models.ChannelKind
}

type OpenDirect struct {
	P      *models.Principal
	UserID string
}

type GetUser struct {
	P      *models.Principal
	UserID string
}

type UpdateProfile struct {
	P           *models.Principal
	DisplayName string
}

type Me struct{ P *models.Principal }

type MeResult struct {
	User  *pb.User
	Email string
}

type Presence struct {
	P       *models.Principal
	UserIDs []string
}

type Done struct{}

package accounts

import (
	"github.com/gocql/gocql"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/core/models"
)

type SignupInfo struct{ InviteToken string }

type SignupInfoResult struct {
	Bootstrap          bool
	SetupTokenRequired bool
	// SetupClosed is set while no account exists and none can be created:
	// the server has no setup token.
	SetupClosed     bool
	Open            bool
	InviteValid     bool
	TeamDisplayName string
}

type SignUp struct {
	Username, Email, DisplayName, Password, InviteToken, SetupToken string
	IP, UserAgent                                                   string
}

type SignUpResult struct {
	Session *pb.Session
	User    *models.User
	// InviteTeamID is the team the consumed invite admits the user to, and
	// InviteHash that invite's.
	InviteTeamID gocql.UUID
	InviteHash   []byte
}

type Login struct {
	Login, Password, IP, UserAgent string
}

type LoginResult struct {
	Session  *pb.Session
	MFAToken string
}

type VerifyMFA struct {
	Token, Code, IP, UserAgent string
}

// Authenticate resolves a session token; IP, when known, is where the
// request came from.
type Authenticate struct{ Token, IP string }

type Logout struct{ P *models.Principal }

type ChangePassword struct {
	P                    *models.Principal
	Current, NewPassword string
}

type BeginTOTP struct {
	P        *models.Principal
	Password string
}

type TOTPSetup struct{ Secret, URL string }

type ConfirmTOTP struct {
	P    *models.Principal
	Code string
}

type RecoveryCodes []string

type DisableTOTP struct {
	P              *models.Principal
	Password, Code string
}

type Done struct{}

// ListSessions replies with the []*pb.SessionInfo of the caller, or of
// UserID for someone who may manage any.
type ListSessions struct {
	P      *models.Principal
	UserID string
}

// RevokeSession signs out one of the caller's sessions.
type RevokeSession struct {
	P         *models.Principal
	SessionID string
}

// RevokeSessions signs out the caller's other sessions, or every session of
// UserID, and replies with how many.
type RevokeSessions struct {
	P      *models.Principal
	UserID string
}

type Revoked int

// FindUser replies with the *models.User of UserID, or else of Login, a
// username or an email.
type FindUser struct {
	P             *models.Principal
	Login, UserID string
}

// SetDeactivated deactivates or reactivates a user and replies with the
// *models.User.
type SetDeactivated struct {
	P           *models.Principal
	UserID      string
	Deactivated bool
}

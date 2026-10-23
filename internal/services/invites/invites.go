// Package invites decides whether a one-time team invite can still admit
// someone.
package invites

import (
	"context"
	"time"

	"github.com/gocql/gocql"

	"github.com/ssenerg/gochat/internal/authz"
	"github.com/ssenerg/gochat/internal/core/ids"
	"github.com/ssenerg/gochat/internal/core/models"
	"github.com/ssenerg/gochat/internal/core/secure"
	ierrors "github.com/ssenerg/gochat/internal/errors"
	"github.com/ssenerg/gochat/internal/services/users"
)

type Repo interface {
	GetInvite(ctx context.Context, hash []byte) (*models.Invite, error)
	GetTeamMember(ctx context.Context, teamID, userID gocql.UUID) (*models.TeamMember, error)
}

var ErrInvalid = ierrors.NotFound("invite is invalid or expired")

// Valid returns the invite of token when it can admit caller: unexpired,
// unused or used by caller in a join that did not complete, and made by
// someone still allowed to invite to its team. caller is zero for anyone.
func Valid(ctx context.Context, repo Repo, az *authz.Authorizer, userCache *users.Cache, token string, caller gocql.UUID) (*models.Invite, error) {
	if !secure.ValidToken(token) {
		return nil, ErrInvalid
	}
	inv, err := repo.GetInvite(ctx, secure.Hash(token))
	if err != nil {
		return nil, notFound(err)
	}
	if !time.Now().Before(inv.ExpiresAt) {
		return nil, ErrInvalid
	}
	if !ids.IsZero(inv.UsedBy) && (ids.IsZero(caller) || inv.UsedBy != caller) {
		return nil, ErrInvalid
	}
	creator, err := userCache.Get(ctx, inv.CreatedBy)
	if err != nil {
		return nil, notFound(err)
	}
	var teamRole string
	m, err := repo.GetTeamMember(ctx, inv.TeamID, inv.CreatedBy)
	switch {
	case err == nil:
		teamRole = string(m.Role)
	case !ierrors.Is(err, ierrors.CodeNotFound):
		return nil, err
	}
	if creator.Deactivated || !az.Can(authz.ObjInvite, authz.ActCreate, teamRole, string(creator.Role)) {
		return nil, ErrInvalid
	}
	return inv, nil
}

func notFound(err error) error {
	if ierrors.Is(err, ierrors.CodeNotFound) {
		return ErrInvalid
	}
	return err
}

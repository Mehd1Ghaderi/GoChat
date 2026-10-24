package accounts

import (
	"bytes"
	"context"
	"slices"
	"time"

	"github.com/gocql/gocql"
	"go.uber.org/zap"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/authz"
	"github.com/ssenerg/gochat/internal/core/ids"
	"github.com/ssenerg/gochat/internal/core/models"
	"github.com/ssenerg/gochat/internal/core/secure"
	ierrors "github.com/ssenerg/gochat/internal/errors"
	"github.com/ssenerg/gochat/internal/services/convert"
)

// maxListedSessions bounds the sessions a listing shows.
const maxListedSessions = 100

var errNoSession = ierrors.NotFound("session not found")

// sessionOwner is whose sessions a call is about: the caller's, or for
// someone who may manage any, those of raw.
func (w *worker) sessionOwner(p *models.Principal, raw string) (gocql.UUID, error) {
	if raw == "" {
		return p.UserID, nil
	}
	id, ok := ids.Parse(raw)
	if !ok {
		return gocql.UUID{}, ierrors.InvalidArgument("invalid user id")
	}
	if id != p.UserID && !w.Authz.Can(authz.ObjSession, authz.ActManageAny, string(p.Role)) {
		return gocql.UUID{}, ierrors.PermissionDenied()
	}
	return id, nil
}

func lastActive(s *models.Session) time.Time {
	if s.LastSeen.After(s.CreatedAt) {
		return s.LastSeen
	}
	return s.CreatedAt
}

func (w *worker) listSessions(ctx context.Context, msg ListSessions) ([]*pb.SessionInfo, error) {
	userID, err := w.sessionOwner(msg.P, msg.UserID)
	if err != nil {
		return nil, err
	}
	list, err := w.Repo.ListUserSessions(ctx, userID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	// A row without an expiry is what a use racing a revocation left.
	list = slices.DeleteFunc(list, func(s *models.Session) bool { return !now.Before(s.ExpiresAt) })
	slices.SortFunc(list, func(a, b *models.Session) int { return lastActive(b).Compare(lastActive(a)) })
	list = list[:min(len(list), maxListedSessions)]
	out := make([]*pb.SessionInfo, len(list))
	for i, s := range list {
		out[i] = convert.Session(s, bytes.Equal(s.TokenHash, msg.P.SessionHash))
	}
	return out, nil
}

func (w *worker) revokeSession(ctx context.Context, msg RevokeSession) (Done, error) {
	if len(msg.SessionID) != 24 {
		return Done{}, errNoSession
	}
	list, err := w.Repo.ListUserSessions(ctx, msg.P.UserID)
	if err != nil {
		return Done{}, err
	}
	i := slices.IndexFunc(list, func(s *models.Session) bool { return secure.PublicID(s.TokenHash) == msg.SessionID })
	if i < 0 {
		return Done{}, errNoSession
	}
	hash := list[i].TokenHash
	if err := w.Repo.DeleteSession(ctx, hash, msg.P.UserID); err != nil {
		return Done{}, err
	}
	w.revoked(msg.P.UserID, [][]byte{hash}, false)
	return Done{}, nil
}

// revokeSessions keeps the caller's own session; another user loses all of
// theirs and every connection.
func (w *worker) revokeSessions(ctx context.Context, msg RevokeSessions) (Revoked, error) {
	userID, err := w.sessionOwner(msg.P, msg.UserID)
	if err != nil {
		return 0, err
	}
	own := userID == msg.P.UserID
	var keep []byte
	if own {
		keep = msg.P.SessionHash
	}
	hashes, err := w.Repo.DeleteUserSessions(ctx, userID, keep)
	w.revoked(userID, hashes, !own)
	if err != nil {
		return 0, err
	}
	if !own {
		w.Logger.Info("sessions revoked", zap.String("user", userID.String()), zap.String("by", msg.P.UserID.String()), zap.Int("count", len(hashes)))
	}
	return Revoked(len(hashes)), nil
}

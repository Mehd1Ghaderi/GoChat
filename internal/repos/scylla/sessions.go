package scylla

import (
	"context"
	"time"

	"github.com/gocql/gocql"

	"github.com/ssenerg/gochat/internal/core/models"
)

func ttlSeconds(until, now time.Time) int {
	s := int(until.Sub(now) / time.Second)
	if s < 1 {
		return 1
	}
	return s
}

func (r *Repository) CreateSession(ctx context.Context, s *models.Session) error {
	ttl := ttlSeconds(s.ExpiresAt, s.CreatedAt)
	b := r.batch(ctx, true)
	b.Query("INSERT INTO sessions (token_hash, user_id, created_at, expires_at, ip, user_agent) VALUES (?, ?, ?, ?, ?, ?) USING TTL ?",
		s.TokenHash, s.UserID, s.CreatedAt, s.ExpiresAt, s.IP, s.UserAgent, ttl)
	b.Query("INSERT INTO user_sessions (user_id, token_hash, created_at, expires_at, ip, user_agent) VALUES (?, ?, ?, ?, ?, ?) USING TTL ?",
		s.UserID, s.TokenHash, s.CreatedAt, s.ExpiresAt, s.IP, s.UserAgent, ttl)
	return r.execBatch(b)
}

const sessionColumns = "token_hash, user_id, created_at, expires_at, ip, user_agent, last_seen, last_ip"

func (r *Repository) GetSession(ctx context.Context, hash []byte) (*models.Session, error) {
	var s models.Session
	err := r.query(ctx, "SELECT "+sessionColumns+" FROM sessions WHERE token_hash = ?", hash).GetRelease(&s)
	if err != nil {
		return nil, wrap(err)
	}
	return &s, nil
}

// TouchSession records when s was last used, and from where. A session revoked
// meanwhile leaves a row with no user and no expiry, which reads as gone and
// lives no longer than the session would have.
func (r *Repository) TouchSession(ctx context.Context, s *models.Session, at time.Time, ip string) error {
	ttl := ttlSeconds(s.ExpiresAt, at)
	b := r.batch(ctx, false)
	b.Query("UPDATE sessions USING TTL ? SET last_seen = ?, last_ip = ? WHERE token_hash = ?", ttl, at, ip, s.TokenHash)
	b.Query("UPDATE user_sessions USING TTL ? SET last_seen = ?, last_ip = ? WHERE user_id = ? AND token_hash = ?", ttl, at, ip, s.UserID, s.TokenHash)
	return r.execBatch(b)
}

func (r *Repository) DeleteSession(ctx context.Context, hash []byte, userID gocql.UUID) error {
	b := r.batch(ctx, true)
	b.Query("DELETE FROM sessions WHERE token_hash = ?", hash)
	b.Query("DELETE FROM user_sessions WHERE user_id = ? AND token_hash = ?", userID, hash)
	return r.execBatch(b)
}

func (r *Repository) ListUserSessions(ctx context.Context, userID gocql.UUID) ([]*models.Session, error) {
	var out []*models.Session
	err := r.query(ctx, "SELECT "+sessionColumns+" FROM user_sessions WHERE user_id = ?", userID).SelectRelease(&out)
	return out, wrap(err)
}

// DeleteUserSessions revokes every session of userID but keep, returning
// the revoked hashes.
func (r *Repository) DeleteUserSessions(ctx context.Context, userID gocql.UUID, keep []byte) ([][]byte, error) {
	sessions, err := r.ListUserSessions(ctx, userID)
	if err != nil {
		return nil, err
	}
	var revoked [][]byte
	for _, s := range sessions {
		if keep != nil && string(s.TokenHash) == string(keep) {
			continue
		}
		if err := r.DeleteSession(ctx, s.TokenHash, userID); err != nil {
			return revoked, err
		}
		revoked = append(revoked, s.TokenHash)
	}
	return revoked, nil
}

func (r *Repository) CreateMFAChallenge(ctx context.Context, c *models.MFAChallenge, now time.Time) error {
	// Conditional like the attempt counter, so both take cluster timestamps.
	_, err := r.query(ctx, "INSERT INTO mfa_challenges (token_hash, user_id, attempts, expires_at, ip, user_agent) VALUES (?, ?, ?, ?, ?, ?) IF NOT EXISTS USING TTL ?",
		c.TokenHash, c.UserID, c.Attempts, c.ExpiresAt, c.IP, c.UserAgent, ttlSeconds(c.ExpiresAt, now)).ExecCASRelease()
	return wrap(err)
}

func (r *Repository) GetMFAChallenge(ctx context.Context, hash []byte) (*models.MFAChallenge, error) {
	var c models.MFAChallenge
	err := r.query(ctx, "SELECT token_hash, user_id, attempts, expires_at, ip, user_agent FROM mfa_challenges WHERE token_hash = ?", hash).GetRelease(&c)
	if err != nil {
		return nil, wrap(err)
	}
	return &c, nil
}

// CountMFAAttempt records one more attempt, refusing when another attempt
// raced this one.
func (r *Repository) CountMFAAttempt(ctx context.Context, c *models.MFAChallenge, now time.Time) (bool, error) {
	applied, err := r.query(ctx, "UPDATE mfa_challenges USING TTL ? SET attempts = ? WHERE token_hash = ? IF attempts = ?",
		ttlSeconds(c.ExpiresAt, now), c.Attempts+1, c.TokenHash, c.Attempts).ExecCASRelease()
	return applied, wrap(err)
}

// ConsumeMFAChallenge deletes the challenge, reporting whether this call is
// the one that did.
func (r *Repository) ConsumeMFAChallenge(ctx context.Context, hash []byte) (bool, error) {
	applied, err := r.query(ctx, "DELETE FROM mfa_challenges WHERE token_hash = ? IF EXISTS", hash).ExecCASRelease()
	return applied, wrap(err)
}

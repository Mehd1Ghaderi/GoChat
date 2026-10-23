package scylla

import (
	"context"
	"errors"
	"time"

	"github.com/gocql/gocql"

	"github.com/ssenerg/gochat/internal/core/models"
	ierrors "github.com/ssenerg/gochat/internal/errors"
)

const userColumns = "id, username, email, display_name, password_hash, system_role, totp_enabled, totp_secret, totp_pending, totp_last_step, deactivated, created_at, updated_at"

// CreateUser claims the username and the email, then writes the user. A claim
// that fails releases the ones taken before it.
func (r *Repository) CreateUser(ctx context.Context, u *models.User) error {
	applied, err := r.query(ctx, "INSERT INTO usernames (username, user_id) VALUES (?, ?) IF NOT EXISTS", u.Username, u.ID).ExecCASRelease()
	if err != nil {
		return wrap(err)
	}
	if !applied {
		return ierrors.AlreadyExists("username is taken")
	}
	applied, err = r.query(ctx, "INSERT INTO emails (email, user_id) VALUES (?, ?) IF NOT EXISTS", u.Email, u.ID).ExecCASRelease()
	if err != nil || !applied {
		r.releaseUsername(u.Username, u.ID)
		if err != nil {
			return wrap(err)
		}
		return ierrors.AlreadyExists("email is already registered")
	}
	err = r.query(ctx, "INSERT INTO users ("+userColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		u.ID, u.Username, u.Email, u.DisplayName, u.PasswordHash, string(u.Role), u.TOTPEnabled, u.TOTPSecret,
		u.TOTPPending, u.TOTPLastStep, u.Deactivated, u.CreatedAt, u.UpdatedAt).ExecRelease()
	if err != nil {
		r.releaseUsername(u.Username, u.ID)
		r.releaseEmail(u.Email, u.ID)
		return wrap(err)
	}
	return nil
}

func (r *Repository) releaseUsername(username string, id gocql.UUID) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = r.query(ctx, "DELETE FROM usernames WHERE username = ? IF user_id = ?", username, id).ExecCASRelease()
}

func (r *Repository) releaseEmail(email string, id gocql.UUID) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = r.query(ctx, "DELETE FROM emails WHERE email = ? IF user_id = ?", email, id).ExecCASRelease()
}

func (r *Repository) GetUser(ctx context.Context, id gocql.UUID) (*models.User, error) {
	var u models.User
	if err := r.query(ctx, "SELECT "+userColumns+" FROM users WHERE id = ?", id).GetRelease(&u); err != nil {
		return nil, wrap(err)
	}
	return &u, nil
}

// GetUsers returns the users found among ids, at most 100 per call.
func (r *Repository) GetUsers(ctx context.Context, ids []gocql.UUID) ([]*models.User, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > 100 {
		return nil, ierrors.InvalidArgument("too many users")
	}
	var users []*models.User
	if err := r.query(ctx, "SELECT "+userColumns+" FROM users WHERE id IN ?", ids).SelectRelease(&users); err != nil {
		return nil, wrap(err)
	}
	return users, nil
}

func (r *Repository) GetUserIDByUsername(ctx context.Context, username string) (gocql.UUID, error) {
	var id gocql.UUID
	if err := r.query(ctx, "SELECT user_id FROM usernames WHERE username = ?", username).Scan(&id); err != nil {
		return gocql.UUID{}, wrap(err)
	}
	return id, nil
}

func (r *Repository) GetUserIDByEmail(ctx context.Context, email string) (gocql.UUID, error) {
	var id gocql.UUID
	if err := r.query(ctx, "SELECT user_id FROM emails WHERE email = ?", email).Scan(&id); err != nil {
		return gocql.UUID{}, wrap(err)
	}
	return id, nil
}

func (r *Repository) UpdateDisplayName(ctx context.Context, id gocql.UUID, name string, now time.Time) error {
	return wrap(r.query(ctx, "UPDATE users SET display_name = ?, updated_at = ? WHERE id = ?", name, now, id).ExecRelease())
}

func (r *Repository) UpdatePassword(ctx context.Context, id gocql.UUID, hash string, now time.Time) error {
	return wrap(r.query(ctx, "UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?", hash, now, id).ExecRelease())
}

func (r *Repository) SetTOTPPending(ctx context.Context, id gocql.UUID, sealed []byte, now time.Time) error {
	return wrap(r.query(ctx, "UPDATE users SET totp_pending = ?, updated_at = ? WHERE id = ?", sealed, now, id).ExecRelease())
}

// EnableTOTP promotes the pending secret, guarded by it being the one the
// caller confirmed.
func (r *Repository) EnableTOTP(ctx context.Context, id gocql.UUID, sealed []byte, step int64, now time.Time) (bool, error) {
	applied, err := r.query(ctx,
		"UPDATE users SET totp_enabled = true, totp_secret = ?, totp_pending = null, totp_last_step = ?, updated_at = ? WHERE id = ? IF totp_pending = ?",
		sealed, step, now, id, sealed).ExecCASRelease()
	return applied, wrap(err)
}

func (r *Repository) DisableTOTP(ctx context.Context, id gocql.UUID, now time.Time) error {
	b := r.batch(ctx, true)
	b.Query("UPDATE users SET totp_enabled = false, totp_secret = null, totp_pending = null, totp_last_step = 0, updated_at = ? WHERE id = ?", now, id)
	b.Query("DELETE FROM recovery_codes WHERE user_id = ?", id)
	return r.execBatch(b)
}

// AdvanceTOTPStep records step as used, refusing a step not after the last.
func (r *Repository) AdvanceTOTPStep(ctx context.Context, id gocql.UUID, step int64) (bool, error) {
	applied, err := r.query(ctx, "UPDATE users SET totp_last_step = ? WHERE id = ? IF totp_last_step < ?", step, id, step).ExecCASRelease()
	return applied, wrap(err)
}

// ReplaceRecoveryCodes swaps the user's codes. The delete is written just
// before the inserts: in one batch they would share a timestamp, and the
// tombstone would win.
func (r *Repository) ReplaceRecoveryCodes(ctx context.Context, id gocql.UUID, hashes [][]byte) error {
	ts := time.Now().UnixMicro()
	b := r.batch(ctx, true)
	b.Query("DELETE FROM recovery_codes USING TIMESTAMP ? WHERE user_id = ?", ts-1, id)
	for _, h := range hashes {
		b.Query("INSERT INTO recovery_codes (user_id, code_hash) VALUES (?, ?) USING TIMESTAMP ?", id, h, ts)
	}
	return r.execBatch(b)
}

func (r *Repository) ConsumeRecoveryCode(ctx context.Context, id gocql.UUID, hash []byte) (bool, error) {
	applied, err := r.query(ctx, "DELETE FROM recovery_codes WHERE user_id = ? AND code_hash = ? IF EXISTS", id, hash).ExecCASRelease()
	return applied, wrap(err)
}

const bootstrapKey = "bootstrap_admin"

// ClaimBootstrap lets exactly one account become the first system admin.
func (r *Repository) ClaimBootstrap(ctx context.Context, id gocql.UUID) (bool, error) {
	applied, err := r.query(ctx, "INSERT INTO system_state (key, value) VALUES (?, ?) IF NOT EXISTS", bootstrapKey, id.String()).ExecCASRelease()
	return applied, wrap(err)
}

func (r *Repository) ReleaseBootstrap(ctx context.Context, id gocql.UUID) error {
	_, err := r.query(ctx, "DELETE FROM system_state WHERE key = ? IF value = ?", bootstrapKey, id.String()).ExecCASRelease()
	return wrap(err)
}

func (r *Repository) Bootstrapped(ctx context.Context) (bool, error) {
	var value string
	err := r.query(ctx, "SELECT value FROM system_state WHERE key = ?", bootstrapKey).Scan(&value)
	if errors.Is(err, gocql.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, wrap(err)
	}
	return true, nil
}

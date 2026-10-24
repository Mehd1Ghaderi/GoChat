package scylla

import (
	"context"
	"errors"

	"github.com/gocql/gocql"

	ierrors "github.com/ssenerg/gochat/internal/errors"
)

// casAttempts bounds the retries of a compare-and-set others keep winning.
const casAttempts = 16

// StorageUsage returns what userID's attachments take and the quota set for
// them, zero when they have none of their own.
func (r *Repository) StorageUsage(ctx context.Context, userID gocql.UUID) (used, quota int64, err error) {
	used, _, err = r.storageUsed(ctx, userID)
	if err != nil {
		return 0, 0, err
	}
	quota, err = r.storageQuota(ctx, userID)
	return used, quota, err
}

func (r *Repository) storageUsed(ctx context.Context, userID gocql.UUID) (int64, bool, error) {
	var used int64
	err := r.query(ctx, "SELECT used FROM storage_usage WHERE user_id = ?", userID).Scan(&used)
	if errors.Is(err, gocql.ErrNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, wrap(err)
	}
	return used, true, nil
}

func (r *Repository) storageQuota(ctx context.Context, userID gocql.UUID) (int64, error) {
	var quota int64
	err := r.query(ctx, "SELECT quota FROM storage_quotas WHERE user_id = ?", userID).Scan(&quota)
	if errors.Is(err, gocql.ErrNotFound) {
		return 0, nil
	}
	return quota, wrap(err)
}

// SetStorageQuota gives userID a quota of their own, or with zero puts them
// back on the default.
func (r *Repository) SetStorageQuota(ctx context.Context, userID gocql.UUID, quota int64) error {
	if quota == 0 {
		return wrap(r.query(ctx, "DELETE FROM storage_quotas WHERE user_id = ?", userID).ExecRelease())
	}
	return wrap(r.query(ctx, "INSERT INTO storage_quotas (user_id, quota) VALUES (?, ?)", userID, quota).ExecRelease())
}

// ChargeStorage adds size to userID's usage unless that passes limit, their
// quota or the default when they have none; zero is no limit.
func (r *Repository) ChargeStorage(ctx context.Context, userID gocql.UUID, size, limit int64) (bool, error) {
	quota, err := r.storageQuota(ctx, userID)
	if err != nil {
		return false, err
	}
	if quota > 0 {
		limit = quota
	}
	used, exists, err := r.storageUsed(ctx, userID)
	if err != nil {
		return false, err
	}
	return r.adjustStorage(ctx, userID, used, exists, func(used int64) (int64, bool) {
		return used + size, limit <= 0 || used+size <= limit
	})
}

// ReleaseStorage takes size off userID's usage.
func (r *Repository) ReleaseStorage(ctx context.Context, userID gocql.UUID, size int64) error {
	used, exists, err := r.storageUsed(ctx, userID)
	if err != nil || !exists {
		return err
	}
	_, err = r.adjustStorage(ctx, userID, used, true, func(used int64) (int64, bool) {
		return max(0, used-size), true
	})
	return err
}

// adjustStorage sets userID's usage to what next makes of it, as a
// compare-and-set retried with the value that won, until next refuses.
func (r *Repository) adjustStorage(ctx context.Context, userID gocql.UUID, used int64, exists bool, next func(int64) (int64, bool)) (bool, error) {
	for range casAttempts {
		n, ok := next(used)
		if !ok {
			return false, nil
		}
		q := r.query(ctx, "INSERT INTO storage_usage (user_id, used) VALUES (?, ?) IF NOT EXISTS", userID, n)
		if exists {
			q = r.query(ctx, "UPDATE storage_usage SET used = ? WHERE user_id = ? IF used = ?", n, userID, used)
		}
		row := make(map[string]any)
		applied, err := q.MapScanCAS(row)
		q.Release()
		if err != nil {
			return false, wrap(err)
		}
		if applied {
			return true, nil
		}
		used, _ = row["used"].(int64)
		exists = true
	}
	return false, ierrors.Unavailable("storage is busy, try again")
}

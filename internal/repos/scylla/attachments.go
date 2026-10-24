package scylla

import (
	"context"
	"time"

	"github.com/gocql/gocql"
	"go.uber.org/zap"

	"github.com/ssenerg/gochat/internal/core/ids"
	"github.com/ssenerg/gochat/internal/core/models"
	ierrors "github.com/ssenerg/gochat/internal/errors"
)

const attachmentColumns = "id, channel_id, uploader_id, name, content_type, size, object_key, message_id, created_at, voice, duration_ms"

// PendingShards spreads a day's unposted uploads over several partitions.
const PendingShards = 16

// PendingUpload is an upload not posted yet, kept until it is or the janitor
// deletes it.
type PendingUpload struct {
	ID        gocql.UUID `db:"id"`
	ObjectKey string     `db:"object_key"`
	CreatedAt time.Time  `db:"created_at"`
}

// PendingDay is the day partition of uploads made at t.
func PendingDay(t time.Time) int32 {
	return int32(t.Unix() / 86400) //nolint:gosec // fits int32 for millions of years
}

func pendingShard(id gocql.UUID) int {
	return int(id[0]) % PendingShards
}

func (r *Repository) InsertAttachment(ctx context.Context, a *models.Attachment) error {
	b := r.batch(ctx, true)
	b.Query("INSERT INTO attachments (id, channel_id, uploader_id, name, content_type, size, object_key, created_at, voice, duration_ms) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		a.ID, a.ChannelID, a.UploaderID, a.Name, a.ContentType, a.Size, a.ObjectKey, a.CreatedAt, a.Voice, a.DurationMS)
	b.Query("INSERT INTO pending_uploads (day, shard, id, object_key, created_at) VALUES (?, ?, ?, ?, ?)",
		PendingDay(a.CreatedAt), pendingShard(a.ID), a.ID, a.ObjectKey, a.CreatedAt)
	return r.execBatch(b)
}

func (r *Repository) GetAttachment(ctx context.Context, id gocql.UUID) (*models.Attachment, error) {
	var a models.Attachment
	if err := r.query(ctx, "SELECT "+attachmentColumns+" FROM attachments WHERE id = ?", id).GetRelease(&a); err != nil {
		return nil, wrap(err)
	}
	return &a, nil
}

func (r *Repository) GetAttachments(ctx context.Context, idList []gocql.UUID) ([]*models.Attachment, error) {
	if len(idList) == 0 {
		return nil, nil
	}
	if len(idList) > 100 {
		return nil, ierrors.InvalidArgument("too many attachments")
	}
	var out []*models.Attachment
	err := r.query(ctx, "SELECT "+attachmentColumns+" FROM attachments WHERE id IN ?", idList).SelectRelease(&out)
	return out, wrap(err)
}

// ClaimAttachment binds an unposted attachment of its channel to messageID,
// reporting false when it is posted or deleted already.
func (r *Repository) ClaimAttachment(ctx context.Context, a *models.Attachment, messageID gocql.UUID) (bool, error) {
	applied, err := r.query(ctx, "UPDATE attachments SET message_id = ? WHERE id = ? IF message_id = null AND channel_id = ?",
		messageID, a.ID, a.ChannelID).ExecCASRelease()
	if err != nil || !applied {
		return false, wrap(err)
	}
	r.forgetPending(ctx, a.ID, a.CreatedAt)
	return true, nil
}

// UnclaimAttachment undoes a claim whose message was not written.
func (r *Repository) UnclaimAttachment(ctx context.Context, a *models.Attachment, messageID gocql.UUID) error {
	applied, err := r.query(ctx, "UPDATE attachments SET message_id = null WHERE id = ? IF message_id = ?", a.ID, messageID).ExecCASRelease()
	if err != nil || !applied {
		return wrap(err)
	}
	return wrap(r.query(ctx, "INSERT INTO pending_uploads (day, shard, id, object_key, created_at) VALUES (?, ?, ?, ?, ?)",
		PendingDay(a.CreatedAt), pendingShard(a.ID), a.ID, a.ObjectKey, a.CreatedAt).ExecRelease())
}

// DeleteAttachment deletes the attachment of a deleted message.
func (r *Repository) DeleteAttachment(ctx context.Context, a *models.Attachment) error {
	return wrap(r.query(ctx, "DELETE FROM attachments WHERE id = ?", a.ID).ExecRelease())
}

// DeleteOrphan deletes an upload's row unless it was posted meanwhile,
// reporting whether its object is no longer referenced.
func (r *Repository) DeleteOrphan(ctx context.Context, id gocql.UUID) (bool, error) {
	row := make(map[string]any)
	q := r.query(ctx, "DELETE FROM attachments WHERE id = ? IF message_id = null", id)
	applied, err := q.MapScanCAS(row)
	q.Release()
	if err != nil {
		return false, wrap(err)
	}
	if applied {
		return true, nil
	}
	posted, _ := row["message_id"].(gocql.UUID)
	return ids.IsZero(posted), nil
}

// ForgetPending drops an upload from the pending list.
func (r *Repository) ForgetPending(ctx context.Context, p *PendingUpload) error {
	return wrap(r.query(ctx, "DELETE FROM pending_uploads WHERE day = ? AND shard = ? AND id = ?",
		PendingDay(p.CreatedAt), pendingShard(p.ID), p.ID).ExecRelease())
}

func (r *Repository) forgetPending(ctx context.Context, id gocql.UUID, createdAt time.Time) {
	if err := r.ForgetPending(ctx, &PendingUpload{ID: id, CreatedAt: createdAt}); err != nil {
		r.logger.Warn("failed to drop a posted upload from the pending list", zap.Error(err))
	}
}

// ListPendingUploads returns the uploads of day not posted yet.
func (r *Repository) ListPendingUploads(ctx context.Context, day int32) ([]*PendingUpload, error) {
	var out []*PendingUpload
	for shard := range PendingShards {
		var part []*PendingUpload
		err := r.query(ctx, "SELECT id, object_key, created_at FROM pending_uploads WHERE day = ? AND shard = ?", day, shard).SelectRelease(&part)
		if err != nil {
			return nil, wrap(err)
		}
		out = append(out, part...)
	}
	return out, nil
}

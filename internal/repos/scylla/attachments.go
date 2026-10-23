package scylla

import (
	"context"
	"time"

	"github.com/gocql/gocql"

	"github.com/ssenerg/gochat/internal/core/models"
	ierrors "github.com/ssenerg/gochat/internal/errors"
)

const attachmentColumns = "id, channel_id, uploader_id, name, content_type, size, object_key, message_id, created_at"

// PendingDay is the partition of attachments uploaded but not yet posted.
func PendingDay(t time.Time) int32 {
	return int32(t.Unix() / 86400) //nolint:gosec // fits int32 for millions of years
}

func (r *Repository) InsertAttachment(ctx context.Context, a *models.Attachment) error {
	b := r.batch(ctx, true)
	b.Query("INSERT INTO attachments ("+attachmentColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, null, ?)",
		a.ID, a.ChannelID, a.UploaderID, a.Name, a.ContentType, a.Size, a.ObjectKey, a.CreatedAt)
	b.Query("INSERT INTO pending_attachments (day, id, created_at) VALUES (?, ?, ?)", PendingDay(a.CreatedAt), a.ID, a.CreatedAt)
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

// ClaimAttachment binds an uploaded attachment to its message. Only the
// channel's actor calls it, one message at a time, so the check before it
// cannot race.
func (r *Repository) ClaimAttachment(ctx context.Context, a *models.Attachment, messageID gocql.UUID) error {
	b := r.batch(ctx, true)
	b.Query("UPDATE attachments SET message_id = ? WHERE id = ?", messageID, a.ID)
	b.Query("DELETE FROM pending_attachments WHERE day = ? AND id = ?", PendingDay(a.CreatedAt), a.ID)
	return r.execBatch(b)
}

func (r *Repository) DeleteAttachment(ctx context.Context, a *models.Attachment) error {
	b := r.batch(ctx, true)
	b.Query("DELETE FROM attachments WHERE id = ?", a.ID)
	b.Query("DELETE FROM pending_attachments WHERE day = ? AND id = ?", PendingDay(a.CreatedAt), a.ID)
	return r.execBatch(b)
}

// ListPendingAttachments returns the attachments of day never posted.
func (r *Repository) ListPendingAttachments(ctx context.Context, day int32) ([]gocql.UUID, error) {
	var out []gocql.UUID
	iter := r.query(ctx, "SELECT id FROM pending_attachments WHERE day = ?", day).Iter()
	var id gocql.UUID
	for iter.Scan(&id) {
		out = append(out, id)
	}
	return out, wrap(iter.Close())
}

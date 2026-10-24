package scylla

import (
	"context"
	"errors"
	"time"

	"github.com/gocql/gocql"

	"github.com/ssenerg/gochat/internal/core/ids"
	"github.com/ssenerg/gochat/internal/core/models"
)

const (
	messageColumns = "channel_id, bucket, id, user_id, content, attachments, edited_at, reply_count, last_reply_at, participants"
	replyColumns   = "channel_id, root_id, id, user_id, content, attachments, edited_at"
)

// InsertMessage writes m, and the bucket it opens when newBucket is set.
func (r *Repository) InsertMessage(ctx context.Context, m *models.Message, newBucket bool) error {
	if newBucket {
		if err := r.query(ctx, "INSERT INTO message_buckets (channel_id, bucket) VALUES (?, ?)", m.ChannelID, m.Bucket).ExecRelease(); err != nil {
			return wrap(err)
		}
	}
	// Absent columns are left out: a null or a whole collection would each
	// write a tombstone.
	if len(m.Attachments) == 0 {
		return wrap(r.query(ctx, "INSERT INTO messages (channel_id, bucket, id, user_id, content) VALUES (?, ?, ?, ?, ?)",
			m.ChannelID, m.Bucket, m.ID, m.UserID, m.Content).ExecRelease())
	}
	return wrap(r.query(ctx, "INSERT INTO messages (channel_id, bucket, id, user_id, content, attachments) VALUES (?, ?, ?, ?, ?, ?)",
		m.ChannelID, m.Bucket, m.ID, m.UserID, m.Content, m.Attachments).ExecRelease())
}

func (r *Repository) GetMessage(ctx context.Context, channelID, id gocql.UUID) (*models.Message, error) {
	var m models.Message
	err := r.query(ctx, "SELECT "+messageColumns+" FROM messages WHERE channel_id = ? AND bucket = ? AND id = ?",
		channelID, ids.BucketOf(id), id).GetRelease(&m)
	if err != nil {
		return nil, wrap(err)
	}
	return &m, nil
}

func (r *Repository) UpdateMessageContent(ctx context.Context, channelID, id gocql.UUID, content string, at time.Time) error {
	return wrap(r.query(ctx, "UPDATE messages SET content = ?, edited_at = ? WHERE channel_id = ? AND bucket = ? AND id = ?",
		content, at, channelID, ids.BucketOf(id), id).ExecRelease())
}

func (r *Repository) DeleteMessage(ctx context.Context, channelID, id gocql.UUID) error {
	return wrap(r.query(ctx, "DELETE FROM messages WHERE channel_id = ? AND bucket = ? AND id = ?",
		channelID, ids.BucketOf(id), id).ExecRelease())
}

// LastMessageBucket returns the newest bucket of the channel, false when it
// has none.
func (r *Repository) LastMessageBucket(ctx context.Context, channelID gocql.UUID) (int32, bool, error) {
	var bucket int32
	err := r.query(ctx, "SELECT bucket FROM message_buckets WHERE channel_id = ? LIMIT 1", channelID).Scan(&bucket)
	if errors.Is(err, gocql.ErrNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, wrap(err)
	}
	return bucket, true, nil
}

// ListMessages returns up to limit messages older than before (newest first),
// walking only the buckets that hold messages, and whether more exist.
func (r *Repository) ListMessages(ctx context.Context, channelID, before gocql.UUID, limit int) ([]*models.Message, bool, error) {
	upper := ids.Bucket(time.Now()) + 1
	if !ids.IsZero(before) {
		upper = ids.BucketOf(before)
	}
	var buckets []int32
	iter := r.query(ctx, "SELECT bucket FROM message_buckets WHERE channel_id = ? AND bucket <= ?", channelID, upper).Iter()
	var b int32
	for iter.Scan(&b) {
		buckets = append(buckets, b)
	}
	if err := iter.Close(); err != nil {
		return nil, false, wrap(err)
	}

	out := make([]*models.Message, 0, limit+1)
	for _, bucket := range buckets {
		want := limit + 1 - len(out)
		var page []*models.Message
		var err error
		if !ids.IsZero(before) && bucket == upper {
			err = r.query(ctx, "SELECT "+messageColumns+" FROM messages WHERE channel_id = ? AND bucket = ? AND id < ? LIMIT ?",
				channelID, bucket, before, want).SelectRelease(&page)
		} else {
			err = r.query(ctx, "SELECT "+messageColumns+" FROM messages WHERE channel_id = ? AND bucket = ? LIMIT ?",
				channelID, bucket, want).SelectRelease(&page)
		}
		if err != nil {
			return nil, false, wrap(err)
		}
		out = append(out, page...)
		if len(out) > limit {
			return out[:limit], true, nil
		}
	}
	return out, false, nil
}

// InsertReply writes a reply to its thread.
func (r *Repository) InsertReply(ctx context.Context, m *models.Message) error {
	if len(m.Attachments) == 0 {
		return wrap(r.query(ctx, "INSERT INTO replies (channel_id, root_id, id, user_id, content) VALUES (?, ?, ?, ?, ?)",
			m.ChannelID, m.RootID, m.ID, m.UserID, m.Content).ExecRelease())
	}
	return wrap(r.query(ctx, "INSERT INTO replies (channel_id, root_id, id, user_id, content, attachments) VALUES (?, ?, ?, ?, ?, ?)",
		m.ChannelID, m.RootID, m.ID, m.UserID, m.Content, m.Attachments).ExecRelease())
}

func (r *Repository) GetReply(ctx context.Context, channelID, rootID, id gocql.UUID) (*models.Message, error) {
	var m models.Message
	err := r.query(ctx, "SELECT "+replyColumns+" FROM replies WHERE channel_id = ? AND root_id = ? AND id = ?",
		channelID, rootID, id).GetRelease(&m)
	if err != nil {
		return nil, wrap(err)
	}
	return &m, nil
}

func (r *Repository) UpdateReplyContent(ctx context.Context, channelID, rootID, id gocql.UUID, content string, at time.Time) error {
	return wrap(r.query(ctx, "UPDATE replies SET content = ?, edited_at = ? WHERE channel_id = ? AND root_id = ? AND id = ?",
		content, at, channelID, rootID, id).ExecRelease())
}

func (r *Repository) DeleteReply(ctx context.Context, channelID, rootID, id gocql.UUID) error {
	return wrap(r.query(ctx, "DELETE FROM replies WHERE channel_id = ? AND root_id = ? AND id = ?",
		channelID, rootID, id).ExecRelease())
}

// ListReplies returns up to limit replies older than before, newest first,
// and whether more exist.
func (r *Repository) ListReplies(ctx context.Context, channelID, rootID, before gocql.UUID, limit int) ([]*models.Message, bool, error) {
	var out []*models.Message
	var err error
	if ids.IsZero(before) {
		err = r.query(ctx, "SELECT "+replyColumns+" FROM replies WHERE channel_id = ? AND root_id = ? ORDER BY id DESC LIMIT ?",
			channelID, rootID, limit+1).SelectRelease(&out)
	} else {
		err = r.query(ctx, "SELECT "+replyColumns+" FROM replies WHERE channel_id = ? AND root_id = ? AND id < ? ORDER BY id DESC LIMIT ?",
			channelID, rootID, before, limit+1).SelectRelease(&out)
	}
	if err != nil {
		return nil, false, wrap(err)
	}
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

// ThreadAttachments lists the files of a thread's replies.
func (r *Repository) ThreadAttachments(ctx context.Context, channelID, rootID gocql.UUID) ([]gocql.UUID, error) {
	iter := r.query(ctx, "SELECT attachments FROM replies WHERE channel_id = ? AND root_id = ?", channelID, rootID).Iter()
	var out, atts []gocql.UUID
	for iter.Scan(&atts) {
		out = append(out, atts...)
	}
	return out, wrap(iter.Close())
}

func (r *Repository) DeleteThread(ctx context.Context, channelID, rootID gocql.UUID) error {
	return wrap(r.query(ctx, "DELETE FROM replies WHERE channel_id = ? AND root_id = ?", channelID, rootID).ExecRelease())
}

// SetThreadSummary records a root's reply count and last reply, adding
// participant when it is set.
func (r *Repository) SetThreadSummary(ctx context.Context, root *models.Message, count int, last time.Time, participant gocql.UUID) error {
	var lastAt *time.Time
	if !last.IsZero() {
		lastAt = &last
	}
	if ids.IsZero(participant) {
		return wrap(r.query(ctx, "UPDATE messages SET reply_count = ?, last_reply_at = ? WHERE channel_id = ? AND bucket = ? AND id = ?",
			count, lastAt, root.ChannelID, root.Bucket, root.ID).ExecRelease())
	}
	return wrap(r.query(ctx, "UPDATE messages SET reply_count = ?, last_reply_at = ?, participants = participants + ? WHERE channel_id = ? AND bucket = ? AND id = ?",
		count, lastAt, []gocql.UUID{participant}, root.ChannelID, root.Bucket, root.ID).ExecRelease())
}

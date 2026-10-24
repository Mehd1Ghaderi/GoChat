package scylla

import (
	"context"
	"slices"

	"github.com/gocql/gocql"

	"github.com/ssenerg/gochat/internal/core/ids"
	"github.com/ssenerg/gochat/internal/core/models"
)

// Pin is a message pinned in a channel, Saved one a person saved.
type (
	Pin struct {
		MessageID gocql.UUID `db:"message_id"`
		RootID    gocql.UUID `db:"root_id"`
	}
	Saved struct {
		MessageID gocql.UUID `db:"message_id"`
		ChannelID gocql.UUID `db:"channel_id"`
		RootID    gocql.UUID `db:"root_id"`
		TeamID    gocql.UUID `db:"team_id"`
	}
)

// SetPinned pins m in its channel, or unpins it.
func (r *Repository) SetPinned(ctx context.Context, m *models.Message, pinned bool, by gocql.UUID) error {
	b := r.batch(ctx, true)
	if ids.IsZero(m.RootID) {
		b.Query("UPDATE messages SET pinned = ? WHERE channel_id = ? AND bucket = ? AND id = ?", pinned, m.ChannelID, ids.BucketOf(m.ID), m.ID)
	} else {
		b.Query("UPDATE replies SET pinned = ? WHERE channel_id = ? AND root_id = ? AND id = ?", pinned, m.ChannelID, m.RootID, m.ID)
	}
	switch {
	case !pinned:
		b.Query("DELETE FROM pinned_messages WHERE channel_id = ? AND message_id = ?", m.ChannelID, m.ID)
	case ids.IsZero(m.RootID):
		b.Query("INSERT INTO pinned_messages (channel_id, message_id, pinned_by) VALUES (?, ?, ?)", m.ChannelID, m.ID, by)
	default:
		b.Query("INSERT INTO pinned_messages (channel_id, message_id, root_id, pinned_by) VALUES (?, ?, ?, ?)", m.ChannelID, m.ID, m.RootID, by)
	}
	return r.execBatch(b)
}

// Unpin forgets the pin of a deleted message.
func (r *Repository) Unpin(ctx context.Context, channelID, messageID gocql.UUID) error {
	return wrap(r.query(ctx, "DELETE FROM pinned_messages WHERE channel_id = ? AND message_id = ?", channelID, messageID).ExecRelease())
}

// ListPins returns a channel's pins, newest message first, at most limit.
func (r *Repository) ListPins(ctx context.Context, channelID gocql.UUID, limit int) ([]*Pin, error) {
	var out []*Pin
	err := r.query(ctx, "SELECT message_id, root_id FROM pinned_messages WHERE channel_id = ? LIMIT ?", channelID, limit).SelectRelease(&out)
	return out, wrap(err)
}

// SetSaved saves m for a person, or forgets it.
func (r *Repository) SetSaved(ctx context.Context, userID gocql.UUID, m *models.Message, teamID gocql.UUID, saved bool) error {
	switch {
	case !saved:
		return wrap(r.query(ctx, "DELETE FROM saved_messages WHERE user_id = ? AND message_id = ?", userID, m.ID).ExecRelease())
	case ids.IsZero(m.RootID):
		return wrap(r.query(ctx, "INSERT INTO saved_messages (user_id, message_id, channel_id, team_id) VALUES (?, ?, ?, ?)",
			userID, m.ID, m.ChannelID, teamID).ExecRelease())
	default:
		return wrap(r.query(ctx, "INSERT INTO saved_messages (user_id, message_id, channel_id, root_id, team_id) VALUES (?, ?, ?, ?, ?)",
			userID, m.ID, m.ChannelID, m.RootID, teamID).ExecRelease())
	}
}

// ListSaved returns what a person saved, newest message first.
func (r *Repository) ListSaved(ctx context.Context, userID gocql.UUID, limit int) ([]*Saved, error) {
	var out []*Saved
	err := r.query(ctx, "SELECT message_id, channel_id, root_id, team_id FROM saved_messages WHERE user_id = ? LIMIT ?", userID, limit).SelectRelease(&out)
	return out, wrap(err)
}

// SavedAmong returns which of messageIDs a person saved.
func (r *Repository) SavedAmong(ctx context.Context, userID gocql.UUID, messageIDs []gocql.UUID) (map[gocql.UUID]bool, error) {
	out := map[gocql.UUID]bool{}
	for chunk := range slices.Chunk(messageIDs, 100) {
		iter := r.query(ctx, "SELECT message_id FROM saved_messages WHERE user_id = ? AND message_id IN ?", userID, chunk).Iter()
		var id gocql.UUID
		for iter.Scan(&id) {
			out[id] = true
		}
		if err := iter.Close(); err != nil {
			return nil, wrap(err)
		}
	}
	return out, nil
}

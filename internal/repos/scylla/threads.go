package scylla

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/gocql/gocql"

	"github.com/ssenerg/gochat/internal/core/ids"
)

// ThreadFollow is a thread one person follows: when it last had a reply,
// and when they last read it. Follows lapse 90 days after the last reply.
type ThreadFollow struct {
	ChannelID   gocql.UUID `db:"channel_id"`
	RootID      gocql.UUID `db:"root_id"`
	TeamID      gocql.UUID `db:"team_id"`
	LastReplyAt time.Time  `db:"last_reply_at"`
	ReadAt      time.Time  `db:"read_at"`
}

// TouchThread records a reply, made at at, for the followers of a thread, who
// follow it from now on; reader, when set, has read it.
func (r *Repository) TouchThread(ctx context.Context, followers []gocql.UUID, channelID, rootID, teamID gocql.UUID, at time.Time, reader gocql.UUID) error {
	for chunk := range slices.Chunk(followers, 50) {
		b := r.batch(ctx, false)
		for _, id := range chunk {
			if id == reader {
				b.Query("UPDATE user_threads SET team_id = ?, last_reply_at = ?, read_at = ? WHERE user_id = ? AND channel_id = ? AND root_id = ?",
					teamID, at, at, id, channelID, rootID)
			} else {
				b.Query("UPDATE user_threads SET team_id = ?, last_reply_at = ? WHERE user_id = ? AND channel_id = ? AND root_id = ?",
					teamID, at, id, channelID, rootID)
			}
		}
		if err := r.execBatch(b); err != nil {
			return err
		}
	}
	return nil
}

// MarkThreadRead notes that a person read a thread at at. A thread they do
// not follow gains no follow: it has no last reply.
func (r *Repository) MarkThreadRead(ctx context.Context, userID, channelID, rootID gocql.UUID, at time.Time) error {
	return wrap(r.query(ctx, "UPDATE user_threads SET read_at = ? WHERE user_id = ? AND channel_id = ? AND root_id = ?",
		at, userID, channelID, rootID).ExecRelease())
}

// GetThreadFollow reports whether a person follows a thread.
func (r *Repository) GetThreadFollow(ctx context.Context, userID, channelID, rootID gocql.UUID) (bool, error) {
	var last time.Time
	err := r.query(ctx, "SELECT last_reply_at FROM user_threads WHERE user_id = ? AND channel_id = ? AND root_id = ?",
		userID, channelID, rootID).Scan(&last)
	if errors.Is(err, gocql.ErrNotFound) {
		return false, nil
	}
	return !last.IsZero(), wrap(err)
}

// UnfollowThreads stops each of userIDs following a thread.
func (r *Repository) UnfollowThreads(ctx context.Context, userIDs []gocql.UUID, channelID, rootID gocql.UUID) error {
	for chunk := range slices.Chunk(userIDs, 50) {
		b := r.batch(ctx, false)
		for _, id := range chunk {
			if !ids.IsZero(id) {
				b.Query("DELETE FROM user_threads WHERE user_id = ? AND channel_id = ? AND root_id = ?", id, channelID, rootID)
			}
		}
		if err := r.execBatch(b); err != nil {
			return err
		}
	}
	return nil
}

// ListThreadFollows returns the threads a person follows, at most limit.
func (r *Repository) ListThreadFollows(ctx context.Context, userID gocql.UUID, limit int) ([]*ThreadFollow, error) {
	var out []*ThreadFollow
	err := r.query(ctx, "SELECT channel_id, root_id, team_id, last_reply_at, read_at FROM user_threads WHERE user_id = ? LIMIT ?",
		userID, limit).SelectRelease(&out)
	return out, wrap(err)
}

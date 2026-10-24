package scylla

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/gocql/gocql"
	"golang.org/x/sync/errgroup"

	"github.com/ssenerg/gochat/internal/core/ids"
	"github.com/ssenerg/gochat/internal/core/models"
)

// Posting records that a message holds a term. A message is filed under its
// team, or under each person of a direct conversation.
type Posting struct {
	MessageID gocql.UUID `db:"message_id"`
	ChannelID gocql.UUID `db:"channel_id"`
	RootID    gocql.UUID `db:"root_id"`
	UserID    gocql.UUID `db:"user_id"`
}

// Month numbers the month of t: the span of one index partition.
func Month(t time.Time) int32 {
	t = t.UTC()
	return int32(t.Year()*12 + int(t.Month()) - 1) //nolint:gosec // years fit
}

// MonthStart is the first instant of month.
func MonthStart(month int32) time.Time {
	return time.Date(int(month/12), time.Month(month%12+1), 1, 0, 0, 0, 0, time.UTC)
}

const indexWriters = 16

func (r *Repository) postings(ctx context.Context, scopes []gocql.UUID, terms []string, fn func(ctx context.Context, scope gocql.UUID, term string) error) error {
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(indexWriters)
	for _, scope := range scopes {
		for _, term := range terms {
			g.Go(func() error { return fn(gctx, scope, term) })
		}
	}
	return g.Wait()
}

// AddPostings files p's message under terms in each scope.
func (r *Repository) AddPostings(ctx context.Context, scopes []gocql.UUID, terms []string, p Posting) error {
	month := Month(p.MessageID.Time())
	return r.postings(ctx, scopes, terms, func(ctx context.Context, scope gocql.UUID, term string) error {
		// A reply alone has a root: a null would write a tombstone.
		if ids.IsZero(p.RootID) {
			return wrap(r.query(ctx, "INSERT INTO search_terms (scope, term, month, message_id, channel_id, user_id) VALUES (?, ?, ?, ?, ?, ?)",
				scope, term, month, p.MessageID, p.ChannelID, p.UserID).ExecRelease())
		}
		return wrap(r.query(ctx, "INSERT INTO search_terms (scope, term, month, message_id, channel_id, root_id, user_id) VALUES (?, ?, ?, ?, ?, ?, ?)",
			scope, term, month, p.MessageID, p.ChannelID, p.RootID, p.UserID).ExecRelease())
	})
}

// RemovePostings takes a message out from under terms in each scope.
func (r *Repository) RemovePostings(ctx context.Context, scopes []gocql.UUID, terms []string, messageID gocql.UUID) error {
	month := Month(messageID.Time())
	return r.postings(ctx, scopes, terms, func(ctx context.Context, scope gocql.UUID, term string) error {
		return wrap(r.query(ctx, "DELETE FROM search_terms WHERE scope = ? AND term = ? AND month = ? AND message_id = ?",
			scope, term, month, messageID).ExecRelease())
	})
}

const postingColumns = "message_id, channel_id, root_id, user_id"

// Postings returns up to limit messages filed under term in a scope's month,
// newest first, older than before when it is set.
func (r *Repository) Postings(ctx context.Context, scope gocql.UUID, term string, month int32, before gocql.UUID, limit int) ([]Posting, error) {
	var out []Posting
	var err error
	if ids.IsZero(before) {
		err = r.query(ctx, "SELECT "+postingColumns+" FROM search_terms WHERE scope = ? AND term = ? AND month = ? LIMIT ?",
			scope, term, month, limit).SelectRelease(&out)
	} else {
		err = r.query(ctx, "SELECT "+postingColumns+" FROM search_terms WHERE scope = ? AND term = ? AND month = ? AND message_id < ? LIMIT ?",
			scope, term, month, before, limit).SelectRelease(&out)
	}
	return out, wrap(err)
}

// HasPostings returns which of messageIDs are filed under term in a scope's
// month.
func (r *Repository) HasPostings(ctx context.Context, scope gocql.UUID, term string, month int32, messageIDs []gocql.UUID) (map[gocql.UUID]bool, error) {
	out := make(map[gocql.UUID]bool, len(messageIDs))
	for chunk := range slices.Chunk(messageIDs, 100) {
		iter := r.query(ctx, "SELECT message_id FROM search_terms WHERE scope = ? AND term = ? AND month = ? AND message_id IN ?",
			scope, term, month, chunk).Iter()
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

// ChannelsAfter pages through every channel, in token order, from the one
// after after.
func (r *Repository) ChannelsAfter(ctx context.Context, after gocql.UUID, limit int) ([]*models.Channel, error) {
	var out []*models.Channel
	var err error
	if ids.IsZero(after) {
		err = r.query(ctx, "SELECT "+channelColumns+" FROM channels LIMIT ?", limit).SelectRelease(&out)
	} else {
		err = r.query(ctx, "SELECT "+channelColumns+" FROM channels WHERE token(id) > token(?) LIMIT ?", after, limit).SelectRelease(&out)
	}
	return out, wrap(err)
}

// GetState reads a value the system keeps, empty when unset.
func (r *Repository) GetState(ctx context.Context, key string) (string, error) {
	var value string
	err := r.query(ctx, "SELECT value FROM system_state WHERE key = ?", key).Scan(&value)
	if errors.Is(err, gocql.ErrNotFound) {
		return "", nil
	}
	return value, wrap(err)
}

func (r *Repository) SetState(ctx context.Context, key, value string) error {
	return wrap(r.query(ctx, "INSERT INTO system_state (key, value) VALUES (?, ?)", key, value).ExecRelease())
}

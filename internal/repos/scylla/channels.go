package scylla

import (
	"context"

	"github.com/gocql/gocql"

	"github.com/ssenerg/gochat/internal/core/models"
	ierrors "github.com/ssenerg/gochat/internal/errors"
)

const channelColumns = "id, team_id, name, display_name, purpose, kind, created_by, created_at, last_message_id"

// NoTeam is the team of direct channels, which belong to none.
var NoTeam = gocql.UUID{}

type TeamChannel struct {
	TeamID    gocql.UUID         `db:"team_id"`
	ChannelID gocql.UUID         `db:"channel_id"`
	Kind      models.ChannelKind `db:"kind"`
}

type ChannelRead struct {
	ChannelID  gocql.UUID `db:"channel_id"`
	LastReadID gocql.UUID `db:"last_read_id"`
}

// CreateChannel claims the channel's name in its team and writes the channel
// with its first member.
func (r *Repository) CreateChannel(ctx context.Context, ch *models.Channel, creator *models.ChannelMember) error {
	ok, err := r.claim(ctx, "INSERT INTO channel_names (team_id, name, channel_id) VALUES (?, ?, ?) IF NOT EXISTS",
		"SELECT channel_id FROM channel_names WHERE team_id = ? AND name = ?", "channel_id", ch.ID, ch.TeamID, ch.Name)
	if err != nil {
		return err
	}
	if !ok {
		return ierrors.AlreadyExists("a channel with this name exists")
	}
	b := r.batch(ctx, true)
	b.Query("INSERT INTO channels (id, team_id, name, display_name, purpose, kind, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		ch.ID, ch.TeamID, ch.Name, ch.DisplayName, ch.Purpose, string(ch.Kind), ch.CreatedBy, ch.CreatedAt)
	b.Query("INSERT INTO team_channels (team_id, channel_id, kind) VALUES (?, ?, ?)", ch.TeamID, ch.ID, string(ch.Kind))
	if creator != nil {
		addMemberQueries(b, creator)
	}
	// A logged batch that timed out is replayed from the batch log, so its
	// claim stays.
	if err := r.execBatch(b); err != nil {
		r.release(err, "DELETE FROM channel_names WHERE team_id = ? AND name = ? IF channel_id = ?", ch.TeamID, ch.Name, ch.ID)
		return err
	}
	return nil
}

// CreateDirectChannel returns the direct channel of pair, creating it with
// members when it does not exist yet. A pair claimed by a creation that never
// completed is completed with the claimed id.
func (r *Repository) CreateDirectChannel(ctx context.Context, pair string, ch *models.Channel, members []*models.ChannelMember) (gocql.UUID, error) {
	row := make(map[string]any)
	q := r.query(ctx, "INSERT INTO direct_channels (pair, channel_id) VALUES (?, ?) IF NOT EXISTS", pair, ch.ID)
	applied, err := q.MapScanCAS(row)
	q.Release()
	if err != nil {
		return gocql.UUID{}, wrap(err)
	}
	if !applied {
		id, _ := row["channel_id"].(gocql.UUID)
		if _, err := r.GetChannel(ctx, id); !ierrors.Is(err, ierrors.CodeNotFound) {
			return id, err
		}
		ch.ID = id
		for _, m := range members {
			m.ChannelID = id
		}
	}
	b := r.batch(ctx, true)
	b.Query("INSERT INTO channels (id, team_id, name, display_name, purpose, kind, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		ch.ID, ch.TeamID, ch.Name, ch.DisplayName, ch.Purpose, string(ch.Kind), ch.CreatedBy, ch.CreatedAt)
	for _, m := range members {
		addMemberQueries(b, m)
	}
	if err := r.execBatch(b); err != nil {
		return gocql.UUID{}, err
	}
	return ch.ID, nil
}

func addMemberQueries(b *gocql.Batch, m *models.ChannelMember) {
	b.Query("INSERT INTO channel_members (channel_id, user_id, team_id, role, joined_at) VALUES (?, ?, ?, ?, ?)",
		m.ChannelID, m.UserID, m.TeamID, string(m.Role), m.JoinedAt)
	b.Query("INSERT INTO user_channels (user_id, team_id, channel_id, role, joined_at) VALUES (?, ?, ?, ?, ?)",
		m.UserID, m.TeamID, m.ChannelID, string(m.Role), m.JoinedAt)
}

func (r *Repository) GetChannel(ctx context.Context, id gocql.UUID) (*models.Channel, error) {
	var ch models.Channel
	if err := r.query(ctx, "SELECT "+channelColumns+" FROM channels WHERE id = ?", id).GetRelease(&ch); err != nil {
		return nil, wrap(err)
	}
	return &ch, nil
}

// GetChannels returns the channels found among ids, at most 100 per call.
func (r *Repository) GetChannels(ctx context.Context, ids []gocql.UUID) ([]*models.Channel, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > 100 {
		return nil, ierrors.InvalidArgument("too many channels")
	}
	var out []*models.Channel
	err := r.query(ctx, "SELECT "+channelColumns+" FROM channels WHERE id IN ?", ids).SelectRelease(&out)
	return out, wrap(err)
}

func (r *Repository) ListTeamChannels(ctx context.Context, teamID gocql.UUID) ([]*TeamChannel, error) {
	var out []*TeamChannel
	err := r.query(ctx, "SELECT team_id, channel_id, kind FROM team_channels WHERE team_id = ?", teamID).SelectRelease(&out)
	return out, wrap(err)
}

func (r *Repository) AddChannelMember(ctx context.Context, m *models.ChannelMember) error {
	b := r.batch(ctx, true)
	addMemberQueries(b, m)
	return r.execBatch(b)
}

func (r *Repository) RemoveChannelMember(ctx context.Context, m *models.ChannelMember) error {
	b := r.batch(ctx, true)
	b.Query("DELETE FROM channel_members WHERE channel_id = ? AND user_id = ?", m.ChannelID, m.UserID)
	b.Query("DELETE FROM user_channels WHERE user_id = ? AND team_id = ? AND channel_id = ?", m.UserID, m.TeamID, m.ChannelID)
	b.Query("DELETE FROM channel_reads WHERE user_id = ? AND channel_id = ?", m.UserID, m.ChannelID)
	return r.execBatch(b)
}

func (r *Repository) ListChannelMembers(ctx context.Context, channelID gocql.UUID) ([]*models.ChannelMember, error) {
	var out []*models.ChannelMember
	err := r.query(ctx, "SELECT channel_id, user_id, team_id, role, joined_at FROM channel_members WHERE channel_id = ?", channelID).SelectRelease(&out)
	return out, wrap(err)
}

// ListUserChannels returns the user's memberships in teamID.
func (r *Repository) ListUserChannels(ctx context.Context, userID, teamID gocql.UUID) ([]*models.ChannelMember, error) {
	var out []*models.ChannelMember
	err := r.query(ctx, "SELECT channel_id, user_id, team_id, role, joined_at FROM user_channels WHERE user_id = ? AND team_id = ?", userID, teamID).SelectRelease(&out)
	return out, wrap(err)
}

// The last message and last read are written at their message's time, so the
// newest one wins whatever order the writes arrive in.

func (r *Repository) SetLastMessage(ctx context.Context, channelID, messageID gocql.UUID) error {
	return wrap(r.query(ctx, "UPDATE channels USING TIMESTAMP ? SET last_message_id = ? WHERE id = ?",
		messageID.Time().UnixMicro(), messageID, channelID).ExecRelease())
}

func (r *Repository) SetLastRead(ctx context.Context, userID, channelID, messageID gocql.UUID) error {
	return wrap(r.query(ctx, "UPDATE channel_reads USING TIMESTAMP ? SET last_read_id = ? WHERE user_id = ? AND channel_id = ?",
		messageID.Time().UnixMicro(), messageID, userID, channelID).ExecRelease())
}

func (r *Repository) ListLastReads(ctx context.Context, userID gocql.UUID) ([]*ChannelRead, error) {
	var out []*ChannelRead
	err := r.query(ctx, "SELECT channel_id, last_read_id FROM channel_reads WHERE user_id = ?", userID).SelectRelease(&out)
	return out, wrap(err)
}

func (r *Repository) GetChannelIDByName(ctx context.Context, teamID gocql.UUID, name string) (gocql.UUID, error) {
	var id gocql.UUID
	if err := r.query(ctx, "SELECT channel_id FROM channel_names WHERE team_id = ? AND name = ?", teamID, name).Scan(&id); err != nil {
		return gocql.UUID{}, wrap(err)
	}
	return id, nil
}

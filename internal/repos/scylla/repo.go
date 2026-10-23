package scylla

import (
	"context"
	"errors"

	"github.com/gocql/gocql"
	"github.com/scylladb/gocqlx/v3"
	"go.uber.org/zap"

	ierrors "github.com/ssenerg/gochat/internal/errors"
)

type Repository struct {
	logger  *zap.Logger
	session gocqlx.Session
}

func NewRepository(logger *zap.Logger, cfg *Config) (*Repository, error) {
	session, err := Open(logger, cfg)
	if err != nil {
		return nil, err
	}
	return &Repository{logger: logger, session: session}, nil
}

func (r *Repository) Stop() {
	r.session.Close()
	r.logger.Info("session closed")
}

func (r *Repository) Ping(ctx context.Context) error {
	var now int64
	if err := r.session.ContextQuery(ctx, "SELECT toUnixTimestamp(now()) FROM system.local", nil).Scan(&now); err != nil {
		return err
	}
	return nil
}

func (r *Repository) query(ctx context.Context, stmt string, args ...any) *gocqlx.Queryx {
	return r.session.ContextQuery(ctx, stmt, nil).Bind(args...)
}

// batch groups writes; logged batches are atomic across partitions.
func (r *Repository) batch(ctx context.Context, logged bool) *gocql.Batch {
	kind := gocql.UnloggedBatch
	if logged {
		kind = gocql.LoggedBatch
	}
	return r.session.Session.Batch(kind).WithContext(ctx)
}

func (r *Repository) execBatch(b *gocql.Batch) error {
	return wrap(r.session.Session.ExecuteBatch(b))
}

func wrap(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, gocql.ErrNotFound):
		return ierrors.NotFound("not found")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return &ierrors.Error{Code: ierrors.CodeDeadlineExceeded, Message: "request timed out", Cause: err}
	default:
		return ierrors.Internal(err)
	}
}

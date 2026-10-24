// Package files decides who may upload to a channel and download from it.
// The bytes themselves stream between the transport and the object store.
package files

import (
	"context"
	"time"

	"github.com/anthdm/hollywood/actor"
	"github.com/gocql/gocql"
	"go.uber.org/zap"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/actors"
	"github.com/ssenerg/gochat/internal/authz"
	"github.com/ssenerg/gochat/internal/core/ids"
	"github.com/ssenerg/gochat/internal/core/models"
	"github.com/ssenerg/gochat/internal/core/validate"
	ierrors "github.com/ssenerg/gochat/internal/errors"
	"github.com/ssenerg/gochat/internal/repos/scylla"
	"github.com/ssenerg/gochat/internal/services/channels"
	"github.com/ssenerg/gochat/internal/services/convert"
)

type Repo interface {
	InsertAttachment(ctx context.Context, a *models.Attachment) error
	GetAttachment(ctx context.Context, id gocql.UUID) (*models.Attachment, error)
	DeleteAttachment(ctx context.Context, a *models.Attachment) error
	ListPendingUploads(ctx context.Context, day int32) ([]*scylla.PendingUpload, error)
	DeleteOrphan(ctx context.Context, id gocql.UUID) (bool, error)
	ForgetPending(ctx context.Context, p *scylla.PendingUpload) error
}

type Storage interface {
	Delete(ctx context.Context, key string) error
}

type Deps struct {
	Logger   *zap.Logger
	Cfg      *Config
	Repo     Repo
	Storage  Storage
	Authz    *authz.Authorizer
	Engine   *actor.Engine
	Channels *actor.PID
}

// BeginUpload replies with an *Upload when the caller may upload to the
// channel. A voice message needs a channel that allows voice.
type BeginUpload struct {
	P          *models.Principal
	ChannelID  string
	Name       string
	Voice      bool
	DurationMS uint32
}

type Upload struct {
	ID        gocql.UUID
	ChannelID gocql.UUID
	Name      string
	ObjectKey string
	// Voice uploads must be audio, which the transport checks.
	Voice      bool
	DurationMS int
}

// CompleteUpload records a stored upload and replies with its *pb.Attachment.
type CompleteUpload struct {
	P           *models.Principal
	Upload      *Upload
	Size        int64
	ContentType string
}

// OpenDownload replies with the *models.Attachment the caller may read.
type OpenDownload struct {
	P            *models.Principal
	AttachmentID string
}

func Pool(d *Deps) actor.Producer {
	return actors.NewPool(d.Cfg.Workers, func() actor.Receiver { return &worker{Deps: d} },
		actor.WithMiddleware(actors.Recover(d.Logger)))
}

type worker struct{ *Deps }

func (w *worker) Receive(c *actor.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), w.Cfg.OpTimeout)
	defer cancel()
	var (
		out any
		err error
	)
	switch msg := c.Message().(type) {
	case BeginUpload:
		out, err = w.begin(ctx, msg)
	case CompleteUpload:
		out, err = w.complete(ctx, msg)
	case OpenDownload:
		out, err = w.open(ctx, msg)
	default:
		return
	}
	if err != nil {
		if e := ierrors.As(err); e.Code == ierrors.CodeInternal {
			w.Logger.Error("files request failed", e.Fields()...)
		}
		actors.Reply(c, err)
		return
	}
	actors.Reply(c, out)
}

func (w *worker) access(ctx context.Context, p *models.Principal, channelID gocql.UUID) (*channels.AccessResult, error) {
	return actors.Ask[*channels.AccessResult](ctx, w.Engine, w.Channels, channels.Access{Target: channels.Target{P: p, ChannelID: channelID}})
}

func (w *worker) begin(ctx context.Context, msg BeginUpload) (*Upload, error) {
	channelID, ok := ids.Parse(msg.ChannelID)
	if !ok {
		return nil, ierrors.InvalidArgument("invalid channel id")
	}
	acc, err := w.access(ctx, msg.P, channelID)
	if err != nil {
		return nil, err
	}
	if acc.Role == "" {
		return nil, ierrors.NotFound("channel not found")
	}
	if !w.Authz.Can(authz.ObjFile, authz.ActUpload, string(acc.Role), string(msg.P.Role)) {
		return nil, ierrors.PermissionDenied()
	}
	up := &Upload{ID: ids.New(), ChannelID: channelID, Name: validate.Filename(msg.Name), Voice: msg.Voice}
	up.ObjectKey = "attachments/" + channelID.String() + "/" + up.ID.String()
	if msg.Voice {
		if acc.Channel.VoiceDisabled {
			return nil, ierrors.FailedPrecondition("voice messages are turned off in this channel")
		}
		if time.Duration(msg.DurationMS)*time.Millisecond > w.Cfg.MaxVoiceDuration {
			return nil, ierrors.InvalidArgument("the voice message is too long")
		}
		up.DurationMS = int(msg.DurationMS)
	}
	return up, nil
}

func (w *worker) complete(ctx context.Context, msg CompleteUpload) (*pb.Attachment, error) {
	u := msg.Upload
	att := &models.Attachment{
		ID:          u.ID,
		ChannelID:   u.ChannelID,
		UploaderID:  msg.P.UserID,
		Name:        u.Name,
		ContentType: msg.ContentType,
		Size:        msg.Size,
		ObjectKey:   u.ObjectKey,
		CreatedAt:   time.Now().UTC(),
		Voice:       u.Voice,
		DurationMS:  u.DurationMS,
	}
	if err := w.Repo.InsertAttachment(ctx, att); err != nil {
		return nil, err
	}
	return convert.Attachment(att), nil
}

var errNoFile = ierrors.NotFound("file not found")

func (w *worker) open(ctx context.Context, msg OpenDownload) (*models.Attachment, error) {
	id, ok := ids.Parse(msg.AttachmentID)
	if !ok {
		return nil, errNoFile
	}
	att, err := w.Repo.GetAttachment(ctx, id)
	if err != nil {
		if ierrors.Is(err, ierrors.CodeNotFound) {
			return nil, errNoFile
		}
		return nil, err
	}
	if ids.IsZero(att.MessageID) && att.UploaderID != msg.P.UserID {
		return nil, errNoFile
	}
	acc, err := w.access(ctx, msg.P, att.ChannelID)
	if err != nil {
		if ierrors.Is(err, ierrors.CodeNotFound) || ierrors.Is(err, ierrors.CodePermissionDenied) {
			return nil, errNoFile
		}
		return nil, err
	}
	if acc.Role == "" || !w.Authz.Can(authz.ObjMessage, authz.ActRead, string(acc.Role), string(msg.P.Role)) {
		return nil, errNoFile
	}
	return att, nil
}

type sweep struct{}

// Janitor deletes uploads never posted within OrphanAfter.
func Janitor(d *Deps) actor.Producer {
	return func() actor.Receiver { return &janitor{Deps: d} }
}

type janitor struct {
	*Deps
	repeater *actor.SendRepeater
	running  bool
}

func (j *janitor) Receive(c *actor.Context) {
	switch c.Message().(type) {
	case actor.Started:
		r := c.SendRepeat(c.PID(), sweep{}, j.Cfg.JanitorInterval)
		j.repeater = &r
	case actor.Stopped:
		if j.repeater != nil {
			j.repeater.Stop()
		}
	case sweep:
		if j.running {
			return
		}
		j.running = true
		pid, engine := c.PID(), c.Engine()
		go func() {
			defer engine.Send(pid, swept{})
			j.sweep()
		}()
	case swept:
		j.running = false
	}
}

type swept struct{}

func (j *janitor) sweep() {
	ctx, cancel := context.WithTimeout(context.Background(), j.Cfg.JanitorInterval)
	defer cancel()
	cutoff := time.Now().Add(-j.Cfg.OrphanAfter)
	last := scylla.PendingDay(cutoff)
	removed := 0
	for day := last - 7; day <= last; day++ {
		pending, err := j.Repo.ListPendingUploads(ctx, day)
		if err != nil {
			j.Logger.Warn("janitor failed to list uploads", zap.Error(err))
			return
		}
		for _, p := range pending {
			if p.CreatedAt.After(cutoff) {
				continue
			}
			// The row goes first, and only while unposted, so a post racing
			// the janitor either keeps its file or fails.
			orphan, err := j.Repo.DeleteOrphan(ctx, p.ID)
			if err != nil {
				j.Logger.Warn("janitor failed to delete upload", zap.Error(err))
				continue
			}
			if orphan {
				if err := j.Storage.Delete(ctx, p.ObjectKey); err != nil {
					j.Logger.Warn("janitor failed to delete object", zap.Error(err))
					continue
				}
				removed++
			}
			if err := j.Repo.ForgetPending(ctx, p); err != nil {
				j.Logger.Warn("janitor failed to update the pending list", zap.Error(err))
			}
		}
	}
	if removed > 0 {
		j.Logger.Info("removed orphaned uploads", zap.Int("count", removed))
	}
}

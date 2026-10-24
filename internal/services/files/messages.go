package files

import (
	"github.com/gocql/gocql"

	"github.com/ssenerg/gochat/internal/core/models"
	ierrors "github.com/ssenerg/gochat/internal/errors"
)

// BeginUpload replies with an *Upload when the caller may upload to the
// channel.
type BeginUpload struct {
	P         *models.Principal
	ChannelID string
	Name      string
}

type Upload struct {
	ID        gocql.UUID
	ChannelID gocql.UUID
	Name      string
	ObjectKey string
	// Room is how many bytes the uploader may still store, -1 for no limit.
	Room int64
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

// Usage replies with the *StorageUsage of the caller, or of UserID for
// someone who may manage any.
type Usage struct {
	P      *models.Principal
	UserID string
}

type StorageUsage struct {
	Used, Quota int64
	// Custom is set when Quota is the user's own, not the default.
	Custom bool
}

// SetQuota gives a user a quota of their own, or with zero the default.
type SetQuota struct {
	P      *models.Principal
	UserID string
	Quota  int64
}

type Done struct{}

var (
	ErrQuota  = ierrors.ResourceExhausted("not enough storage left: delete some files to make room")
	errNoUser = ierrors.NotFound("user not found")
)

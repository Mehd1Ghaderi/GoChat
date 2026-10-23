package directory

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"

	"github.com/gocql/gocql"
	"go.uber.org/zap"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/authz"
	"github.com/ssenerg/gochat/internal/core/models"
	"github.com/ssenerg/gochat/internal/core/photo"
	ierrors "github.com/ssenerg/gochat/internal/errors"
	"github.com/ssenerg/gochat/internal/services/convert"
)

type Storage interface {
	Put(ctx context.Context, key string, r io.Reader, contentType string) (int64, error)
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
}

var errNoPhoto = ierrors.NotFound("photo not found")

func userPhotoKey(id gocql.UUID, version string) string {
	return "photos/users/" + id.String() + "/" + version
}

func teamPhotoKey(id gocql.UUID, version string) string {
	return "photos/teams/" + id.String() + "/" + version
}

// storePhoto checks a processed photo and stores it, returning its version.
func (w *worker) storePhoto(ctx context.Context, data []byte, key func(string) string) (string, error) {
	ctype, err := photo.Check(data)
	if err != nil {
		return "", ierrors.InvalidArgument(err.Error())
	}
	version := photo.Version(data)
	if _, err := w.Storage.Put(ctx, key(version), bytes.NewReader(data), ctype); err != nil {
		return "", ierrors.Internal(err)
	}
	return version, nil
}

func (w *worker) dropPhoto(ctx context.Context, key string) {
	if err := w.Storage.Delete(ctx, key); err != nil {
		w.Logger.Warn("failed to delete a replaced photo", zap.String("key", key), zap.Error(err))
	}
}

func (w *worker) setPhoto(ctx context.Context, p *models.Principal, data []byte) (*pb.User, error) {
	u, err := w.Repo.GetUser(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	version := ""
	if data != nil {
		if version, err = w.storePhoto(ctx, data, func(v string) string { return userPhotoKey(u.ID, v) }); err != nil {
			return nil, err
		}
	}
	if version == u.Photo {
		return convert.User(u), nil
	}
	if err := w.Repo.SetUserPhoto(ctx, u.ID, version, time.Now().UTC()); err != nil {
		if version != "" {
			w.dropPhoto(ctx, userPhotoKey(u.ID, version))
		}
		return nil, err
	}
	w.Users.Invalidate(u.ID)
	if u.Photo != "" {
		w.dropPhoto(ctx, userPhotoKey(u.ID, u.Photo))
	}
	u.Photo = version
	return convert.User(u), nil
}

func (w *worker) setTeamPhoto(ctx context.Context, p *models.Principal, rawID string, data []byte) (*pb.Team, error) {
	teamID, err := parseID(rawID, "team")
	if err != nil {
		return nil, err
	}
	m, err := w.membership(ctx, teamID, p.UserID)
	if err != nil && p.Role != models.SystemRoleAdmin {
		return nil, err
	}
	var role models.TeamRole
	if m != nil {
		role = m.Role
	}
	if !w.Authz.Can(authz.ObjTeam, authz.ActUpdate, string(role), string(p.Role)) {
		return nil, ierrors.PermissionDenied()
	}
	team, err := w.Repo.GetTeam(ctx, teamID)
	if err != nil {
		return nil, err
	}
	version := ""
	if data != nil {
		if version, err = w.storePhoto(ctx, data, func(v string) string { return teamPhotoKey(teamID, v) }); err != nil {
			return nil, err
		}
	}
	if version != team.Photo {
		if err := w.Repo.SetTeamPhoto(ctx, teamID, version); err != nil {
			if version != "" {
				w.dropPhoto(ctx, teamPhotoKey(teamID, version))
			}
			return nil, err
		}
		if team.Photo != "" {
			w.dropPhoto(ctx, teamPhotoKey(teamID, team.Photo))
		}
		team.Photo = version
	}
	return convert.Team(team, role), nil
}

// getPhoto returns the current photo of a user, or of a team the caller
// belongs to. Current says whether it is the version asked for.
func (w *worker) getPhoto(ctx context.Context, msg GetPhoto) (*Photo, error) {
	if !photo.ValidVersion(msg.Version) {
		return nil, errNoPhoto
	}
	var key string
	if msg.UserID != "" {
		id, err := parseID(msg.UserID, "user")
		if err != nil {
			return nil, err
		}
		u, err := w.Users.Get(ctx, id)
		if err != nil || u.Photo == "" {
			return nil, errNoPhoto
		}
		key, msg.Version = userPhotoKey(id, u.Photo), u.Photo
	} else {
		id, err := parseID(msg.TeamID, "team")
		if err != nil {
			return nil, err
		}
		if _, err := w.membership(ctx, id, msg.P.UserID); err != nil && msg.P.Role != models.SystemRoleAdmin {
			return nil, errNoPhoto
		}
		team, err := w.Repo.GetTeam(ctx, id)
		if err != nil || team.Photo == "" {
			return nil, errNoPhoto
		}
		key, msg.Version = teamPhotoKey(id, team.Photo), team.Photo
	}
	body, err := w.Storage.Get(ctx, key)
	if err != nil {
		return nil, ierrors.Internal(err)
	}
	defer func() { _ = body.Close() }()
	data, err := io.ReadAll(io.LimitReader(body, photo.MaxStored+1))
	if err != nil {
		return nil, ierrors.Internal(err)
	}
	ctype := http.DetectContentType(data)
	if len(data) > photo.MaxStored || (ctype != "image/jpeg" && ctype != "image/png") {
		return nil, errNoPhoto
	}
	return &Photo{Data: data, ContentType: ctype, Version: msg.Version}, nil
}

package web

import (
	"net/http"
	"strconv"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/client/drpc"
	ierrors "github.com/ssenerg/gochat/internal/errors"
)

type adminData struct {
	Page
	Query string
	Found *adminUser
}

type adminUser struct {
	User     *pb.User
	Email    string
	Sessions []*pb.SessionInfo
	Storage  *pb.GetStorageUsageResponse
	// Self is set when the admin found themself.
	Self          bool
	Status, Error string
}

// admin returns the signed-in system admin, or answers as if the page did
// not exist.
func (h *Handler) admin(w http.ResponseWriter, r *http.Request) *pb.User {
	u := h.signedIn(w, r)
	if u != nil && u.Role != pb.SystemRole_SYSTEM_ROLE_ADMIN {
		h.notFound(w, r)
		return nil
	}
	return u
}

// findUser gathers what the admin page shows of a user.
func (h *Handler) findUser(r *http.Request, me *pb.User, req *pb.FindUserRequest) (*adminUser, error) {
	ctx, cancel := h.ctx(r)
	defer cancel()
	found, err := h.client.Admin.FindUser(ctx, req)
	if err != nil {
		return nil, err
	}
	id := found.User.Id
	sessions, err := h.client.Auth.ListSessions(ctx, &pb.ListSessionsRequest{UserId: id})
	if err != nil {
		return nil, err
	}
	storage, err := h.client.Files.GetStorageUsage(ctx, &pb.GetStorageUsageRequest{UserId: id})
	if err != nil {
		return nil, err
	}
	return &adminUser{User: found.User, Email: found.Email, Sessions: sessions.Sessions, Storage: storage, Self: id == me.Id}, nil
}

func (h *Handler) adminUsers(w http.ResponseWriter, r *http.Request) {
	me := h.admin(w, r)
	if me == nil {
		return
	}
	data := adminData{Page: Page{Title: "Users", CSRF: h.csrfToken(w, r), User: me}, Query: r.URL.Query().Get("q")}
	if data.Query != "" {
		found, err := h.findUser(r, me, &pb.FindUserRequest{Key: &pb.FindUserRequest_Login{Login: data.Query}})
		switch {
		case err == nil:
			data.Found = found
		case drpc.Err(err).Code == ierrors.CodeNotFound:
			data.Error = "No one has that username or email."
		default:
			h.fail(w, r, err)
			return
		}
	}
	h.page(w, r, http.StatusOK, "admin", data)
}

// adminCard answers with the user's card after a change, err telling why it
// failed.
func (h *Handler) adminCard(w http.ResponseWriter, r *http.Request, me *pb.User, err error, ok string) {
	found, ferr := h.findUser(r, me, &pb.FindUserRequest{Key: &pb.FindUserRequest_UserId{UserId: r.PathValue("user")}})
	if ferr != nil {
		h.fail(w, r, ferr)
		return
	}
	if err != nil {
		found.Error = drpc.Err(err).Message
	} else {
		found.Status = ok
	}
	h.fragment(w, http.StatusOK, "admin-user", found)
}

func (h *Handler) adminTarget(w http.ResponseWriter, r *http.Request) *pb.User {
	me := h.admin(w, r)
	if me != nil && !uuidRe.MatchString(r.PathValue("user")) {
		h.notFound(w, r)
		return nil
	}
	return me
}

func (h *Handler) adminDeactivate(w http.ResponseWriter, r *http.Request) {
	me := h.adminTarget(w, r)
	if me == nil {
		return
	}
	deactivate := r.PostFormValue("deactivated") == "1"
	ctx, cancel := h.ctx(r)
	defer cancel()
	_, err := h.client.Admin.SetUserDeactivated(ctx, &pb.SetUserDeactivatedRequest{UserId: r.PathValue("user"), Deactivated: deactivate})
	ok := "Reactivated."
	if deactivate {
		ok = "Deactivated and signed out everywhere."
	}
	h.adminCard(w, r, me, err, ok)
}

func (h *Handler) adminRevokeSessions(w http.ResponseWriter, r *http.Request) {
	me := h.adminTarget(w, r)
	if me == nil {
		return
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	_, err := h.client.Auth.RevokeSessions(ctx, &pb.RevokeSessionsRequest{UserId: r.PathValue("user")})
	h.adminCard(w, r, me, err, "Signed out everywhere.")
}

func (h *Handler) adminQuota(w http.ResponseWriter, r *http.Request) {
	me := h.adminTarget(w, r)
	if me == nil {
		return
	}
	var quota int64
	if r.PostFormValue("default") != "1" {
		mib, err := strconv.ParseInt(r.PostFormValue("mib"), 10, 64)
		if err != nil || mib < 1 || mib > 1<<30 {
			h.adminCard(w, r, me, ierrors.InvalidArgument("Give the quota as a whole number of MiB."), "")
			return
		}
		quota = mib << 20
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	_, err := h.client.Admin.SetStorageQuota(ctx, &pb.SetStorageQuotaRequest{UserId: r.PathValue("user"), Quota: quota})
	h.adminCard(w, r, me, err, "Quota saved.")
}

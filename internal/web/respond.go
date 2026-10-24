package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"go.uber.org/zap"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/client/drpc"
	ierrors "github.com/ssenerg/gochat/internal/errors"
	"github.com/ssenerg/gochat/internal/web/render"
)

// Page is what every full page carries.
type Page struct {
	Title string
	CSRF  string
	User  *pb.User
	Error string
}

func (h *Handler) page(w http.ResponseWriter, r *http.Request, status int, name string, data any) {
	var buf bytes.Buffer
	if err := h.render.Page(&buf, name, data); err != nil {
		h.logger.Error("failed to render page", zap.String("page", name), zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func (h *Handler) fragment(w http.ResponseWriter, status int, name string, data any) {
	var buf bytes.Buffer
	if err := h.render.Fragment(&buf, name, data); err != nil {
		h.logger.Error("failed to render fragment", zap.String("fragment", name), zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

// text answers an htmx request with escaped plain text for its target.
func (h *Handler) text(w http.ResponseWriter, status int, kind, msg string) {
	h.fragment(w, status, "status", render.Toast{Kind: kind, Text: msg})
}

// navigate sends the browser to path: htmx swaps the main pane, a plain
// request follows a redirect.
func (h *Handler) navigate(w http.ResponseWriter, r *http.Request, path string, closeModal bool) {
	if !isHTMX(r) {
		http.Redirect(w, r, path, http.StatusSeeOther) //nolint:gosec // callers build path from validated parts
		return
	}
	loc, _ := json.Marshal(map[string]string{"path": path, "target": "#main"})
	w.Header().Set("HX-Location", string(loc))
	if closeModal {
		w.Header().Set("HX-Trigger", "closeModal")
	}
	w.WriteHeader(http.StatusNoContent)
}

// reload sends the browser to path with a full page load.
func (h *Handler) reload(w http.ResponseWriter, r *http.Request, path string) {
	if isHTMX(r) {
		w.Header().Set("HX-Redirect", path)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
}

// fail answers a failed call: a sign-in redirect when the session is gone,
// otherwise an error page, or for htmx, a status with the message in X-Error.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	e := drpc.Err(err)
	status := statusOf(e.Code)
	switch {
	case e.Code == ierrors.CodeUnauthenticated:
		h.me.Delete(h.sessionToken(r))
		next := "/login"
		if r.Method == http.MethodGet && !isHTMX(r) {
			next += "?next=" + url.QueryEscape(r.URL.RequestURI())
		}
		h.reload(w, r, next)
		return
	case status >= 500:
		h.logger.Error("request failed", zap.String("path", r.URL.Path), zap.Error(err))
	}
	if isHTMX(r) {
		w.Header().Set("X-Error", headerSafe(e.Message))
		http.Error(w, e.Message, status)
		return
	}
	heading := http.StatusText(status)
	h.page(w, r, status, "error", struct {
		Page
		Heading, Message string
	}{Page: Page{Title: heading, CSRF: h.csrfToken(w, r)}, Heading: heading, Message: e.Message})
}

var errNoPage = ierrors.NotFound("This page does not exist.")

func (h *Handler) notFound(w http.ResponseWriter, r *http.Request) {
	h.fail(w, r, errNoPage)
}

func statusOf(c ierrors.Code) int {
	switch c {
	case ierrors.CodeInvalidArgument:
		return http.StatusBadRequest
	case ierrors.CodeUnauthenticated:
		return http.StatusUnauthorized
	case ierrors.CodePermissionDenied:
		return http.StatusForbidden
	case ierrors.CodeNotFound:
		return http.StatusNotFound
	case ierrors.CodeAlreadyExists, ierrors.CodeFailedPrecondition:
		return http.StatusConflict
	case ierrors.CodeResourceExhausted:
		return http.StatusTooManyRequests
	case ierrors.CodeDeadlineExceeded:
		return http.StatusGatewayTimeout
	case ierrors.CodeUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func headerSafe(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r > 0x7e {
			return -1
		}
		return r
	}, s)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// formError reports whether err is the person's to fix, and its message.
func formError(err error) (string, bool) {
	e := drpc.Err(err)
	switch e.Code {
	case ierrors.CodeInvalidArgument, ierrors.CodeAlreadyExists, ierrors.CodeFailedPrecondition,
		ierrors.CodePermissionDenied, ierrors.CodeResourceExhausted, ierrors.CodeNotFound:
		return e.Message, true
	}
	return "", false
}

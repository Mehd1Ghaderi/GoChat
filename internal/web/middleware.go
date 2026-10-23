package web

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/ssenerg/gochat/internal/core/secure"
)

const (
	csrfHeader = "X-CSRF-Token"
	csrfField  = "_csrf"
)

func (h *Handler) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(v)
				}
				h.logger.Error("handler panicked", zap.String("path", r.URL.Path), zap.Any("panic", v), zap.Stack("stack"))
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Content-Security-Policy", h.csp)
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("Referrer-Policy", "same-origin")
		hd.Set("Cross-Origin-Opener-Policy", "same-origin")
		hd.Set("Cross-Origin-Resource-Policy", "same-origin")
		hd.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=(), interest-cohort=()")
		if h.secure {
			hd.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		hd.Set("Cache-Control", "no-store")
		hd.Set("Vary", "HX-Request, HX-Target")
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) throttle(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ok, wait := h.reqs.Allow(h.clientIP(r)); !ok {
			retryAfter(w, wait)
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func retryAfter(w http.ResponseWriter, wait time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
}

// limited applies the stricter budget of the authentication endpoints.
func (h *Handler) limited(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ok, wait := h.auth.Allow(h.clientIP(r)); !ok {
			retryAfter(w, wait)
			w.Header().Set("X-Error", "Too many attempts, wait a minute.")
			http.Error(w, "too many attempts, wait a minute", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

const maxFormBytes = 1 << 20

// csrf requires a same-origin request carrying the token of the CSRF cookie,
// in the X-CSRF-Token header (htmx) or the _csrf field (plain forms).
func (h *Handler) csrf(next http.Handler) http.Handler { return h.guard(next, false) }

// csrfMultipart guards a handler that reads a multipart body and bounds it
// itself.
func (h *Handler) csrfMultipart(next http.Handler) http.Handler { return h.guard(next, true) }

func (h *Handler) guard(next http.Handler, multipart bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.sameOrigin(r) {
			h.logger.Warn("cross-origin request refused", zap.String("path", r.URL.Path), zap.String("origin", r.Header.Get("Origin")))
			http.Error(w, "cross-origin request refused", http.StatusForbidden)
			return
		}
		if !multipart {
			if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
				http.Error(w, "unsupported request body", http.StatusUnsupportedMediaType)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
		}
		c, err := r.Cookie(h.cookieName("csrf"))
		if err != nil || !secure.ValidToken(c.Value) {
			h.refuseCSRF(w, r)
			return
		}
		sent := r.Header.Get(csrfHeader)
		if sent == "" && strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
			sent = r.PostFormValue(csrfField)
		}
		if !secure.Equal(sent, c.Value) {
			h.refuseCSRF(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) refuseCSRF(w http.ResponseWriter, r *http.Request) {
	if isHTMX(r) {
		w.Header().Set("X-Error", "The page expired, reload it.")
		http.Error(w, "the page expired, reload it", http.StatusForbidden)
		return
	}
	http.Redirect(w, r, safeNext(r.URL.Path), http.StatusSeeOther) //nolint:gosec // safeNext keeps it on this site
}

func (h *Handler) sameOrigin(r *http.Request) bool {
	if o := r.Header.Get("Origin"); o != "" {
		return o == h.origin
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
		return true
	}
	return false
}

// cookieName gives a cookie the __Host- prefix when cookies are Secure, which
// binds it to this host and path /.
func (h *Handler) cookieName(name string) string {
	if h.secure {
		return "__Host-gochat_" + name
	}
	return "gochat_" + name
}

func (h *Handler) setCookie(w http.ResponseWriter, name, value string, maxAge time.Duration, sameSite http.SameSite) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure follows the public URL's scheme
		Name:     h.cookieName(name),
		Value:    value,
		Path:     "/",
		MaxAge:   int(maxAge / time.Second),
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: sameSite,
	})
}

func (h *Handler) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure follows the public URL's scheme
		Name: h.cookieName(name), Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: h.secure, SameSite: http.SameSiteLaxMode,
	})
}

// csrfToken returns the request's CSRF token, issuing one when missing.
func (h *Handler) csrfToken(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(h.cookieName("csrf")); err == nil && secure.ValidToken(c.Value) {
		return c.Value
	}
	// Lax: a visit from another site does not see a Strict cookie, and the
	// new token it got would break every other open tab.
	token := secure.Token()
	h.setCookie(w, "csrf", token, 0, http.SameSiteLaxMode)
	return token
}

func (h *Handler) sessionToken(r *http.Request) string {
	c, err := r.Cookie(h.cookieName("session"))
	if err != nil || !secure.ValidToken(c.Value) {
		return ""
	}
	return c.Value
}

package web

import (
	"context"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/anthdm/hollywood/actor"
	"go.uber.org/zap"
	"golang.org/x/time/rate"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/client/drpc"
	"github.com/ssenerg/gochat/internal/core/cache"
	ierrors "github.com/ssenerg/gochat/internal/errors"
	"github.com/ssenerg/gochat/internal/web/gateway"
	"github.com/ssenerg/gochat/internal/web/render"
	"github.com/ssenerg/gochat/internal/web/views"
)

const meCacheTTL = 30 * time.Second

type Handler struct {
	logger  *zap.Logger
	cfg     *Config
	client  *drpc.Client
	render  *render.Renderer
	engine  *actor.Engine
	gw      *gateway.Deps
	secure  bool
	origin  string
	wsOrig  string
	trusted []*net.IPNet
	auth    *ipLimiter
	reqs    *ipLimiter
	me      *cache.Cache[string, *pb.User]
	// photoReads and photoWork bound the photo uploads handled at once.
	photoReads chan struct{}
	photoWork  chan struct{}
	connSeq    atomic.Uint64
	csp        string
}

func NewHandler(logger *zap.Logger, cfg *Config, client *drpc.Client, r *render.Renderer, engine *actor.Engine, gw *gateway.Deps) *Handler {
	u, _ := url.Parse(cfg.PublicURL)
	h := &Handler{
		logger: logger.Named("web"),
		cfg:    cfg,
		client: client,
		render: r,
		engine: engine,
		gw:     gw,
		secure: u.Scheme == "https",
		origin: u.Scheme + "://" + u.Host,
		auth:   newIPLimiter(rate.Limit(cfg.RateLimit.AuthPerMinute/60), cfg.RateLimit.AuthBurst),
		reqs:   newIPLimiter(rate.Limit(cfg.RateLimit.RequestsPerSecond), cfg.RateLimit.RequestBurst),
		me:     cache.New[string, *pb.User](100_000, meCacheTTL),

		photoReads: make(chan struct{}, photoReaders),
		photoWork:  make(chan struct{}, photoDecoders),
	}
	wsScheme := "ws"
	if h.secure {
		wsScheme = "wss"
	}
	h.wsOrig = wsScheme + "://" + u.Host
	for _, cidr := range cfg.TrustedProxies {
		_, n, _ := net.ParseCIDR(cidr)
		h.trusted = append(h.trusted, n)
	}
	h.csp = strings.Join([]string{
		"default-src 'none'",
		"script-src 'self'",
		"style-src 'self'",
		"img-src 'self' data:",
		"connect-src 'self' " + h.wsOrig,
		"form-action 'self'",
		"frame-ancestors 'none'",
		"base-uri 'none'",
		"manifest-src 'self'",
	}, "; ")
	return h
}

// Routes returns the gateway's whole HTTP surface.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(views.Files, "static")
	mux.Handle("GET "+h.render.StaticPrefix, h.staticFiles(http.StripPrefix(h.render.StaticPrefix, http.FileServerFS(static))))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	mux.HandleFunc("GET /login", h.loginPage)
	mux.Handle("POST /login", h.limited(h.csrf(http.HandlerFunc(h.login))))
	mux.HandleFunc("GET /login/mfa", h.mfaPage)
	mux.Handle("POST /login/mfa", h.limited(h.csrf(http.HandlerFunc(h.mfa))))
	mux.HandleFunc("GET /signup", h.signupPage)
	mux.Handle("POST /signup", h.limited(h.csrf(http.HandlerFunc(h.signup))))
	mux.Handle("POST /logout", h.csrf(http.HandlerFunc(h.logout)))
	mux.Handle("GET /join/{token}", h.limited(http.HandlerFunc(h.joinPage)))
	mux.Handle("POST /join/{token}", h.limited(h.csrf(http.HandlerFunc(h.join))))

	mux.HandleFunc("GET /{$}", h.home)
	mux.HandleFunc("GET /teams/new", h.newTeamPage)
	mux.Handle("POST /teams", h.csrf(http.HandlerFunc(h.createTeam)))
	mux.HandleFunc("GET /t/{team}", h.teamHome)
	mux.HandleFunc("GET /t/{team}/ch/{channel}", h.channelPage)
	mux.HandleFunc("GET /t/{team}/ch/{channel}/messages", h.olderMessages)
	mux.HandleFunc("GET /t/{team}/ch/{channel}/members", h.channelMembers)
	mux.HandleFunc("GET /t/{team}/ch/{channel}/add", h.addMemberModal)
	mux.HandleFunc("GET /t/{team}/ch/{channel}/add/search", h.addMemberSearch)
	mux.Handle("POST /t/{team}/ch/{channel}/members", h.csrf(http.HandlerFunc(h.addMember)))
	mux.Handle("POST /t/{team}/ch/{channel}/members/{user}/remove", h.csrf(http.HandlerFunc(h.removeMember)))
	mux.Handle("POST /t/{team}/ch/{channel}/join", h.csrf(http.HandlerFunc(h.joinChannel)))
	mux.Handle("POST /t/{team}/ch/{channel}/leave", h.csrf(http.HandlerFunc(h.leaveChannel)))
	mux.HandleFunc("GET /t/{team}/channels/new", h.newChannelModal)
	mux.Handle("POST /t/{team}/channels", h.csrf(http.HandlerFunc(h.createChannel)))
	mux.HandleFunc("GET /t/{team}/channels/browse", h.browseModal)
	mux.HandleFunc("GET /t/{team}/people", h.peopleModal)
	mux.HandleFunc("GET /t/{team}/people/search", h.peopleSearch)
	mux.Handle("POST /t/{team}/dm", h.csrf(http.HandlerFunc(h.openDirect)))
	mux.HandleFunc("GET /t/{team}/invite", h.inviteModal)
	mux.Handle("POST /t/{team}/invites", h.csrf(http.HandlerFunc(h.createInvite)))
	mux.Handle("POST /t/{team}/invites/{invite}/revoke", h.csrf(http.HandlerFunc(h.revokeInvite)))
	mux.HandleFunc("GET /t/{team}/members", h.teamMembers)
	mux.HandleFunc("GET /t/{team}/settings", h.teamSettings)
	mux.Handle("POST /t/{team}/photo", h.csrfMultipart(http.HandlerFunc(h.setTeamPhoto)))
	mux.Handle("POST /t/{team}/photo/remove", h.csrf(http.HandlerFunc(h.setTeamPhoto)))
	mux.Handle("POST /t/{team}/members/{user}/remove", h.csrf(http.HandlerFunc(h.removeTeamMember)))
	mux.Handle("POST /t/{team}/members/{user}/role", h.csrf(http.HandlerFunc(h.teamMemberRole)))

	mux.HandleFunc("GET /m/{channel}/{message}", h.messageFragment)
	mux.HandleFunc("GET /m/{channel}/{message}/thread", h.threadPanel)
	mux.HandleFunc("GET /m/{channel}/people", h.mentionSuggestions)
	mux.HandleFunc("GET /m/{channel}/{message}/react", h.reactionPicker)
	mux.Handle("POST /m/{channel}/{message}/react", h.csrf(http.HandlerFunc(h.react)))
	mux.HandleFunc("GET /m/{channel}/{message}/edit", h.editMessageForm)
	mux.Handle("POST /m/{channel}/{message}", h.csrf(http.HandlerFunc(h.editMessage)))
	mux.Handle("POST /m/{channel}/{message}/delete", h.csrf(http.HandlerFunc(h.deleteMessage)))

	mux.Handle("POST /upload", h.csrfMultipart(http.HandlerFunc(h.upload)))
	mux.HandleFunc("GET /files/{id}", h.download)
	mux.HandleFunc("GET /files/{id}/inline", h.download)
	mux.HandleFunc("GET /photos/u/{id}/{version}", h.servePhoto)
	mux.HandleFunc("GET /photos/t/{id}/{version}", h.servePhoto)

	mux.HandleFunc("GET /settings", h.settingsPage)
	mux.Handle("POST /settings/profile", h.csrf(http.HandlerFunc(h.updateProfile)))
	mux.Handle("POST /settings/photo", h.csrfMultipart(http.HandlerFunc(h.setPhoto)))
	mux.Handle("POST /settings/photo/remove", h.csrf(http.HandlerFunc(h.setPhoto)))
	mux.Handle("POST /settings/password", h.csrf(http.HandlerFunc(h.changePassword)))
	mux.Handle("POST /settings/totp/begin", h.csrf(http.HandlerFunc(h.beginTOTP)))
	mux.Handle("POST /settings/totp/confirm", h.csrf(http.HandlerFunc(h.confirmTOTP)))
	mux.Handle("POST /settings/totp/disable", h.csrf(http.HandlerFunc(h.disableTOTP)))

	mux.HandleFunc("GET /ws", h.websocket)

	return h.recover(h.headers(h.throttle(mux)))
}

func (h *Handler) staticFiles(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Files only, never a listing of them.
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		next.ServeHTTP(w, r)
	})
}

// clientIP is the peer's address, or with a trusted proxy in front, the
// right-most address in X-Forwarded-For that is not one of the proxies.
func (h *Handler) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if len(h.trusted) == 0 || !h.isTrusted(net.ParseIP(host)) {
		return host
	}
	hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		ip := net.ParseIP(strings.TrimSpace(hops[i]))
		if ip == nil {
			break
		}
		if !h.isTrusted(ip) {
			return ip.String()
		}
	}
	return host
}

func (h *Handler) isTrusted(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, n := range h.trusted {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func (h *Handler) call(r *http.Request) drpc.Call {
	ua := r.UserAgent()
	if len(ua) > 256 {
		ua = ua[:256]
	}
	return drpc.Call{Session: h.sessionToken(r), ClientIP: h.clientIP(r), UserAgent: ua}
}

func (h *Handler) ctx(r *http.Request) (context.Context, context.CancelFunc) {
	return h.client.Context(r.Context(), h.call(r))
}

// currentUser returns the signed-in user, or nil.
// currentUser returns the signed-in user, or nil. Only a failure to find
// out, such as the server restarting, is an error.
func (h *Handler) currentUser(r *http.Request) (*pb.User, error) {
	token := h.sessionToken(r)
	if token == "" {
		return nil, nil
	}
	if u, ok := h.me.Get(token); ok {
		return u, nil
	}
	ctx, cancel := h.ctx(r)
	defer cancel()
	res, err := h.client.Auth.Me(ctx, &pb.MeRequest{})
	if err != nil {
		if drpc.Err(err).Code == ierrors.CodeUnauthenticated {
			return nil, nil
		}
		return nil, err
	}
	h.me.Set(token, res.User)
	return res.User, nil
}

func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// safeNext keeps a post-login redirect on this site.
func safeNext(next string) string {
	if next == "" || len(next) > 2048 || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return "/"
	}
	// Browsers drop tabs and newlines from URLs and read a backslash as a
	// slash, either of which can turn a path into another host.
	for i := range len(next) {
		if c := next[i]; c < 0x20 || c == 0x7f || c == '\\' {
			return "/"
		}
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Opaque != "" {
		return "/"
	}
	return next
}

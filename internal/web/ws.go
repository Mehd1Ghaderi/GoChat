package web

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/anthdm/hollywood/actor"
	"github.com/coder/websocket"
	"go.uber.org/zap"

	"github.com/ssenerg/gochat/internal/actors"
	"github.com/ssenerg/gochat/internal/web/gateway"
)

// websocket upgrades a signed-in page's socket and hands it to a session
// actor; this goroutine stays as the socket's reader.
func (h *Handler) websocket(w http.ResponseWriter, r *http.Request) {
	token := h.sessionToken(r)
	if token == "" {
		http.Error(w, "sign in first", http.StatusUnauthorized)
		return
	}
	if r.Header.Get("Origin") != h.origin {
		http.Error(w, "cross-origin socket refused", http.StatusForbidden)
		return
	}
	u, _ := url.Parse(h.origin)
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{u.Host}})
	if err != nil {
		h.logger.Debug("websocket upgrade failed", zap.Error(err))
		return
	}
	ws.SetReadLimit(h.gw.Cfg.ReadLimit)
	id := h.connSeq.Add(1)
	call := h.call(r)
	pid := h.engine.Spawn(gateway.NewSession(h.gw, id, call, ws), "session", actor.WithID(strconv.FormatUint(id, 36)),
		actor.WithMiddleware(actors.Recover(h.logger)))
	defer h.engine.Poison(pid)
	for {
		typ, data, err := ws.Read(context.Background())
		if err != nil {
			return
		}
		if typ == websocket.MessageText {
			h.engine.Send(pid, gateway.Frame{Data: data})
		}
	}
}

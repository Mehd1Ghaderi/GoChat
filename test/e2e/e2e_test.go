// Package e2e drives a running stack the way browsers do: forms, htmx
// requests and websockets. It runs only when GOCHAT_E2E_URL is set:
//
//	GOCHAT_E2E_URL=http://localhost:8080 GOCHAT_E2E_ADMIN=admin \
//	GOCHAT_E2E_ADMIN_PASSWORD=... GOCHAT_E2E_TEAM=acme go test ./test/e2e/
package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"image"
	"image/color"
	_ "image/jpeg" // decodes the stored photos
	"image/png"

	"github.com/coder/websocket"
	"github.com/pquerna/otp/totp"

	"github.com/ssenerg/gochat/internal/core/secure"
)

var (
	baseURL   = os.Getenv("GOCHAT_E2E_URL")
	adminName = os.Getenv("GOCHAT_E2E_ADMIN")
	adminPass = os.Getenv("GOCHAT_E2E_ADMIN_PASSWORD")
	teamName  = os.Getenv("GOCHAT_E2E_TEAM")
)

type client struct {
	t    *testing.T
	http *http.Client
	name string
}

func newClient(t *testing.T, name string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, name: name, http: &http.Client{
		Jar:     jar,
		Timeout: 20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

func (c *client) cookie(name string) string {
	u, _ := url.Parse(baseURL)
	for _, ck := range c.http.Jar.Cookies(u) {
		if ck.Name == name {
			return ck.Value
		}
	}
	return ""
}

func (c *client) do(method, path string, body io.Reader, contentType string, htmx bool) (*http.Response, string) {
	c.t.Helper()
	req, err := http.NewRequest(method, baseURL+path, body)
	if err != nil {
		c.t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	if method == http.MethodPost {
		req.Header.Set("Origin", baseURL)
		if tok := c.cookie("gochat_csrf"); tok != "" && htmx {
			req.Header.Set("X-CSRF-Token", tok)
		}
	}
	var buf []byte
	if body != nil {
		buf, _ = io.ReadAll(body)
	}
	// The sign-in endpoints are rate limited per address, which every client
	// here shares: wait for the limiter rather than fail.
	for attempt := 0; ; attempt++ {
		if buf != nil {
			req.Body = io.NopCloser(bytes.NewReader(buf))
		}
		res, err := c.http.Do(req)
		if err != nil {
			c.t.Fatalf("%s %s: %v", method, path, err)
		}
		b, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		wait, _ := strconv.Atoi(res.Header.Get("Retry-After"))
		if res.StatusCode != http.StatusTooManyRequests || attempt == 5 || wait > 15 {
			return res, string(b)
		}
		time.Sleep(time.Duration(wait) * time.Second)
	}
}

func (c *client) get(path string) (*http.Response, string) {
	return c.do(http.MethodGet, path, nil, "", false)
}

func (c *client) form(path string, values url.Values) (*http.Response, string) {
	if c.cookie("gochat_csrf") == "" {
		c.get("/login")
	}
	values.Set("_csrf", c.cookie("gochat_csrf"))
	return c.do(http.MethodPost, path, strings.NewReader(values.Encode()), "application/x-www-form-urlencoded", false)
}

func (c *client) hx(path string, values url.Values) (*http.Response, string) {
	return c.do(http.MethodPost, path, strings.NewReader(values.Encode()), "application/x-www-form-urlencoded", true)
}

func (c *client) login(login, password string) {
	c.t.Helper()
	res, body := c.form("/login", url.Values{"login": {login}, "password": {password}})
	if res.StatusCode != http.StatusSeeOther || c.cookie("gochat_session") == "" {
		c.t.Fatalf("%s: login failed: %d %s", c.name, res.StatusCode, body)
	}
}

func random(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type socket struct {
	t    *testing.T
	conn *websocket.Conn
	in   chan string
}

func (c *client) socket(origin string) (*socket, *http.Response, error) {
	h := http.Header{}
	h.Set("Origin", origin)
	u, _ := url.Parse(baseURL)
	h.Set("Cookie", "gochat_session="+c.cookie("gochat_session"))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, res, err := websocket.Dial(ctx, "ws://"+u.Host+"/ws", &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		return nil, res, err
	}
	s := &socket{t: c.t, conn: conn, in: make(chan string, 256)}
	go func() {
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				close(s.in)
				return
			}
			s.in <- string(data)
		}
	}()
	return s, res, nil
}

func (s *socket) send(v any) {
	s.t.Helper()
	b, _ := json.Marshal(v)
	if err := s.conn.Write(context.Background(), websocket.MessageText, b); err != nil {
		s.t.Fatal(err)
	}
}

// expect waits for a frame containing every one of parts.
func (s *socket) expect(within time.Duration, parts ...string) string {
	s.t.Helper()
	deadline := time.After(within)
	for {
		select {
		case f, ok := <-s.in:
			if !ok {
				s.t.Fatalf("socket closed waiting for %q", parts)
			}
			all := true
			for _, p := range parts {
				if !strings.Contains(f, p) {
					all = false
				}
			}
			if all {
				return f
			}
		case <-deadline:
			s.t.Fatalf("no frame with %q", parts)
		}
	}
}

// collect returns the frames that arrive within a while.
func (s *socket) collect(within time.Duration) []string {
	var out []string
	deadline := time.After(within)
	for {
		select {
		case f, ok := <-s.in:
			if !ok {
				return out
			}
			out = append(out, f)
		case <-deadline:
			return out
		}
	}
}

func (s *socket) refuse(within time.Duration, part string) {
	s.t.Helper()
	deadline := time.After(within)
	for {
		select {
		case f, ok := <-s.in:
			if !ok {
				return
			}
			if strings.Contains(f, part) {
				s.t.Fatalf("got a frame with %q: %s", part, f)
			}
		case <-deadline:
			return
		}
	}
}

var (
	inviteRe    = regexp.MustCompile(`/join/([A-Za-z0-9_-]{43})`)
	channelRe   = regexp.MustCompile(`/t/[a-z0-9-]+/ch/([0-9a-f-]{36})`)
	attRe       = regexp.MustCompile(`name="attachment" value="([0-9a-f-]{36})"`)
	secretRe    = regexp.MustCompile(`<code>([A-Z2-7]{16,})</code>`)
	codeRe      = regexp.MustCompile(`<code>([a-z2-7]{5}-[a-z2-7]{5})</code>`)
	teamIDRe    = regexp.MustCompile(`data-team="([0-9a-f-]{36})"`)
	msgIDRe     = regexp.MustCompile(`id="msg-([0-9a-f-]{36})"`)
	newestRe    = regexp.MustCompile(`data-newest="([0-9a-f-]{36})"`)
	offTopicRe  = regexp.MustCompile(`data-channel="([0-9a-f-]{36})"(?s:[^<]*>.{0,600}?)<span class="label">Off-Topic</span>`)
	photoRe     = regexp.MustCompile(`/photos/([ut])/([0-9a-f-]{36})/([0-9a-f]{16})`)
	staticCSSRe = regexp.MustCompile(`/static/[0-9a-f]+/app\.css`)
	pickRe      = regexp.MustCompile(`data-pick="([0-9a-f-]{36})"`)
)

// bootstrap sets a fresh server up with the admin and the team the tests use.
func bootstrap(t *testing.T, admin *client) {
	t.Helper()
	admin.get("/signup")
	res, body := admin.form("/signup", url.Values{
		"username": {adminName}, "display_name": {"Ada Admin"}, "email": {adminName + "@example.test"},
		"password": {adminPass}, "confirm": {adminPass},
	})
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("bootstrap sign-up: %d %s", res.StatusCode, body)
	}
	res, body = admin.form("/teams", url.Values{"name": {teamName}, "display_name": {"Acme"}})
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("bootstrap team: %d %s", res.StatusCode, body)
	}
}

func TestEndToEnd(t *testing.T) {
	if baseURL == "" {
		t.Skip("GOCHAT_E2E_URL is not set")
	}
	admin := newClient(t, "admin")
	if os.Getenv("GOCHAT_E2E_BOOTSTRAP") != "" {
		bootstrap(t, admin)
	}
	admin.login(adminName, adminPass)

	// One-time invite.
	_, body := admin.hx("/t/"+teamName+"/invites", url.Values{})
	m := inviteRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no invite link in %s", body)
	}
	invite := m[1]

	bob := newClient(t, "bob")
	bobName, bobPass := "bob-"+random(4), "pw-"+random(8)
	if _, body := bob.get("/join/" + invite); !strings.Contains(body, "Create an account") {
		t.Fatalf("join page: %s", body)
	}
	bob.get("/signup?invite=" + invite)
	res, body := bob.form("/signup", url.Values{
		"username": {bobName}, "display_name": {"Bob <b>Tester</b>"}, "email": {bobName + "@example.test"},
		"password": {bobPass}, "confirm": {bobPass}, "invite": {invite},
	})
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/t/"+teamName {
		t.Fatalf("signup with invite: %d %s %s", res.StatusCode, res.Header.Get("Location"), body)
	}

	t.Run("invite works once", func(t *testing.T) {
		eve := newClient(t, "eve")
		eve.get("/signup?invite=" + invite)
		res, body := eve.form("/signup", url.Values{
			"username": {"eve-" + random(4)}, "email": {"eve-" + random(4) + "@example.test"},
			"password": {"pw-" + random(8)}, "confirm": {"x"}, "invite": {invite},
		})
		if res.StatusCode == http.StatusSeeOther {
			t.Fatal("a used invite admitted a second account")
		}
		if !strings.Contains(body, "Invite required") {
			t.Fatalf("expected the invite to be spent: %s", body)
		}
	})

	res, _ = bob.get("/t/" + teamName)
	m = channelRe.FindStringSubmatch(res.Header.Get("Location"))
	if m == nil {
		t.Fatalf("team home did not redirect to a channel: %s", res.Header.Get("Location"))
	}
	town := m[1]
	_, page := admin.get("/t/" + teamName + "/ch/" + town)
	teamID := teamIDRe.FindStringSubmatch(page)[1]

	adminWS, _, err := admin.socket(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	bobWS, _, err := bob.socket(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	view := func(s *socket, ch string) {
		s.send(map[string]string{"op": "view", "team_id": teamID, "team_name": teamName, "channel_id": ch})
	}
	view(adminWS, town)
	view(bobWS, town)
	time.Sleep(300 * time.Millisecond)

	t.Run("messages arrive in real time and escaped", func(t *testing.T) {
		bobWS.send(map[string]string{"op": "post", "channel_id": town, "content": "hello <script>alert(1)</script> from bob"})
		f := adminWS.expect(5*time.Second, "from bob", `hx-swap-oob="afterbegin"`)
		if strings.Contains(f, "<script>") || strings.Contains(f, "<b>Tester") {
			t.Fatalf("unescaped content reached the page: %s", f)
		}
		bobWS.expect(5*time.Second, "from bob")
	})

	t.Run("threads keep replies apart", func(t *testing.T) {
		bobWS.send(map[string]string{"op": "post", "channel_id": town, "content": "root for a thread"})
		f := adminWS.expect(5*time.Second, "root for a thread", `hx-swap-oob="afterbegin"`)
		root := msgIDRe.FindStringSubmatch(f)[1]
		bobWS.send(map[string]string{"op": "thread", "root_id": root})
		adminWS.send(map[string]string{"op": "post", "channel_id": town, "root_id": root, "content": "first reply"})
		f = bobWS.expect(5*time.Second, "first reply", `id="replies-`+root+`"`)
		reply := msgIDRe.FindStringSubmatch(f)[1]
		adminWS.expect(5*time.Second, `id="msg-`+root+`"`, "1 reply")
		if _, page := admin.get("/t/" + teamName + "/ch/" + town); strings.Contains(page, "first reply") || !strings.Contains(page, "1 reply") {
			t.Fatal("the reply is in the channel's stream, or the root does not count it")
		}
		if _, panel := admin.do(http.MethodGet, "/m/"+town+"/"+root+"/thread", nil, "", true); !strings.Contains(panel, "root for a thread") ||
			!strings.Contains(panel, "first reply") || !strings.Contains(panel, `id="reply-composer"`) {
			t.Fatalf("thread panel: %s", panel)
		}
		if res, _ := bob.hx("/m/"+town+"/"+reply+"/delete?root="+root, url.Values{}); res.StatusCode != http.StatusForbidden {
			t.Fatalf("bob deleted admin's reply: %d", res.StatusCode)
		}
		if res, _ := admin.hx("/m/"+town+"/"+reply+"/delete?root="+root, url.Values{}); res.StatusCode != http.StatusOK {
			t.Fatalf("deleting a reply: %d", res.StatusCode)
		}
		bobWS.expect(5*time.Second, `id="msg-`+reply+`"`, `hx-swap-oob="delete"`)
		if f := adminWS.expect(5*time.Second, `id="msg-`+root+`"`); strings.Contains(f, "thread-link") {
			t.Fatalf("the root still counts a deleted reply: %s", f)
		}
		adminWS.send(map[string]string{"op": "post", "channel_id": town, "root_id": root, "content": "second reply"})
		bobWS.expect(5*time.Second, "second reply")
		if res, _ := bob.hx("/m/"+town+"/"+root+"/delete", url.Values{}); res.StatusCode != http.StatusOK {
			t.Fatalf("deleting a root: %d", res.StatusCode)
		}
		bobWS.expect(5*time.Second, `id="thread"`, `hx-swap-oob="outerHTML"`)
		if res, _ := admin.do(http.MethodGet, "/m/"+town+"/"+root+"/thread", nil, "", true); res.StatusCode != http.StatusNotFound {
			t.Fatalf("a deleted root's thread: %d", res.StatusCode)
		}
		bobWS.send(map[string]string{"op": "post", "channel_id": town, "root_id": root, "content": "late reply"})
		bobWS.expect(5*time.Second, "message not found")
	})

	t.Run("mentions highlight, count and complete", func(t *testing.T) {
		_, page := bob.get("/t/" + teamName + "/ch/" + town)
		m := offTopicRe.FindStringSubmatch(page)
		if m == nil {
			t.Fatalf("no Off-Topic in bob's sidebar: %s", page)
		}
		offTopic := m[1]
		adminWS.send(map[string]string{"op": "post", "channel_id": offTopic, "content": "please look, @" + bobName})
		bobWS.expect(5*time.Second, `id="unread-`+offTopic+`" class="mentions"`, ">1<")
		if _, page := bob.get("/t/" + teamName + "/ch/" + town); !strings.Contains(page, `id="unread-`+offTopic+`" class="mentions"`) {
			t.Fatal("the mention is not counted on reload")
		}
		adminWS.send(map[string]string{"op": "post", "channel_id": town, "content": "hey @" + bobName + " and `@nobody`"})
		f := bobWS.expect(5*time.Second, `class="msg mentioned"`)
		if !strings.Contains(f, `<span class="mention">@`+bobName+`</span>`) || strings.Contains(f, `">@nobody`) {
			t.Fatalf("mention markup: %s", f)
		}
		if _, list := bob.do(http.MethodGet, "/m/"+town+"/people?q=adm", nil, "", true); !strings.Contains(list, `data-insert="@admin "`) {
			t.Fatalf("suggestions for adm: %s", list)
		}
		if _, list := bob.do(http.MethodGet, "/m/"+town+"/people?q=cha", nil, "", true); !strings.Contains(list, `data-insert="@channel "`) {
			t.Fatalf("suggestions for cha: %s", list)
		}
		if res, _ := bob.do(http.MethodGet, "/m/"+town+"/people?q=%3Cx", nil, "", true); res.StatusCode != http.StatusNotFound {
			t.Fatalf("a bad prefix was accepted: %d", res.StatusCode)
		}
		// Reading the channel clears its count: the page tells the socket
		// the newest message it shows.
		defer view(bobWS, town)
		_, page = bob.get("/t/" + teamName + "/ch/" + offTopic)
		newest := newestRe.FindStringSubmatch(page)
		if newest == nil {
			t.Fatalf("no newest message on the page: %s", page)
		}
		bobWS.send(map[string]string{"op": "view", "team_id": teamID, "team_name": teamName, "channel_id": offTopic, "last_id": newest[1]})
		deadline := time.Now().Add(6 * time.Second)
		for {
			_, page := bob.get("/t/" + teamName + "/ch/" + town)
			if !strings.Contains(page, `id="unread-`+offTopic+`" class="mentions"`) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("reading the channel did not clear its mentions")
			}
			time.Sleep(300 * time.Millisecond)
		}
	})

	t.Run("reactions toggle and arrive live", func(t *testing.T) {
		bobWS.send(map[string]string{"op": "post", "channel_id": town, "content": "react to me"})
		f := adminWS.expect(5*time.Second, "react to me", `hx-swap-oob="afterbegin"`)
		msg := msgIDRe.FindStringSubmatch(f)[1]
		bar := `id="reactions-` + msg + `"`
		react := func(c *client, emoji string, remove bool) (*http.Response, string) {
			v := url.Values{"emoji": {emoji}}
			if remove {
				v.Set("remove", "1")
			}
			return c.hx("/m/"+town+"/"+msg+"/react", v)
		}
		if res, body := react(admin, "tada", false); res.StatusCode != http.StatusOK || !strings.Contains(body, bar) || !strings.Contains(body, `aria-pressed="true"`) {
			t.Fatalf("react: %d %s", res.StatusCode, body)
		}
		bobWS.expect(5*time.Second, bar, "🎉", `<span class="n">1</span>`, `aria-pressed="false"`)
		react(bob, "tada", false)
		adminWS.expect(5*time.Second, bar, `<span class="n">2</span>`, `aria-pressed="true"`)
		react(admin, "tada", true)
		bobWS.expect(5*time.Second, bar, `<span class="n">1</span>`, `aria-pressed="true"`)
		if _, page := admin.get("/t/" + teamName + "/ch/" + town); !strings.Contains(page, bar) || !strings.Contains(page, "🎉") {
			t.Fatal("the reaction is gone after a reload")
		}
		if res, _ := react(bob, "<script>", false); res.StatusCode == http.StatusOK {
			t.Fatal("an unknown emoji was accepted")
		}
		if _, picker := bob.do(http.MethodGet, "/m/"+town+"/"+msg+"/react", nil, "", true); !strings.Contains(picker, `class="emoji-grid"`) {
			t.Fatalf("picker: %s", picker)
		}
	})

	t.Run("notifications follow mentions, replies and mutes", func(t *testing.T) {
		_, page := bob.get("/t/" + teamName + "/ch/" + town)
		offTopic := offTopicRe.FindStringSubmatch(page)[1]
		adminWS.send(map[string]string{"op": "post", "channel_id": offTopic, "content": "a word, @" + bobName})
		f := bobWS.expect(5*time.Second, `id="notify"`, `data-url="/c/`+offTopic+`"`)
		if !strings.Contains(f, "in Off-Topic") || !strings.Contains(f, "a word, @"+bobName) {
			t.Fatalf("notice: %s", f)
		}

		bobWS.send(map[string]string{"op": "post", "channel_id": town, "content": "thread for notices"})
		root := msgIDRe.FindStringSubmatch(adminWS.expect(5*time.Second, "thread for notices", `hx-swap-oob="afterbegin"`))[1]
		adminWS.send(map[string]string{"op": "post", "channel_id": town, "root_id": root, "content": "a reply for bob"})
		if f := bobWS.expect(5*time.Second, `id="notify"`, "replied in a thread"); !strings.Contains(f, "?thread="+root) {
			t.Fatalf("reply notice: %s", f)
		}

		if res, body := bob.hx("/t/"+teamName+"/ch/"+offTopic+"/mute", url.Values{"muted": {"1"}}); res.StatusCode != http.StatusOK || !strings.Contains(body, `aria-pressed="true"`) {
			t.Fatalf("mute: %d %s", res.StatusCode, body)
		}
		bobWS.expect(5*time.Second, `id="channel-lists"`, "sb-link muted")
		adminWS.send(map[string]string{"op": "post", "channel_id": offTopic, "content": "again, @" + bobName})
		frames := strings.Join(bobWS.collect(2*time.Second), "\n")
		if strings.Contains(frames, `id="notify"`) || !strings.Contains(frames, `id="unread-`+offTopic+`" class="mentions"`) {
			t.Fatalf("a muted channel notified, or did not count the mention: %s", frames)
		}
		bob.hx("/t/"+teamName+"/ch/"+offTopic+"/mute", url.Values{"muted": {"0"}})

		res, _ := bob.get("/c/" + offTopic + "?thread=" + root)
		if loc := res.Header.Get("Location"); res.StatusCode != http.StatusSeeOther || loc != "/t/"+teamName+"/ch/"+offTopic+"?thread="+root {
			t.Fatalf("channel link: %d %q", res.StatusCode, loc)
		}
	})

	t.Run("typing reaches others only", func(t *testing.T) {
		adminWS.send(map[string]string{"op": "typing", "channel_id": town})
		bobWS.expect(5*time.Second, "is typing")
		adminWS.refuse(time.Second, "is typing")
	})

	t.Run("attachments upload, post and download", func(t *testing.T) {
		png := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 64))
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, _ := mw.CreateFormFile("file", "../../pic<1>.png")
		_, _ = fw.Write(png)
		_ = mw.Close()
		res, body := bob.do(http.MethodPost, "/upload?channel="+town, &buf, mw.FormDataContentType(), true)
		m := attRe.FindStringSubmatch(body)
		if res.StatusCode != http.StatusOK || m == nil {
			t.Fatalf("upload: %d %s", res.StatusCode, body)
		}
		att := m[1]
		bobWS.send(map[string]any{"op": "post", "channel_id": town, "content": "", "attachment": att})
		adminWS.expect(5*time.Second, "/files/"+att, "pic")
		res, got := admin.get("/files/" + att)
		if res.StatusCode != http.StatusOK || got != string(png) {
			t.Fatalf("download: %d", res.StatusCode)
		}
		if res.Header.Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(res.Header.Get("Content-Disposition"), "attachment") {
			t.Fatalf("download headers: %v", res.Header)
		}
		if res, _ := admin.get("/files/" + att + "/inline"); res.Header.Get("Content-Type") != "image/png" {
			t.Fatalf("inline image type: %s", res.Header.Get("Content-Type"))
		}
		// An attachment is posted once.
		bobWS.send(map[string]any{"op": "post", "channel_id": town, "content": "again", "attachment": att})
		bobWS.expect(5*time.Second, "attachment is not available")
	})

	t.Run("private channels stay private", func(t *testing.T) {
		name := "secret-" + random(3)
		res, body := admin.hx("/t/"+teamName+"/channels", url.Values{"name": {name}, "display_name": {"Secret"}, "type": {"private"}})
		m := channelRe.FindStringSubmatch(res.Header.Get("HX-Location"))
		if m == nil {
			t.Fatalf("create private channel: %d %s", res.StatusCode, body)
		}
		secret := m[1]
		if res, _ := bob.get("/t/" + teamName + "/ch/" + secret); res.StatusCode != http.StatusNotFound {
			t.Fatalf("bob read a private channel: %d", res.StatusCode)
		}
		if res, _ := bob.get("/t/" + teamName + "/ch/" + secret + "/messages?before=" + town); res.StatusCode != http.StatusNotFound {
			t.Fatalf("bob listed a private channel: %d", res.StatusCode)
		}
		bobWS.send(map[string]string{"op": "post", "channel_id": secret, "content": "sneaky"})
		bobWS.expect(5*time.Second, "channel not found")
		view(adminWS, secret)
		adminWS.send(map[string]string{"op": "post", "channel_id": secret, "content": "for admins only"})
		adminWS.expect(5*time.Second, "for admins only")
		bobWS.refuse(time.Second, "for admins only")
		view(adminWS, town)
	})

	t.Run("search finds what each person may read", func(t *testing.T) {
		word := "zq" + random(4)
		name := "hidden-" + random(3)
		res, _ := admin.hx("/t/"+teamName+"/channels", url.Values{"name": {name}, "display_name": {"Hidden"}, "type": {"private"}})
		hidden := channelRe.FindStringSubmatch(res.Header.Get("HX-Location"))[1]
		view(adminWS, hidden)
		adminWS.send(map[string]string{"op": "post", "channel_id": hidden, "content": "private " + word + " plans"})
		adminWS.expect(5*time.Second, "private "+word)
		view(adminWS, town)
		bobWS.send(map[string]string{"op": "post", "channel_id": town, "content": "the public " + word + " release notes <b>bold</b>"})
		pub := msgIDRe.FindStringSubmatch(adminWS.expect(5*time.Second, "public "+word))[1]

		search := func(c *client, q string) string {
			t.Helper()
			for range 10 {
				_, body := c.do(http.MethodGet, "/t/"+teamName+"/search?q="+url.QueryEscape(q), nil, "", true)
				// The results, without the query echoed in the search box.
				_, body, _ = strings.Cut(body, `id="search-results"`)
				if !strings.Contains(body, "searching too fast") {
					return body
				}
				time.Sleep(time.Second)
			}
			t.Fatalf("searching %q stayed too fast", q)
			return ""
		}
		// Messages are filed for search a moment after they are posted.
		var body string
		for deadline := time.Now().Add(8 * time.Second); ; time.Sleep(400 * time.Millisecond) {
			if body = search(admin, word); strings.Contains(body, "plans") && strings.Contains(body, "release") {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("search did not find both messages: %s", body)
			}
		}
		if !strings.Contains(body, "<mark>"+word+"</mark>") || strings.Contains(body, "<b>bold") || !strings.Contains(body, "?msg="+pub) {
			t.Fatalf("results: %s", body)
		}
		if body := search(bob, word); !strings.Contains(body, "release") || strings.Contains(body, "plans") {
			t.Fatalf("bob's results: %s", body)
		}
		for q, want := range map[string]string{
			`"release notes" ` + word:   "release",
			word + " from:" + adminName: "plans",
			word + " in:" + name:        "plans",
			word + " -release":          "plans",
		} {
			body := search(admin, q)
			other := map[string]string{"release": "plans", "plans": "release"}[want]
			if !strings.Contains(body, want) || strings.Contains(body, other) {
				t.Fatalf("%q: %s", q, body)
			}
		}
		if body := search(admin, "the"); !strings.Contains(body, "Type a word") {
			t.Fatalf("a stop word alone: %s", body)
		}
		if _, page := admin.get("/t/" + teamName + "/ch/" + town + "?msg=" + pub); !strings.Contains(page, ` focus" id="msg-`+pub+`" data-focus="1"`) {
			t.Fatalf("the linked message is not focused: %s", page)
		}
		if res, _ := bob.hx("/m/"+town+"/"+pub, url.Values{"content": {"nothing to see"}}); res.StatusCode != http.StatusOK {
			t.Fatalf("edit: %d", res.StatusCode)
		}
		for deadline := time.Now().Add(8 * time.Second); strings.Contains(search(admin, word), "release"); time.Sleep(400 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("an edited message is still found by its old words")
			}
		}
	})

	t.Run("group conversations and the switcher", func(t *testing.T) {
		_, body := admin.hx("/t/"+teamName+"/invites", url.Values{})
		carol := newClient(t, "carol")
		carolName, carolPass := "carol-"+random(4), "pw-"+random(8)
		inv := inviteRe.FindStringSubmatch(body)[1]
		carol.get("/signup?invite=" + inv)
		if res, body := carol.form("/signup", url.Values{
			"username": {carolName}, "display_name": {"Carol"}, "email": {carolName + "@example.test"},
			"password": {carolPass}, "confirm": {carolPass}, "invite": {inv},
		}); res.StatusCode != http.StatusSeeOther {
			t.Fatalf("carol's signup: %d %s", res.StatusCode, body)
		}
		userID := func(q string) string {
			_, list := admin.do(http.MethodGet, "/t/"+teamName+"/people/search?q="+q, nil, "", true)
			m := pickRe.FindStringSubmatch(list)
			if m == nil {
				t.Fatalf("no one found for %q: %s", q, list)
			}
			return m[1]
		}
		bobUser, carolUser := userID(bobName), userID(carolName)
		open := func(people ...string) string {
			t.Helper()
			res, body := admin.hx("/t/"+teamName+"/dm", url.Values{"user_id": people})
			m := channelRe.FindStringSubmatch(res.Header.Get("HX-Location"))
			if m == nil {
				t.Fatalf("open %v: %d %s", people, res.StatusCode, body)
			}
			return m[1]
		}
		group := open(bobUser, carolUser)
		if again := open(carolUser, bobUser); again != group {
			t.Fatalf("the same people opened another group: %s, %s", group, again)
		}
		if direct := open(bobUser); direct == group {
			t.Fatal("one person opened the group")
		}
		bobWS.expect(5*time.Second, `id="channel-lists"`, `class="group-count"`)
		view(adminWS, group)
		adminWS.send(map[string]string{"op": "post", "channel_id": group, "content": "hello group"})
		bobWS.expect(5*time.Second, `id="notify"`, "hello group")
		bobWS.expect(5*time.Second, `id="unread-`+group+`" class="mentions"`)
		view(adminWS, town)
		if _, page := carol.get("/t/" + teamName + "/ch/" + group); !strings.Contains(page, "hello group") || !strings.Contains(page, "Your conversation with") {
			t.Fatalf("carol's view of the group: %s", page)
		}
		if res, _ := bob.hx("/t/"+teamName+"/ch/"+group+"/leave", url.Values{}); res.StatusCode < 400 {
			t.Fatalf("bob left a group conversation: %d", res.StatusCode)
		}
		if res, _ := admin.hx("/t/"+teamName+"/dm", url.Values{"user_id": {bobUser, carolUser, "00000000-0000-4000-8000-000000000000"}}); res.StatusCode != http.StatusNotFound {
			t.Fatalf("a group with a stranger: %d", res.StatusCode)
		}

		if _, list := bob.do(http.MethodGet, "/t/"+teamName+"/switch/results?q=off", nil, "", true); !strings.Contains(list, "Off-Topic") || !strings.Contains(list, `role="option"`) {
			t.Fatalf("switcher for off: %s", list)
		}
		if _, list := bob.do(http.MethodGet, "/t/"+teamName+"/switch/results?q=carol", nil, "", true); !strings.Contains(list, "Carol") {
			t.Fatalf("switcher for carol: %s", list)
		}
		if _, modal := bob.do(http.MethodGet, "/t/"+teamName+"/switch", nil, "", true); !strings.Contains(modal, "data-switch-input") || !strings.Contains(modal, "Town Square") {
			t.Fatalf("switcher: %s", modal)
		}
	})

	t.Run("csrf and origin are enforced", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, baseURL+"/t/"+teamName+"/invites", nil)
		req.Header.Set("HX-Request", "true")
		res, err := admin.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("a request without the CSRF token passed: %d", res.StatusCode)
		}
		req, _ = http.NewRequest(http.MethodPost, baseURL+"/t/"+teamName+"/invites", nil)
		req.Header.Set("Origin", "https://evil.example")
		req.Header.Set("X-CSRF-Token", admin.cookie("gochat_csrf"))
		res, err = admin.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("a cross-origin request passed: %d", res.StatusCode)
		}
		if _, res, err := admin.socket("https://evil.example"); err == nil || res == nil || res.StatusCode != http.StatusForbidden {
			t.Fatalf("a cross-origin socket was accepted")
		}
	})

	t.Run("proxies may not rewrite pages", func(t *testing.T) {
		res, body := admin.get("/settings")
		if cc := res.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") || !strings.Contains(cc, "no-transform") {
			t.Fatalf("page Cache-Control: %q", cc)
		}
		if res, _ := admin.do(http.MethodGet, "/settings", nil, "", true); !strings.Contains(res.Header.Get("Cache-Control"), "no-transform") {
			t.Fatalf("fragment Cache-Control: %q", res.Header.Get("Cache-Control"))
		}
		css := staticCSSRe.FindString(body)
		if css == "" {
			t.Fatalf("no stylesheet in %s", body)
		}
		if res, _ := admin.get(css); res.StatusCode != http.StatusOK || strings.Contains(res.Header.Get("Cache-Control"), "no-transform") {
			t.Fatalf("static asset: %d %q", res.StatusCode, res.Header.Get("Cache-Control"))
		}
	})

	t.Run("members cannot invite", func(t *testing.T) {
		res, _ := bob.hx("/t/"+teamName+"/invites", url.Values{})
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("a member created an invite: %d", res.StatusCode)
		}
	})

	t.Run("invites die with their creator's rights", func(t *testing.T) {
		_, list := admin.do(http.MethodGet, "/t/"+teamName+"/members", nil, "", true)
		m := regexp.MustCompile(`@` + regexp.QuoteMeta(bobName) + `(?s:.*?)/members/([0-9a-f-]{36})/role`).FindStringSubmatch(list)
		if m == nil {
			t.Fatalf("bob is not in the member list: %s", list)
		}
		bobID := m[1]
		admin.hx("/t/"+teamName+"/members/"+bobID+"/role", url.Values{"role": {"admin"}})
		_, body := bob.hx("/t/"+teamName+"/invites", url.Values{})
		link := inviteRe.FindStringSubmatch(body)
		if link == nil {
			t.Fatalf("a team admin could not invite: %s", body)
		}
		if _, page := newClient(t, "carol").get("/join/" + link[1]); !strings.Contains(page, "Create an account") {
			t.Fatalf("a fresh invite is not usable: %s", page)
		}
		admin.hx("/t/"+teamName+"/members/"+bobID+"/role", url.Values{"role": {"member"}})
		if _, page := newClient(t, "carol").get("/join/" + link[1]); !strings.Contains(page, "Invite unavailable") {
			t.Fatalf("an invite outlived its creator's admin role: %s", page)
		}
		_, modal := admin.do(http.MethodGet, "/t/"+teamName+"/invite", nil, "", true)
		entry := "/invites/" + secure.PublicID(secure.Hash(link[1])) + "/revoke"
		if i := strings.Index(modal, entry); i < 0 || !strings.Contains(modal[:i], "Suspended") {
			t.Fatalf("the suspended invite is not shown as such: %s", modal)
		}
	})

	t.Run("a removed member cannot rejoin with their invite", func(t *testing.T) {
		_, body := admin.hx("/t/"+teamName+"/invites", url.Values{})
		link := inviteRe.FindStringSubmatch(body)
		if link == nil {
			t.Fatalf("no invite link in %s", body)
		}
		frank := newClient(t, "frank")
		name, pass := "frank-"+random(4), "pw-"+random(8)
		frank.get("/signup?invite=" + link[1])
		res, body := frank.form("/signup", url.Values{
			"username": {name}, "display_name": {"Frank"}, "email": {name + "@example.test"},
			"password": {pass}, "confirm": {pass}, "invite": {link[1]},
		})
		if res.StatusCode != http.StatusSeeOther {
			t.Fatalf("signup: %d %s", res.StatusCode, body)
		}
		_, list := admin.do(http.MethodGet, "/t/"+teamName+"/members", nil, "", true)
		m := regexp.MustCompile(`@` + regexp.QuoteMeta(name) + `(?s:.*?)/members/([0-9a-f-]{36})/remove`).FindStringSubmatch(list)
		if m == nil {
			t.Fatalf("frank is not in the member list: %s", list)
		}
		admin.hx("/t/"+teamName+"/members/"+m[1]+"/remove", url.Values{})
		frank.form("/join/"+link[1], url.Values{})
		if res, _ := frank.get("/t/" + teamName); res.StatusCode == http.StatusOK {
			t.Fatal("a removed member rejoined with the invite they used")
		}
	})

	t.Run("revoked invites stop working", func(t *testing.T) {
		_, body := admin.hx("/t/"+teamName+"/invites", url.Values{})
		link := inviteRe.FindStringSubmatch(body)
		if link == nil {
			t.Fatalf("no invite link in %s", body)
		}
		revoke := "/t/" + teamName + "/invites/" + secure.PublicID(secure.Hash(link[1])) + "/revoke"
		if !strings.Contains(body, revoke) {
			t.Fatalf("the new invite is not listed: %s", body)
		}
		if i := strings.Index(body, `id="invite-list"`); i < 0 || strings.Contains(body[i:], link[1]) {
			t.Fatalf("the open invites are missing or show tokens: %s", body)
		}
		if res, _ := bob.do(http.MethodGet, "/t/"+teamName+"/invite", nil, "", true); res.StatusCode != http.StatusForbidden {
			t.Fatalf("a member listed the invites: %d", res.StatusCode)
		}
		if res, _ := bob.hx(revoke, url.Values{}); res.StatusCode != http.StatusForbidden {
			t.Fatalf("a member revoked an invite: %d", res.StatusCode)
		}
		res, list := admin.hx(revoke, url.Values{})
		if res.StatusCode != http.StatusOK || !strings.Contains(list, `id="invite-list"`) || strings.Contains(list, revoke) {
			t.Fatalf("a revoked invite is still listed: %s", list)
		}
		if _, page := newClient(t, "dave").get("/join/" + link[1]); !strings.Contains(page, "Invite unavailable") {
			t.Fatalf("a revoked invite still works: %s", page)
		}
		if _, again := admin.hx(revoke, url.Values{}); !strings.Contains(again, "invite not found") {
			t.Fatalf("revoking twice: %s", again)
		}
	})

	t.Run("profile and team photos", func(t *testing.T) {
		pic := image.NewRGBA(image.Rect(0, 0, 400, 300))
		for i := range pic.Pix {
			if pic.Pix[i] = uint8(i); i%4 == 3 {
				pic.Pix[i] = 255
			}
		}
		var raw bytes.Buffer
		_ = png.Encode(&raw, pic)
		upload := func(c *client, path string, name string, data []byte) string {
			var buf bytes.Buffer
			mw := multipart.NewWriter(&buf)
			fw, _ := mw.CreateFormFile("photo", name)
			_, _ = fw.Write(data)
			_ = mw.Close()
			res, body := c.do(http.MethodPost, path, &buf, mw.FormDataContentType(), true)
			if res.StatusCode != http.StatusOK {
				t.Fatalf("%s: %d %s", path, res.StatusCode, body)
			}
			return body
		}
		body := upload(bob, "/settings/photo", "me.png", raw.Bytes())
		m := photoRe.FindStringSubmatch(body)
		if m == nil || m[1] != "u" {
			t.Fatalf("no photo after upload: %s", body)
		}
		userPhoto := m[0]
		res, got := admin.get(userPhoto)
		if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "image/jpeg" || !strings.Contains(res.Header.Get("Cache-Control"), "immutable") {
			t.Fatalf("photo: %d %v", res.StatusCode, res.Header)
		}
		if cfg, _, err := image.DecodeConfig(strings.NewReader(got)); err != nil || cfg.Width != 256 || cfg.Height != 256 {
			t.Fatalf("photo is not a 256px square: %v %v", cfg, err)
		}
		if res, _ := newClient(t, "anon").get(userPhoto); res.StatusCode == http.StatusOK {
			t.Fatal("a signed-out client got a photo")
		}
		if body := upload(bob, "/settings/photo", "x.svg", []byte("<svg xmlns='http://www.w3.org/2000/svg'/>")); !strings.Contains(body, "Use a JPEG, PNG, GIF or WebP picture") {
			t.Fatalf("svg accepted: %s", body)
		}

		if body := upload(bob, "/t/"+teamName+"/photo", "team.png", raw.Bytes()); !strings.Contains(body, "form-error") || strings.Contains(body, "Saved.") {
			t.Fatalf("a member set the team photo: %s", body)
		}
		var tinted bytes.Buffer
		sq := image.NewNRGBA(image.Rect(0, 0, 64, 64))
		for i := range 64 * 64 {
			sq.Set(i%64, i/64, color.NRGBA{200, 210, 250, 120})
		}
		_ = png.Encode(&tinted, sq)
		body = upload(admin, "/t/"+teamName+"/photo", "team.png", tinted.Bytes())
		m = photoRe.FindStringSubmatch(body)
		if m == nil || m[1] != "t" || !strings.Contains(body, `id="team-rail"`) {
			t.Fatalf("team photo: %s", body)
		}
		if res, _ := bob.get(m[0]); res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "image/png" {
			t.Fatalf("member fetching the team photo: %d %v", res.StatusCode, res.Header)
		}
		if _, body := admin.hx("/t/"+teamName+"/photo/remove", url.Values{}); photoRe.MatchString(body) {
			t.Fatalf("team photo not removed: %s", body)
		}

		if _, body := bob.hx("/settings/photo/remove", url.Values{}); photoRe.MatchString(body) {
			t.Fatalf("photo not removed: %s", body)
		}
		if res, _ := admin.get(userPhoto); res.StatusCode != http.StatusNotFound {
			t.Fatalf("removed photo still served: %d", res.StatusCode)
		}
	})

	t.Run("forms refuse multipart bodies and open redirects", func(t *testing.T) {
		res, _ := admin.do(http.MethodPost, "/t/"+teamName+"/invites", strings.NewReader("--x--"), "multipart/form-data; boundary=x", true)
		if res.StatusCode != http.StatusUnsupportedMediaType {
			t.Fatalf("a multipart form body was accepted: %d", res.StatusCode)
		}
		res, _ = admin.get("/login?next=" + url.QueryEscape("/\t/evil.example"))
		if loc := res.Header.Get("Location"); loc != "/" {
			t.Fatalf("redirected to %q", loc)
		}
	})

	t.Run("two-factor sign-in", func(t *testing.T) {
		_, body := bob.hx("/settings/totp/begin", url.Values{"password": {bobPass}})
		m := secretRe.FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("no secret: %s", body)
		}
		code, _ := totp.GenerateCode(m[1], time.Now())
		_, body = bob.hx("/settings/totp/confirm", url.Values{"code": {code}})
		codes := codeRe.FindAllStringSubmatch(body, -1)
		if len(codes) != 10 {
			t.Fatalf("no recovery codes: %s", body)
		}
		fresh := newClient(t, "bob2")
		res, _ := fresh.form("/login", url.Values{"login": {bobName}, "password": {bobPass}})
		if res.Header.Get("Location") != "/login/mfa" || fresh.cookie("gochat_session") != "" {
			t.Fatalf("password alone signed in: %d %s", res.StatusCode, res.Header.Get("Location"))
		}
		_, _ = fresh.form("/login/mfa", url.Values{"code": {code}})
		if fresh.cookie("gochat_session") != "" {
			t.Fatal("a used TOTP code was accepted again")
		}
		res, _ = fresh.form("/login/mfa", url.Values{"code": {codes[0][1]}})
		if res.StatusCode != http.StatusSeeOther || fresh.cookie("gochat_session") == "" {
			t.Fatalf("recovery code refused: %d", res.StatusCode)
		}
		again := newClient(t, "bob3")
		again.form("/login", url.Values{"login": {bobName}, "password": {bobPass}})
		again.form("/login/mfa", url.Values{"code": {codes[0][1]}})
		if again.cookie("gochat_session") != "" {
			t.Fatal("a recovery code worked twice")
		}
	})

	t.Run("logout revokes the socket", func(t *testing.T) {
		res, _ := bob.hx("/logout", url.Values{})
		if res.Header.Get("HX-Redirect") != "/login" {
			t.Fatalf("logout: %d %v", res.StatusCode, res.Header)
		}
		bobWS.expect(5*time.Second, `data-redirect="/login"`)
	})

	fmt.Fprintln(os.Stderr, "e2e users:", bobName)
}

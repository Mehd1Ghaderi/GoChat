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

	"github.com/coder/websocket"
	"github.com/pquerna/otp/totp"
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
	inviteRe  = regexp.MustCompile(`/join/([A-Za-z0-9_-]{43})`)
	channelRe = regexp.MustCompile(`/t/[a-z0-9-]+/ch/([0-9a-f-]{36})`)
	attRe     = regexp.MustCompile(`name="attachment" value="([0-9a-f-]{36})"`)
	secretRe  = regexp.MustCompile(`<code>([A-Z2-7]{16,})</code>`)
	codeRe    = regexp.MustCompile(`<code>([a-z2-7]{5}-[a-z2-7]{5})</code>`)
	teamIDRe  = regexp.MustCompile(`data-team="([0-9a-f-]{36})"`)
)

func TestEndToEnd(t *testing.T) {
	if baseURL == "" {
		t.Skip("GOCHAT_E2E_URL is not set")
	}
	admin := newClient(t, "admin")
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

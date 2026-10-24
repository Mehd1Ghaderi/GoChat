package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
)

func TestAccountFragments(t *testing.T) {
	r, err := New(16)
	if err != nil {
		t.Fatal(err)
	}
	now := timestamppb.New(time.Date(2026, 10, 24, 14, 5, 0, 0, time.UTC))
	current := &pb.SessionInfo{Id: "aaaaaaaaaaaaaaaaaaaaaaaa", UserAgent: "Firefox/140.0 (X11; Linux)", Ip: "10.0.0.1", CreatedAt: now, LastSeenAt: now, Current: true}
	other := &pb.SessionInfo{Id: "bbbbbbbbbbbbbbbbbbbbbbbb", UserAgent: "<b>evil</b>", Ip: "10.0.0.2", LastIp: "10.0.0.3", CreatedAt: now}
	var buf bytes.Buffer
	if err := r.Fragment(&buf, "sessions", map[string]any{"List": []*pb.SessionInfo{current, other}, "Others": true}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"This device", "/settings/sessions/bbbbbbbbbbbbbbbbbbbbbbbb/revoke", "10.0.0.3", "from 10.0.0.2", "last seen never", "Sign out all other sessions"} {
		if !strings.Contains(out, want) {
			t.Errorf("sessions lack %q: %s", want, out)
		}
	}
	if strings.Contains(out, "/settings/sessions/aaaaaaaaaaaaaaaaaaaaaaaa/revoke") || strings.Contains(out, "<b>evil") {
		t.Errorf("the current session can be revoked here, or an agent is unescaped: %s", out)
	}

	user := &pb.User{Id: "u1", Username: "bob", DisplayName: "Bob", CreatedAt: now}
	card := func(data map[string]any) string {
		buf.Reset()
		if err := r.Fragment(&buf, "admin-user", data); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	out = card(map[string]any{"User": user, "Email": "bob@example.test", "Sessions": []*pb.SessionInfo{other},
		"Storage": &pb.GetStorageUsageResponse{Used: 3 << 20, Quota: 10 << 20}})
	for _, want := range []string{`id="admin-user"`, "Deactivate", "/admin/users/u1/sessions/revoke", `value="10"`, "3.0 MB used of 10.0 MB"} {
		if !strings.Contains(out, want) {
			t.Errorf("the card lacks %q: %s", want, out)
		}
	}
	user.Deactivated = true
	out = card(map[string]any{"User": user, "Storage": &pb.GetStorageUsageResponse{Quota: 5 << 20, Custom: true}, "Self": false})
	if !strings.Contains(out, "Reactivate") || !strings.Contains(out, "Use the default") || !strings.Contains(out, "Not signed in anywhere") {
		t.Errorf("a deactivated user's card: %s", out)
	}
	out = card(map[string]any{"User": user, "Storage": &pb.GetStorageUsageResponse{}, "Self": true})
	if strings.Contains(out, "/deactivate") || strings.Contains(out, "<progress") {
		t.Errorf("an admin can deactivate themself, or no quota shows a meter: %s", out)
	}
}

func TestAccountPages(t *testing.T) {
	r, err := New(16)
	if err != nil {
		t.Fatal(err)
	}
	now := timestamppb.Now()
	admin := &pb.User{Id: "a1", Username: "ada", Role: pb.SystemRole_SYSTEM_ROLE_ADMIN, CreatedAt: now}
	var buf bytes.Buffer
	err = r.Page(&buf, "settings", map[string]any{
		"Title": "Settings", "CSRF": "x", "User": admin, "Email": "ada@example.test",
		"Sessions": map[string]any{"List": []*pb.SessionInfo{{Id: "aaaaaaaaaaaaaaaaaaaaaaaa", CreatedAt: now, Current: true}}},
		"Storage":  &pb.GetStorageUsageResponse{Used: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); !strings.Contains(out, `id="sessions"`) || !strings.Contains(out, "10 B used.") || !strings.Contains(out, `href="/admin/users"`) {
		t.Errorf("settings: %s", out)
	}
	buf.Reset()
	err = r.Page(&buf, "admin", map[string]any{"Title": "Users", "CSRF": "x", "User": admin, "Query": "bob", "Error": "No one has that username or email."})
	if err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); !strings.Contains(out, `value="bob"`) || !strings.Contains(out, "No one has that") {
		t.Errorf("admin: %s", out)
	}
}

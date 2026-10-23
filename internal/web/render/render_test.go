package render

import (
	"bytes"
	"strings"
	"testing"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
)

func TestTemplatesParse(t *testing.T) {
	if _, err := New(16); err != nil {
		t.Fatal(err)
	}
}

func TestMarkdownIsSafe(t *testing.T) {
	r, err := New(16)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"<script>alert(1)</script>":          "<script",
		"[x](javascript:alert(1))":           "javascript:",
		"![t](https://evil.example/t.png)":   "<img",
		"<img src=x onerror=alert(1)>":       "onerror",
		"<a href=\"https://x\">x</a>":        "<a href=\"https://x\">",
		"[x](https://ok.example \"t\")":      "<script",
		"hi <b onclick=\"x()\">there</b>":    "onclick",
		"`<i>` and ```\n<svg onload=1>\n```": "<svg",
	}
	for in, bad := range cases {
		out := string(r.markdown(in))
		if strings.Contains(out, bad) {
			t.Errorf("%q rendered %q", in, out)
		}
	}
	out := string(r.markdown("see https://example.com"))
	if !strings.Contains(out, `rel="nofollow noopener noreferrer"`) || !strings.Contains(out, `target="_blank"`) {
		t.Errorf("autolink lacks rel and target: %q", out)
	}
}

func TestMarkdownCannotBlowUp(t *testing.T) {
	r, err := New(16)
	if err != nil {
		t.Fatal(err)
	}
	wide := strings.Repeat("|a", 2000) + "|\n" + strings.Repeat("|-", 2000) + "|\n" + strings.Repeat("|\n", 4000)
	long := "|a|b|\n|-|-|\n" + strings.Repeat("|\n", 7000)
	cases := []string{wide, long, strings.Repeat(">", 8000) + " x", strings.Repeat("- ", 5000) + "x"}
	for _, in := range cases {
		if out := r.markdown(in); len(out) > 4*len(in)+64 {
			t.Errorf("%d bytes of input rendered to %d", len(in), len(out))
		}
	}
	if out := string(r.markdown("|a|b|\n|-|-|\n|1|2|")); !strings.Contains(out, "<table>") {
		t.Errorf("small table not rendered: %q", out)
	}
	if out := string(r.markdown("> quote\n- item\n  - nested")); !strings.Contains(out, "<blockquote>") || !strings.Contains(out, "<ul>") {
		t.Errorf("quote and list not rendered: %q", out)
	}
}

func TestUnread(t *testing.T) {
	older := "00000000-0000-1000-8000-000000000000"
	newer := "00000001-0000-1000-8000-000000000000"
	cross := "ffffffff-0000-1000-8000-000000000000"
	later := "00000000-0001-1000-8000-000000000000"
	if !Unread(&pb.Channel{LastMessageId: newer, LastReadId: older}) {
		t.Error("newer message should be unread")
	}
	if Unread(&pb.Channel{LastMessageId: older, LastReadId: newer}) {
		t.Error("older message should be read")
	}
	if !Unread(&pb.Channel{LastMessageId: later, LastReadId: cross}) {
		t.Error("time_mid outranks time_low")
	}
}

func TestMessageFragment(t *testing.T) {
	r, err := New(16)
	if err != nil {
		t.Fatal(err)
	}
	m := r.Message(&pb.Message{Id: "id1", ChannelId: "c1", Content: "<b>hi</b>", Author: &pb.User{Id: "u", Username: "u", DisplayName: "<U>"}}, "u", false)
	var buf bytes.Buffer
	if err := r.Fragment(&buf, "message-new", m); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "<b>hi") || strings.Contains(out, "<U>") {
		t.Errorf("unescaped content: %s", out)
	}
	if !strings.Contains(out, `hx-swap-oob="afterbegin"`) {
		t.Errorf("missing oob: %s", out)
	}
}

func TestPhotos(t *testing.T) {
	r, err := New(16)
	if err != nil {
		t.Fatal(err)
	}
	u := &pb.User{Id: "11111111-1111-4111-8111-111111111111", Username: "ann", Photo: "0123456789abcdef"}
	var out bytes.Buffer
	field := map[string]any{"ID": "profile-photo", "URL": "/settings/photo", "User": u}
	if err := r.Fragment(&out, "photo-field", field); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); !strings.Contains(s, `src="/photos/u/`+u.Id+`/0123456789abcdef"`) || !strings.Contains(s, "Remove") {
		t.Fatalf("user photo field: %s", s)
	}
	out.Reset()
	team := &pb.Team{Id: "22222222-2222-4222-8222-222222222222", Name: "acme", DisplayName: "Acme"}
	if err := r.Fragment(&out, "photo-field", struct {
		ID, URL, Error, Status string
		User                   *pb.User
		Team                   *pb.Team
	}{ID: "team-photo", URL: "/t/acme/photo", Team: team}); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); strings.Contains(s, "<img") || strings.Contains(s, "Remove") || !strings.Contains(s, `lg" aria-hidden="true">A</span>`) {
		t.Fatalf("team without photo: %s", s)
	}
	out.Reset()
	if err := r.Fragment(&out, "avatar", map[string]any{"User": (*pb.User)(nil), "Size": "sm"}); err != nil {
		t.Fatalf("nil user: %v", err)
	}
}

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

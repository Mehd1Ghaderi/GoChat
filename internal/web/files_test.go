package web

import (
	"slices"
	"strings"
	"testing"

	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/web/render"
)

func TestParseRange(t *testing.T) {
	cases := map[string]*pb.ByteRange{
		"bytes=0-1":                   {First: 0, Last: 1},
		"bytes=10-":                   {First: 10, Last: -1},
		"bytes=-500":                  {Suffix: 500},
		"bytes= 3-4":                  {First: 3, Last: 4},
		"":                            nil,
		"bytes=-0":                    nil,
		"bytes=5-4":                   nil,
		"bytes=+5-":                   nil,
		"bytes=0-1,4-5":               nil,
		"items=0-1":                   nil,
		"bytes=x-1":                   nil,
		"bytes=99999999999999999999-": nil,
	}
	for h, want := range cases {
		if got := parseRange(h); !proto.Equal(got, want) {
			t.Errorf("parseRange(%q) = %v, want %v", h, got, want)
		}
	}
}

func TestPolicyAllowsWebAssemblyOnly(t *testing.T) {
	r, err := render.New(16)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(zap.NewNop(), &Config{PublicURL: "https://chat.example.com", RateLimit: &RateLimitConfig{}}, nil, r, nil, nil)
	if !strings.Contains(h.csp, "script-src 'self' 'wasm-unsafe-eval';") || strings.Contains(h.csp, "'unsafe-eval'") || strings.Contains(h.csp, "'unsafe-inline'") {
		t.Fatalf("policy: %s", h.csp)
	}
}

func TestSourcesDropRepeats(t *testing.T) {
	got := sources([]string{"'self'", "wss://chat.example.com", "wss://Chat.example.com/", "wss://voice.example.com"})
	if want := []string{"'self'", "wss://chat.example.com", "wss://voice.example.com"}; !slices.Equal(got, want) {
		t.Fatalf("sources: %v", got)
	}
}

package web

import (
	"strings"
	"testing"
)

func TestPublicURLIsAnOrigin(t *testing.T) {
	cases := map[string]string{
		"https://Chat.Example.com:443/": "https://chat.example.com",
		"http://localhost:80":           "http://localhost",
		"http://localhost:8080":         "http://localhost:8080",
		"HTTPS://[::1]:8443":            "https://[::1]:8443",
	}
	for in, want := range cases {
		c := &Config{PublicURL: in}
		// Later checks fail on the empty config; the origin is set first.
		_ = c.Validate()
		if c.PublicURL != want {
			t.Errorf("%q became %q, want %q", in, c.PublicURL, want)
		}
	}
}

func TestMinAppVersionIsAVersion(t *testing.T) {
	for v, ok := range map[string]bool{"": true, "0.3": true, "1.2.0": true, "12": true, "v1.2": false, "1.2.3.4": false, "1.x": false} {
		if err := (&Config{PublicURL: "http://localhost", MinAppVersion: v}).Validate(); (err == nil || !strings.Contains(err.Error(), "min_app_version")) != ok {
			t.Errorf("%q: %v", v, err)
		}
	}
}

package web

import "testing"

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

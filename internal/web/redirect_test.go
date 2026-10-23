package web

import "testing"

func TestSafeNext(t *testing.T) {
	cases := map[string]string{
		"":                     "/",
		"/t/acme":              "/t/acme",
		"//evil.example":       "/",
		"/\\evil.example":      "/",
		"https://evil.example": "/",
		"/ok\r\nSet-Cookie:x":  "/",
	}
	for in, want := range cases {
		if got := safeNext(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

package render

import "testing"

func TestDevice(t *testing.T) {
	cases := map[string]string{
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15":                   "Safari on macOS",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36":                         "Chrome on Windows",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36 Edg/140.0.0":             "Edge on Windows",
		"Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0":                                                                  "Firefox on Linux",
		"Mozilla/5.0 (Linux; Android 15) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Mobile Safari/537.36":                            "Chrome on Android",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1": "Safari on iPhone",
		"Go-http-client/1.1":                 "Go-http-client",
		"GoChat/0.2.0 (Android 16; Pixel 8)": "GoChat app on Pixel 8",
		"GoChat/0.2.0 (iOS 18.4; iPhone)":    "GoChat app on iPhone",
		"GoChat/0.2.0 (Android 16)":          "GoChat app on Android",
		"GoChat/0.2.0":                       "GoChat app",
		"":                                   "Unknown device",
		"<script> (x)":                       "Unknown device",
	}
	for ua, want := range cases {
		if got := Device(ua); got != want {
			t.Errorf("Device(%q) = %q, want %q", ua, got, want)
		}
	}
}

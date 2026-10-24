package audio

import "testing"

func TestSniff(t *testing.T) {
	cases := []struct {
		name string
		head []byte
		want string
		ok   bool
	}{
		{"webm", []byte{0x1a, 0x45, 0xdf, 0xa3, 0x9f, 0x42, 0x86, 0x81}, "audio/webm", true},
		{"ogg", []byte("OggS\x00\x02\x00\x00"), "audio/ogg", true},
		{"mp4", []byte("\x00\x00\x00\x1cftypM4A \x00\x00\x00\x00"), "audio/mp4", true},
		{"png", []byte("\x89PNG\r\n\x1a\n"), "", false},
		{"text", []byte("hello world"), "", false},
		{"short ftyp", []byte("\x00\x00ftyp"), "", false},
		{"empty", nil, "", false},
	}
	for _, c := range cases {
		got, ok := Sniff(c.head)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: Sniff = %q, %v; want %q, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestPlayable(t *testing.T) {
	for _, ct := range []string{"audio/webm", "audio/ogg", "audio/mp4"} {
		if !Playable(ct) {
			t.Errorf("Playable(%q) = false", ct)
		}
	}
	for _, ct := range []string{"", "audio/mpeg", "video/webm", "text/html", "image/svg+xml"} {
		if Playable(ct) {
			t.Errorf("Playable(%q) = true", ct)
		}
	}
}

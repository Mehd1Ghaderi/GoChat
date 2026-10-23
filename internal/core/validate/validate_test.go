package validate

import "testing"

func TestUsername(t *testing.T) {
	if u, err := Username("  Alice.B "); err != nil || u != "alice.b" {
		t.Fatalf("%q %v", u, err)
	}
	for _, bad := range []string{"ab", "-alice", "alice smith", "al<i>ce", "ålice"} {
		if _, err := Username(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestEmail(t *testing.T) {
	if _, err := Email("Bob@Example.com"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"bob", "bob@localhost", "Bob <bob@example.com>", "bob@exa mple.com"} {
		if _, err := Email(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestLineRejectsInvisibleFormatting(t *testing.T) {
	if _, err := Line("name", "evil\u202egnp.exe", 1, 64); err == nil {
		t.Fatal("bidi override accepted")
	}
	if _, err := Line("name", "tab\there", 1, 64); err == nil {
		t.Fatal("control character accepted")
	}
}

func TestText(t *testing.T) {
	out, err := Text("message", "hi\r\nthere\x00\x07", 100)
	if err != nil || out != "hi\nthere" {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := Text("message", "toolong", 3); err == nil {
		t.Fatal("long text accepted")
	}
}

func TestFilename(t *testing.T) {
	cases := map[string]string{
		"../../etc/passwd":   "passwd",
		`C:\Users\a\b.txt`:   "b.txt",
		"a\"b\u202e.png":     "ab.png",
		"":                   "file",
		"..":                 "file",
		"report (final).pdf": "report (final).pdf",
	}
	for in, want := range cases {
		if got := Filename(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

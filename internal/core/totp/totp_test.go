package totp

import (
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestMatch(t *testing.T) {
	secret, url, err := Generate("GoChat", "alice")
	if err != nil || secret == "" || url == "" {
		t.Fatal(err)
	}
	now := time.Now()
	code, _ := totp.GenerateCode(secret, now)
	step, ok := Match(secret, code, now)
	if !ok || step != now.Unix()/30 {
		t.Fatalf("current code refused: %v %d", ok, step)
	}
	if _, ok := Match(secret, code, now.Add(3*time.Minute)); ok {
		t.Fatal("stale code accepted")
	}
	if _, ok := Match(secret, "12345", now); ok {
		t.Fatal("short code accepted")
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes := RecoveryCodes(10)
	seen := map[string]bool{}
	for _, c := range codes {
		n, ok := NormalizeRecovery(c)
		if !ok || n != c {
			t.Fatalf("code %q normalizes to %q", c, n)
		}
		if seen[c] {
			t.Fatal("duplicate code")
		}
		seen[c] = true
	}
	if n, ok := NormalizeRecovery(" ABCDE fghij "); !ok || n != "abcde-fghij" {
		t.Fatalf("typed code: %q %v", n, ok)
	}
	if _, ok := NormalizeRecovery("abcde-fgh1j"); ok {
		t.Fatal("non-base32 code accepted")
	}
}

package secure

import (
	"bytes"
	"testing"
)

func TestToken(t *testing.T) {
	a, b := Token(), Token()
	if a == b || !ValidToken(a) || ValidToken(a+"x") || ValidToken("") {
		t.Fatal("token shape")
	}
}

func TestSealer(t *testing.T) {
	s, err := NewSealer(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed := s.Seal([]byte("secret"), []byte("user-1"))
	out, err := s.Open(sealed, []byte("user-1"))
	if err != nil || string(out) != "secret" {
		t.Fatalf("open: %v %q", err, out)
	}
	if _, err := s.Open(sealed, []byte("user-2")); err == nil {
		t.Fatal("sealed value moved to another row")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := s.Open(sealed, []byte("user-1")); err == nil {
		t.Fatal("tampered value opened")
	}
	if _, err := NewSealer([]byte("short")); err == nil {
		t.Fatal("short key accepted")
	}
}

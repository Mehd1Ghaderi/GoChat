// Package password hashes passwords with argon2id in the PHC string format,
// so parameters can change without invalidating stored hashes.
package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

type Params struct {
	Time    uint32 `mapstructure:"time"`
	Memory  uint32 `mapstructure:"memory_kib"`
	Threads uint8  `mapstructure:"threads"`
}

func (p Params) Validate() error {
	if p.Time < 1 {
		return errors.New("time must be at least 1")
	}
	if p.Memory < 8*1024 {
		return errors.New("memory_kib must be at least 8192")
	}
	if p.Threads < 1 {
		return errors.New("threads must be at least 1")
	}
	return nil
}

const (
	saltLen = 16
	keyLen  = 32
)

var errMalformed = errors.New("malformed password hash")

func Hash(password string, p Params) string {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		panic(err)
	}
	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, keyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

// Verify reports whether password matches encoded, and whether encoded was
// made with other parameters than p and should be replaced.
func Verify(password, encoded string, p Params) (ok, rehash bool, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, false, errMalformed
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, false, errMalformed
	}
	var stored Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &stored.Memory, &stored.Time, &stored.Threads); err != nil {
		return false, false, errMalformed
	}
	// A stored hash is trusted input, but its cost still bounds what one
	// verification may take.
	if stored.Memory > 1<<20 || stored.Time > 64 || stored.Threads == 0 {
		return false, false, errMalformed
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, false, errMalformed
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return false, false, errMalformed
	}
	got := argon2.IDKey([]byte(password), salt, stored.Time, stored.Memory, stored.Threads, uint32(len(key)))
	if subtle.ConstantTimeCompare(got, key) != 1 {
		return false, false, nil
	}
	return true, stored != p, nil
}

// Dummy burns the same time as a real verification, for unknown accounts.
func Dummy(password string, p Params) {
	_ = argon2.IDKey([]byte(password), make([]byte, saltLen), p.Time, p.Memory, p.Threads, keyLen)
}

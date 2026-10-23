// Package totp implements RFC 6238 codes and single-use recovery codes.
package totp

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

const (
	period = 30
	skew   = 1
)

var opts = totp.ValidateOpts{Period: period, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}

// Generate returns a new base32 secret and its otpauth:// URL.
func Generate(issuer, account string) (secret, url string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: account,
		Period:      period,
		SecretSize:  20,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

// Match returns the time step code is valid for, within one step of now.
// Callers must refuse a step at or below the last one accepted.
func Match(secret, code string, now time.Time) (step int64, ok bool) {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return 0, false
	}
	current := now.Unix() / period
	for d := -skew; d <= skew; d++ {
		s := current + int64(d)
		want, err := totp.GenerateCodeCustom(secret, time.Unix(s*period, 0), opts)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return s, true
		}
	}
	return 0, false
}

var recoveryEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// RecoveryCodes returns n codes shaped xxxxx-xxxxx.
func RecoveryCodes(n int) []string {
	codes := make([]string, n)
	for i := range codes {
		b := make([]byte, 7)
		if _, err := rand.Read(b); err != nil {
			panic(err)
		}
		s := strings.ToLower(recoveryEncoding.EncodeToString(b))[:10]
		codes[i] = s[:5] + "-" + s[5:]
	}
	return codes
}

// NormalizeRecovery returns the canonical form of a typed recovery code, or
// false if it cannot be one.
func NormalizeRecovery(code string) (string, bool) {
	code = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(code), " ", ""))
	code = strings.ReplaceAll(code, "-", "")
	if len(code) != 10 {
		return "", false
	}
	for _, r := range code {
		if (r < 'a' || r > 'z') && (r < '2' || r > '7') {
			return "", false
		}
	}
	return code[:5] + "-" + code[5:], true
}

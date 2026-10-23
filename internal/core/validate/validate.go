// Package validate normalizes and checks every value a person types.
package validate

import (
	"net/mail"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ssenerg/gochat/internal/errors"
)

var (
	usernameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,31}$`)
	slugRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,49}$`)
)

func Username(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if !usernameRe.MatchString(s) {
		return "", errors.InvalidArgument("username must be 3-32 characters: lowercase letters, digits, '.', '_' or '-'")
	}
	return s, nil
}

func Email(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) > 254 {
		return "", errors.InvalidArgument("email is too long")
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Address != s || !strings.Contains(s[strings.LastIndexByte(s, '@'):], ".") {
		return "", errors.InvalidArgument("email is invalid")
	}
	return s, nil
}

// Slug checks the URL name of a team or channel.
func Slug(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if !slugRe.MatchString(s) {
		return "", errors.InvalidArgument("name must be 2-50 characters: lowercase letters, digits or '-'")
	}
	return s, nil
}

// Line checks a single line of free text such as a display name.
func Line(field, s string, minRunes, maxRunes int) (string, error) {
	s = strings.TrimSpace(s)
	n := utf8.RuneCountInString(s)
	if !utf8.ValidString(s) || n < minRunes || n > maxRunes {
		return "", errors.InvalidArgument(field + " has an invalid length")
	}
	for _, r := range s {
		if unicode.IsControl(r) || isInvisibleFormat(r) {
			return "", errors.InvalidArgument(field + " contains invalid characters")
		}
	}
	return s, nil
}

// Text checks multi-line free text such as a message.
func Text(field, s string, maxRunes int) (string, error) {
	if !utf8.ValidString(s) {
		return "", errors.InvalidArgument(field + " is not valid UTF-8")
	}
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if utf8.RuneCountInString(s) > maxRunes {
		return "", errors.InvalidArgument(field + " is too long")
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\n' || r == '\t' || (!unicode.IsControl(r) && r != utf8.RuneError) {
			b.WriteRune(r)
		}
	}
	return b.String(), nil
}

func Password(s string, minLen int) error {
	if len(s) > 256 {
		return errors.InvalidArgument("password must be at most 256 bytes")
	}
	if utf8.RuneCountInString(s) < minLen {
		return errors.InvalidArgument("password is too short")
	}
	if strings.TrimSpace(s) == "" {
		return errors.InvalidArgument("password is invalid")
	}
	return nil
}

// Filename returns a safe display name for an uploaded file.
func Filename(s string) string {
	s = path.Base(strings.ReplaceAll(s, "\\", "/"))
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) || isInvisibleFormat(r) || r == '"' || r == '/' || r == utf8.RuneError {
			continue
		}
		b.WriteRune(r)
	}
	s = strings.TrimSpace(b.String())
	for len(s) > 200 {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	if s == "" || s == "." || s == ".." {
		return "file"
	}
	return s
}

// isInvisibleFormat catches bidi overrides and other format characters that
// can disguise text.
func isInvisibleFormat(r rune) bool {
	return unicode.Is(unicode.Cf, r)
}

// Package mention finds the people a message calls out: @username, and
// @channel, @all or @here for everyone in a channel or everyone online.
package mention

import (
	"regexp"
	"strings"
)

// MaxNames bounds the names one message can mention.
const MaxNames = 20

type Mentions struct {
	// Usernames are lowercase and unique.
	Usernames []string
	// Channel is set by @channel or @all, Here by @here.
	Channel bool
	Here    bool
}

// Mentions reports whether m calls out username.
func (m Mentions) Of(username string) bool {
	for _, u := range m.Usernames {
		if u == username {
			return true
		}
	}
	return false
}

var (
	fenced = regexp.MustCompile("(?s)(```|~~~).*?(```|~~~|$)")
	inline = regexp.MustCompile("`[^`\n]*`")
)

// Parse finds the mentions in a message's text, outside of code.
func Parse(text string) Mentions {
	var out Mentions
	if !strings.Contains(text, "@") {
		return out
	}
	text = inline.ReplaceAllString(fenced.ReplaceAllString(text, " "), " ")
	seen := map[string]bool{}
	for i := 0; i < len(text); i++ {
		if text[i] != '@' || (i > 0 && isNameByte(text[i-1])) || (i > 0 && text[i-1] == '@') {
			continue
		}
		j := i + 1
		for j < len(text) && isNameByte(text[j]) {
			j++
		}
		name := strings.ToLower(strings.TrimRight(text[i+1:j], "._-"))
		i = j - 1
		switch {
		case name == "":
		case name == "channel" || name == "all":
			out.Channel = true
		case name == "here":
			out.Here = true
		case len(name) <= 32 && !seen[name] && len(out.Usernames) < MaxNames:
			seen[name] = true
			out.Usernames = append(out.Usernames, name)
		}
	}
	return out
}

func isNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-'
}

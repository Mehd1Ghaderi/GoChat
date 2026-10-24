// Package search turns text into the terms the message index keeps, and what
// a person types into the search box into a query over them.
package search

import (
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kljensen/snowball/english"
	"golang.org/x/text/unicode/norm"

	ierrors "github.com/ssenerg/gochat/internal/errors"
)

const (
	// MaxTerms bounds the distinct terms indexed for one message.
	MaxTerms = 300
	// MaxQueryTerms bounds the words one search looks for.
	MaxQueryTerms  = 8
	MaxQueryLength = 256
	minRunes       = 2
	maxRunes       = 40
)

// Too common to be worth an index entry.
var stopWords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true, "be": true, "but": true,
	"by": true, "for": true, "if": true, "in": true, "into": true, "is": true, "it": true, "no": true,
	"not": true, "of": true, "on": true, "or": true, "so": true, "such": true, "that": true, "the": true,
	"their": true, "then": true, "there": true, "these": true, "they": true, "this": true, "to": true,
	"was": true, "will": true, "with": true, "we": true, "you": true, "i": true, "me": true, "my": true,
}

// Word is one word of a text, where it lies in bytes, and its term: empty
// when the word is not indexed.
type Word struct {
	Term       string
	Start, End int
}

func inWord(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.In(r, unicode.Mn, unicode.Mc) || r == '‌'
}

// Words splits text into its words.
func Words(text string) []Word {
	var out []Word
	start := -1
	for i, r := range text {
		if inWord(r) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			out = append(out, Word{Term: Term(text[start:i]), Start: start, End: i})
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, Word{Term: Term(text[start:]), Start: start, End: len(text)})
	}
	return out
}

// Term normalizes one word into the form the index keeps, or "" for a word
// it skips. Spellings that differ only in Arabic or Persian letter variants,
// diacritics, digits or English inflection share a term.
func Term(word string) string {
	if len(word) > 8*maxRunes {
		return ""
	}
	var b strings.Builder
	for _, r := range norm.NFKC.String(word) {
		switch {
		case r == '‌' || r == 'ـ' || (r >= 'ً' && r <= 'ٟ') || r == 'ٰ':
			continue
		case r == 'ي' || r == 'ى':
			r = 'ی'
		case r == 'ك':
			r = 'ک'
		case r == 'ة' || r == 'ۀ':
			r = 'ه'
		case r == 'أ' || r == 'إ':
			r = 'ا'
		case r >= '۰' && r <= '۹':
			r = '0' + (r - '۰')
		case r >= '٠' && r <= '٩':
			r = '0' + (r - '٠')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	t := b.String()
	if n := utf8.RuneCountInString(t); n < minRunes || n > maxRunes || stopWords[t] {
		return ""
	}
	if asciiLetters(t) {
		t = english.Stem(t, false)
	}
	return t
}

func asciiLetters(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 'a' || c > 'z' {
			return false
		}
	}
	return true
}

// Stream returns the terms of text in order, repeats included.
func Stream(text string) []string {
	words := Words(text)
	out := make([]string, 0, len(words))
	for _, w := range words {
		if w.Term != "" {
			out = append(out, w.Term)
		}
	}
	return out
}

// Terms returns the distinct terms of text in order, at most MaxTerms.
func Terms(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range Stream(text) {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
			if len(out) == MaxTerms {
				break
			}
		}
	}
	return out
}

// Matches returns the words of text whose term is one of terms.
func Matches(text string, terms []string) []Word {
	want := make(map[string]bool, len(terms))
	for _, t := range terms {
		want[t] = true
	}
	var out []Word
	for _, w := range Words(text) {
		if w.Term != "" && want[w.Term] {
			out = append(out, w)
		}
	}
	return out
}

// Query is what to look for: messages holding every term and phrase and
// none of the excluded terms, with the filters set.
type Query struct {
	Terms   []string
	Phrases [][]string
	Exclude []string
	// From is a username and In a channel's URL name.
	From string
	In   string
	// After is inclusive and Before exclusive; zero when unset.
	After  time.Time
	Before time.Time
}

var (
	usernameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)
	slugRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,49}$`)
)

type token struct {
	text    string
	quoted  bool
	negated bool
}

func tokens(s string) []token {
	var out []token
	for i := 0; i < len(s); {
		if s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r' {
			i++
			continue
		}
		t := token{}
		if s[i] == '-' && i+1 < len(s) && s[i+1] != ' ' {
			t.negated = true
			i++
		}
		if s[i] == '"' {
			end := strings.IndexByte(s[i+1:], '"')
			if end < 0 {
				end = len(s) - i - 1
			}
			t.text, t.quoted = s[i+1:i+1+end], true
			i += end + 2
		} else {
			end := strings.IndexAny(s[i:], " \t\r\n")
			if end < 0 {
				end = len(s) - i
			}
			t.text = s[i : i+end]
			i += end
		}
		out = append(out, t)
	}
	return out
}

// Parse reads a search: words, "phrases", -excluded words and the filters
// from:username, in:channel, before:, after: and on: with dates like
// 2026-01-31.
func Parse(s string) (*Query, error) {
	if len(s) > MaxQueryLength {
		return nil, ierrors.InvalidArgument("the search is too long")
	}
	q := &Query{}
	seen := map[string]bool{}
	add := func(terms []string) {
		for _, t := range terms {
			if !seen[t] {
				seen[t] = true
				q.Terms = append(q.Terms, t)
			}
		}
	}
	for _, tok := range tokens(s) {
		if tok.quoted {
			terms := Stream(tok.text)
			if len(terms) > 1 && !tok.negated {
				q.Phrases = append(q.Phrases, terms)
			}
			if tok.negated {
				q.Exclude = append(q.Exclude, terms...)
			} else {
				add(terms)
			}
			continue
		}
		if key, val, ok := strings.Cut(tok.text, ":"); ok && !tok.negated {
			done, err := q.filter(strings.ToLower(key), val)
			if err != nil {
				return nil, err
			}
			if done {
				continue
			}
		}
		terms := Stream(tok.text)
		switch {
		case tok.negated:
			q.Exclude = append(q.Exclude, terms...)
		case len(terms) > 1:
			q.Phrases = append(q.Phrases, terms)
			add(terms)
		default:
			add(terms)
		}
	}
	if len(q.Terms) > MaxQueryTerms {
		return nil, ierrors.InvalidArgument("search for at most 8 words at a time")
	}
	if len(q.Terms) == 0 && q.In == "" {
		return nil, ierrors.InvalidArgument("type a word to search for: very common words are not searched")
	}
	if !q.After.IsZero() && !q.Before.IsZero() && !q.After.Before(q.Before) {
		return nil, ierrors.InvalidArgument("those dates leave nothing to search")
	}
	return q, nil
}

func (q *Query) filter(key, val string) (bool, error) {
	switch key {
	case "from":
		name := strings.ToLower(strings.TrimPrefix(val, "@"))
		if !usernameRe.MatchString(name) {
			return false, ierrors.InvalidArgument("from: takes a username, like from:alice")
		}
		q.From = name
	case "in":
		name := strings.ToLower(strings.TrimLeft(val, "~#"))
		if !slugRe.MatchString(name) {
			return false, ierrors.InvalidArgument("in: takes a channel's URL name, like in:town-square")
		}
		q.In = name
	case "before", "after", "on":
		day, err := time.Parse(time.DateOnly, val)
		if err != nil {
			return false, ierrors.InvalidArgument("write dates like 2026-01-31")
		}
		switch key {
		case "before":
			q.Before = day
		case "after":
			q.After = day.Add(24 * time.Hour)
		default:
			q.After, q.Before = day, day.Add(24*time.Hour)
		}
	default:
		return false, nil
	}
	return true, nil
}

// Match reports whether text holds what q looks for. Filters are the
// caller's to check.
func (q *Query) Match(text string) bool {
	stream := Stream(text)
	have := make(map[string]bool, len(stream))
	for _, t := range stream {
		have[t] = true
	}
	for _, t := range q.Terms {
		if !have[t] {
			return false
		}
	}
	for _, t := range q.Exclude {
		if have[t] {
			return false
		}
	}
	for _, p := range q.Phrases {
		if !contains(stream, p) {
			return false
		}
	}
	return true
}

func contains(stream, phrase []string) bool {
	for i := 0; i+len(phrase) <= len(stream); i++ {
		match := true
		for j, t := range phrase {
			if stream[i+j] != t {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

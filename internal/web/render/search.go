package render

import (
	"html/template"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	pb "github.com/ssenerg/gochat/api/protobuf/gen/gochat/v1"
	"github.com/ssenerg/gochat/internal/core/emoji"
	"github.com/ssenerg/gochat/internal/core/search"
)

// Hit is a search result as its reader sees it.
type Hit struct {
	Message *pb.Message
	Channel *pb.Channel
	Where   string
	Excerpt template.HTML
	Time    time.Time
	// URL opens the message where it was written.
	URL string
}

// Hits prepares search results found for terms in team.
func Hits(team *pb.Team, list []*pb.SearchHit, terms []string) []Hit {
	out := make([]Hit, 0, len(list))
	for _, h := range list {
		m, ch := h.GetMessage(), h.GetChannel()
		if m == nil || ch == nil {
			continue
		}
		u := "/t/" + team.Name + "/ch/" + ch.Id
		if m.RootId != "" {
			u += "?thread=" + m.RootId
		} else {
			u += "?msg=" + m.Id
		}
		out = append(out, Hit{
			Message: m, Channel: ch, Where: Title(ch), Excerpt: Highlight(m.Content, terms),
			Time: m.GetCreatedAt().AsTime(), URL: u,
		})
	}
	return out
}

const (
	excerptBytes = 320
	excerptLead  = 100
)

var shortcodeRe = regexp.MustCompile(`:[a-z0-9_+-]{1,32}:`)

// Highlight returns text on one line, shortened around its first match,
// escaped, with the words that match terms marked.
func Highlight(text string, terms []string) template.HTML {
	text = strings.Join(strings.Fields(text), " ")
	text = shortcodeRe.ReplaceAllStringFunc(text, func(code string) string {
		if char, ok := emoji.Char(code[1 : len(code)-1]); ok {
			return char
		}
		return code
	})
	matches := search.Matches(text, terms)
	start, end := 0, len(text)
	if len(text) > excerptBytes {
		if len(matches) > 0 {
			start = max(0, matches[0].Start-excerptLead)
		}
		end = min(len(text), start+excerptBytes)
		for start > 0 && !utf8.RuneStart(text[start]) {
			start--
		}
		for end < len(text) && !utf8.RuneStart(text[end]) {
			end++
		}
	}
	var b strings.Builder
	if start > 0 {
		b.WriteString("…")
	}
	pos := start
	for _, m := range matches {
		if m.Start < pos || m.End > end {
			continue
		}
		b.WriteString(template.HTMLEscapeString(text[pos:m.Start]))
		b.WriteString("<mark>")
		b.WriteString(template.HTMLEscapeString(text[m.Start:m.End]))
		b.WriteString("</mark>")
		pos = m.End
	}
	b.WriteString(template.HTMLEscapeString(text[pos:end]))
	if end < len(text) {
		b.WriteString("…")
	}
	return template.HTML(b.String()) //nolint:gosec // every piece of text is escaped above
}

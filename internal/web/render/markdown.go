package render

import (
	"bytes"
	"html/template"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// newMarkdown renders messages without raw HTML. Images become links, so a
// message cannot make every reader's browser fetch an outside URL.
func newMarkdown() goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(extension.Linkify, extension.Strikethrough, extension.Table, extension.TaskList),
		goldmark.WithParserOptions(parser.WithASTTransformers(util.Prioritized(linkTransformer{}, 100))),
		goldmark.WithRendererOptions(html.WithHardWraps()),
	)
}

type linkTransformer struct{}

func (linkTransformer) Transform(doc *ast.Document, _ text.Reader, _ parser.Context) {
	var images []*ast.Image
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch v := n.(type) {
		case *ast.Link:
			setLinkAttrs(v)
		case *ast.AutoLink:
			v.SetAttributeString("rel", []byte("nofollow noopener noreferrer"))
			v.SetAttributeString("target", []byte("_blank"))
		case *ast.Image:
			images = append(images, v)
		case *ast.Heading:
			// Headings shout in a chat; keep them as strong paragraphs.
			v.Level = max(v.Level, 4)
		}
		return ast.WalkContinue, nil
	})
	for _, img := range images {
		link := ast.NewLink()
		link.Destination = img.Destination
		link.Title = img.Title
		for c := img.FirstChild(); c != nil; {
			next := c.NextSibling()
			link.AppendChild(link, c)
			c = next
		}
		if link.ChildCount() == 0 {
			link.AppendChild(link, ast.NewString(img.Destination))
		}
		setLinkAttrs(link)
		img.Parent().ReplaceChild(img.Parent(), img, link)
	}
}

func setLinkAttrs(l *ast.Link) {
	l.SetAttributeString("rel", []byte("nofollow noopener noreferrer"))
	l.SetAttributeString("target", []byte("_blank"))
}

// Limits on what Markdown may expand into: a table pads every row to its
// header's columns, and each nested container marker opens a block.
const (
	maxTableColumns = 32
	maxTableCells   = 4096
	maxNesting      = 16
	maxExpansion    = 32
)

var tableDelimiter = regexp.MustCompile(`^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$`)

func (r *Renderer) markdown(src string) template.HTML {
	if expands(src) {
		return plain(src)
	}
	var buf bytes.Buffer
	if err := r.md.Convert([]byte(src), &buf); err != nil || buf.Len() > maxExpansion*len(src)+4096 {
		return plain(src)
	}
	return template.HTML(buf.String()) //nolint:gosec // goldmark escapes text and drops raw HTML
}

// plain renders src as escaped text, for content Markdown would blow up.
func plain(src string) template.HTML {
	return template.HTML("<p>" + strings.ReplaceAll(template.HTMLEscapeString(src), "\n", "<br>\n") + "</p>") //nolint:gosec // escaped
}

// expands reports whether src holds a table or nesting past the limits.
func expands(src string) bool {
	lines := strings.Split(src, "\n")
	// blank[i] is the first blank line at or after line i.
	blank := make([]int, len(lines)+1)
	blank[len(lines)] = len(lines)
	for i := len(lines) - 1; i >= 0; i-- {
		blank[i] = blank[i+1]
		if strings.TrimSpace(lines[i]) == "" {
			blank[i] = i
		}
	}
	for i, line := range lines {
		if nesting(line) > maxNesting {
			return true
		}
		if !strings.Contains(line, "-") || !tableDelimiter.MatchString(line) {
			continue
		}
		// A table runs to the next blank line.
		rows := blank[i+1] - i
		cols := strings.Count(strings.Trim(strings.TrimSpace(line), "|"), "|") + 1
		if cols > maxTableColumns || cols*rows > maxTableCells {
			return true
		}
	}
	return false
}

// nesting counts the container markers opening line: quotes and list items.
func nesting(line string) int {
	depth := 0
	for i := 0; i < len(line); {
		switch c := line[i]; {
		case c == ' ' || c == '\t':
			i++
		case c == '>':
			depth++
			i++
		case (c == '-' || c == '*' || c == '+') && i+1 < len(line) && (line[i+1] == ' ' || line[i+1] == '\t'):
			depth++
			i += 2
		case c >= '0' && c <= '9':
			j := i
			for j < len(line) && j-i < 9 && line[j] >= '0' && line[j] <= '9' {
				j++
			}
			if j+1 < len(line) && (line[j] == '.' || line[j] == ')') && (line[j+1] == ' ' || line[j+1] == '\t') {
				depth++
				i = j + 2
				continue
			}
			return depth
		default:
			return depth
		}
	}
	return depth
}

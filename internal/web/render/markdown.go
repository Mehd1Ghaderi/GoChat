package render

import (
	"bytes"
	"html/template"

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

func (r *Renderer) markdown(src string) template.HTML {
	var buf bytes.Buffer
	if err := r.md.Convert([]byte(src), &buf); err != nil {
		return template.HTML(template.HTMLEscapeString(src)) //nolint:gosec // escaped
	}
	return template.HTML(buf.String()) //nolint:gosec // goldmark escapes text and drops raw HTML
}

package render

import (
	"bytes"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"github.com/ssenerg/gochat/internal/core/emoji"
)

// An @name in a message renders as a mention; the same for every reader,
// so bodies stay cacheable. Code keeps its text.
var kindMention = ast.NewNodeKind("Mention")

type mentionNode struct {
	ast.BaseInline
	name []byte
}

func (n *mentionNode) Kind() ast.NodeKind { return kindMention }

func (n *mentionNode) Dump(src []byte, level int) { ast.DumpHelper(n, src, level, nil, nil) }

type mentionParser struct{}

func (mentionParser) Trigger() []byte { return []byte{'@'} }

func (mentionParser) Parse(_ ast.Node, block text.Reader, _ parser.Context) ast.Node {
	if c := block.PrecendingCharacter(); isNameRune(c) || c == '@' {
		return nil
	}
	line, _ := block.PeekLine()
	end := 1
	for end < len(line) && isNameRune(rune(line[end])) {
		end++
	}
	name := bytes.TrimRight(line[1:end], "._-")
	if len(name) == 0 || len(name) > 32 {
		return nil
	}
	block.Advance(1 + len(name))
	return &mentionNode{name: bytes.ToLower(name)}
}

func isNameRune(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-'
}

type mentionRenderer struct{}

func (mentionRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindMention, func(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			_, _ = w.WriteString(`<span class="mention">@`)
			_, _ = w.Write(util.EscapeHTML(n.(*mentionNode).name))
			_, _ = w.WriteString(`</span>`)
		}
		return ast.WalkSkipChildren, nil
	})
}

// A :name: of a known emoji renders as the emoji.
var kindEmoji = ast.NewNodeKind("Emoji")

type emojiNode struct {
	ast.BaseInline
	name, char string
}

func (n *emojiNode) Kind() ast.NodeKind { return kindEmoji }

func (n *emojiNode) Dump(src []byte, level int) { ast.DumpHelper(n, src, level, nil, nil) }

type emojiParser struct{}

func (emojiParser) Trigger() []byte { return []byte{':'} }

func (emojiParser) Parse(_ ast.Node, block text.Reader, _ parser.Context) ast.Node {
	line, _ := block.PeekLine()
	end := bytes.IndexByte(line[1:], ':')
	if end < 1 || end > 32 {
		return nil
	}
	name := string(line[1 : end+1])
	char, ok := emoji.Char(name)
	if !ok {
		return nil
	}
	block.Advance(end + 2)
	return &emojiNode{name: name, char: char}
}

type emojiRenderer struct{}

func (emojiRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindEmoji, func(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			e := n.(*emojiNode)
			_, _ = w.WriteString(`<span class="emoji" title=":`)
			_, _ = w.Write(util.EscapeHTML([]byte(e.name)))
			_, _ = w.WriteString(`:">`)
			_, _ = w.WriteString(e.char)
			_, _ = w.WriteString(`</span>`)
		}
		return ast.WalkSkipChildren, nil
	})
}

// SPDX-License-Identifier: AGPL-3.0-or-later

package markdown

import (
	"bytes"
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// syntaxExt adds math ($...$, $$...$$) and directives (::name{...}, :::name{...}).
type syntaxExt struct{}

func (e *syntaxExt) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(
		parser.WithBlockParsers(
			util.Prioritized(&mathBlockParser{}, 90),
			util.Prioritized(&directiveParser{}, 95),
		),
		parser.WithInlineParsers(util.Prioritized(&mathInlineParser{}, 90)),
	)
}

// ---- nodes ----

var (
	kindFigure    = ast.NewNodeKind("SkimditFigure")
	kindMath      = ast.NewNodeKind("SkimditMath")
	kindDirective = ast.NewNodeKind("SkimditDirective")
)

type figureNode struct{ ast.BaseBlock }

func (n *figureNode) Kind() ast.NodeKind          { return kindFigure }
func (n *figureNode) Dump(src []byte, level int)  { ast.DumpHelper(n, src, level, nil, nil) }
func (n *figureNode) IsRaw() bool                 { return false }
func (n *figureNode) HasBlankPreviousLines() bool { return true }
func (n *figureNode) SetBlankPreviousLines(bool)  {}

type mathNode struct {
	ast.BaseInline
	tex     string
	display bool
	offset  int
}

func (n *mathNode) Kind() ast.NodeKind { return kindMath }
func (n *mathNode) Dump(src []byte, level int) {
	ast.DumpHelper(n, src, level, map[string]string{"tex": n.tex}, nil)
}

type mathBlock struct {
	ast.BaseBlock
	oneLine bool
}

func (n *mathBlock) Kind() ast.NodeKind         { return kindMath }
func (n *mathBlock) Dump(src []byte, level int) { ast.DumpHelper(n, src, level, nil, nil) }
func (n *mathBlock) IsRaw() bool                { return true }

type directiveNode struct {
	ast.BaseBlock
	name   string
	attrs  map[string]string
	leaf   bool
	colons int
	offset int
	closed bool
}

func (n *directiveNode) Kind() ast.NodeKind { return kindDirective }
func (n *directiveNode) Dump(src []byte, level int) {
	ast.DumpHelper(n, src, level, map[string]string{"name": n.name}, nil)
}

// ---- math ----

type mathInlineParser struct{}

func (p *mathInlineParser) Trigger() []byte { return []byte{'$'} }

func (p *mathInlineParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	line, seg := block.PeekLine()
	if len(line) < 3 || line[0] != '$' {
		return nil
	}
	display := line[1] == '$'
	start := 1
	if display {
		start = 2
	}
	if start >= len(line) || line[start] == ' ' || line[start] == '\t' || line[start] == '\n' {
		return nil
	}
	for i := start; i < len(line); i++ {
		c := line[i]
		if c == '\\' {
			i++
			continue
		}
		if c != '$' {
			continue
		}
		if display {
			if i+1 < len(line) && line[i+1] == '$' {
				tex := string(line[start:i])
				block.Advance(i + 2)
				return &mathNode{tex: tex, display: true, offset: seg.Start}
			}
			continue
		}
		prev := line[i-1]
		if prev == ' ' || prev == '\t' {
			continue
		}
		if i+1 < len(line) && line[i+1] >= '0' && line[i+1] <= '9' {
			// "$5 and $10" is money, not math.
			continue
		}
		tex := string(line[start:i])
		block.Advance(i + 1)
		return &mathNode{tex: tex, offset: seg.Start}
	}
	return nil
}

type mathBlockParser struct{}

func (p *mathBlockParser) Trigger() []byte { return []byte{'$'} }

func (p *mathBlockParser) Open(parent ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, seg := reader.PeekLine()
	trimmed := bytes.TrimSpace(line)
	if !bytes.HasPrefix(trimmed, []byte("$$")) {
		return nil, parser.NoChildren
	}
	n := &mathBlock{}
	rest := bytes.TrimSpace(trimmed[2:])
	if len(rest) > 0 {
		// One-line form: $$ x^2 $$
		if !bytes.HasSuffix(rest, []byte("$$")) {
			return nil, parser.NoChildren
		}
		inner := bytes.TrimSuffix(rest, []byte("$$"))
		start := seg.Start + bytes.Index(line, inner)
		n.Lines().Append(text.NewSegment(start, start+len(inner)))
		n.oneLine = true
		reader.Advance(seg.Len())
		return n, parser.NoChildren
	}
	reader.Advance(seg.Len())
	return n, parser.NoChildren
}

func (p *mathBlockParser) Continue(node ast.Node, reader text.Reader, pc parser.Context) parser.State {
	if node.(*mathBlock).oneLine {
		return parser.Close
	}
	line, seg := reader.PeekLine()
	if line == nil {
		return parser.Close
	}
	if string(bytes.TrimSpace(line)) == "$$" {
		reader.Advance(seg.Len())
		return parser.Close
	}
	node.Lines().Append(seg)
	reader.Advance(seg.Len())
	return parser.Continue | parser.NoChildren
}

func (p *mathBlockParser) Close(node ast.Node, reader text.Reader, pc parser.Context) {}
func (p *mathBlockParser) CanInterruptParagraph() bool                                { return true }
func (p *mathBlockParser) CanAcceptIndentedLine() bool                                { return false }

func (nr *nodeRenderer) math(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	st := nr.st
	switch n := node.(type) {
	case *mathNode:
		h, err := st.mathHTML(n.tex, n.display, st.line(n.offset))
		if err == nil {
			_, _ = w.WriteString(st.trust(h))
		}
	case *mathBlock:
		h, err := st.mathHTML(string(rawLines(n, src)), true, st.nodeLine(n))
		if err == nil {
			_, _ = w.WriteString(st.trust(h) + "\n")
		}
	}
	return ast.WalkSkipChildren, nil
}

func (st *state) mathHTML(tex string, display bool, line int) (string, error) {
	st.doc.HasMath = true
	h, err := st.r.katex.Render(tex, display)
	if err != nil {
		st.diag(line, false, "math: %v", err)
		return "", err
	}
	if display {
		return "<div class=\"math math-display\">" + h + "</div>", nil
	}
	return "<span class=\"math math-inline\">" + h + "</span>", nil
}

// ---- directives ----

var directiveRe = regexp.MustCompile(`^(:{2,})([a-z][a-z0-9-]*)(\{.*\})?\s*$`)

type directiveParser struct{}

func (p *directiveParser) Trigger() []byte { return []byte{':'} }

func (p *directiveParser) Open(parent ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, seg := reader.PeekLine()
	m := directiveRe.FindSubmatch(bytes.TrimRight(line, "\n"))
	if m == nil {
		return nil, parser.NoChildren
	}
	colons := len(m[1])
	attrsSrc := ""
	if len(m[3]) > 0 {
		attrsSrc = string(m[3][1 : len(m[3])-1])
	}
	attrs, err := parseAttrs(attrsSrc)
	n := &directiveNode{name: string(m[2]), attrs: attrs, colons: colons, leaf: colons == 2, offset: seg.Start}
	if err != nil {
		n.attrs = map[string]string{"__error": err.Error()}
	}
	reader.Advance(seg.Len())
	if n.leaf {
		return n, parser.NoChildren
	}
	return n, parser.HasChildren
}

func (p *directiveParser) Continue(node ast.Node, reader text.Reader, pc parser.Context) parser.State {
	n := node.(*directiveNode)
	if n.leaf {
		return parser.Close
	}
	line, seg := reader.PeekLine()
	if line == nil {
		return parser.Close
	}
	if t := string(bytes.TrimSpace(line)); t == strings.Repeat(":", n.colons) {
		reader.Advance(seg.Len())
		n.closed = true
		return parser.Close
	}
	return parser.Continue | parser.HasChildren
}

func (p *directiveParser) Close(node ast.Node, reader text.Reader, pc parser.Context) {}
func (p *directiveParser) CanInterruptParagraph() bool                                { return true }
func (p *directiveParser) CanAcceptIndentedLine() bool                                { return false }

func (nr *nodeRenderer) directive(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*directiveNode)
	st := nr.st
	line := st.line(n.offset)
	if e, ok := n.attrs["__error"]; ok {
		if entering {
			st.diag(line, false, "::%s: %s", n.name, e)
		}
		return ast.WalkSkipChildren, nil
	}
	if n.leaf {
		if entering {
			out, err := st.renderDirective(n.name, n.attrs, "", line, true)
			if err == nil {
				_, _ = w.WriteString(st.trust(out) + "\n")
			}
		}
		return ast.WalkSkipChildren, nil
	}
	if entering {
		if !n.closed {
			st.diag(line, false, ":::%s is never closed (end it with a line of %d colons)", n.name, n.colons)
		}
		id := len(st.directives) + 1000000
		for st.directives[id] != nil {
			id++
		}
		st.directives[id] = &directiveCall{name: n.name, attrs: n.attrs, line: line}
		n.SetAttributeString("data-skimdit-id", id)
		_, _ = w.WriteString(st.openToken(id) + "\n")
	} else {
		v, _ := n.AttributeString("data-skimdit-id")
		_, _ = w.WriteString(st.closeToken(v.(int)) + "\n")
	}
	return ast.WalkContinue, nil
}

var attrRe = regexp.MustCompile(`^\s*([a-zA-Z_][a-zA-Z0-9_-]*)(?:\s*=\s*(?:"((?:[^"\\]|\\.)*)"|'([^']*)'|([^\s"'}]+)))?`)

// parseAttrs parses `key="v" key2=v flag`.
func parseAttrs(s string) (map[string]string, error) {
	attrs := map[string]string{}
	s = strings.TrimSpace(s)
	for s != "" {
		m := attrRe.FindStringSubmatchIndex(s)
		if m == nil {
			return attrs, fmt.Errorf("cannot read attributes near %q (use key=\"value\")", s)
		}
		key := s[m[2]:m[3]]
		val := ""
		switch {
		case m[4] >= 0:
			val = strings.ReplaceAll(strings.ReplaceAll(s[m[4]:m[5]], `\"`, `"`), `\\`, `\`)
		case m[6] >= 0:
			val = s[m[6]:m[7]]
		case m[8] >= 0:
			val = s[m[8]:m[9]]
		default:
			val = "true"
		}
		attrs[key] = val
		s = strings.TrimSpace(s[m[1]:])
	}
	return attrs, nil
}

var youtubeIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{6,20}$`)

// renderDirective renders built-in directives and falls back to site ones.
func (st *state) renderDirective(name string, attrs map[string]string, inner string, line int, leaf bool) (string, error) {
	fail := func(format string, args ...any) (string, error) {
		err := fmt.Errorf(format, args...)
		st.diag(line, false, "%s directive: %v", name, err)
		return "", err
	}
	e := html.EscapeString
	switch name {
	case "callout":
		kind := attrs["type"]
		if kind == "" {
			kind = "note"
		}
		switch kind {
		case "note", "tip", "warning", "important":
		default:
			return fail("type must be note, tip, warning or important")
		}
		title := attrs["title"]
		var b strings.Builder
		fmt.Fprintf(&b, "<aside class=\"callout callout-%s\" role=\"note\">", kind)
		if title != "" {
			fmt.Fprintf(&b, "<p class=\"callout-title\">%s</p>", e(title))
		}
		b.WriteString(inner)
		b.WriteString("</aside>")
		return b.String(), nil
	case "youtube":
		id := attrs["id"]
		if !youtubeIDRe.MatchString(id) {
			return fail("needs id=\"<video id>\"")
		}
		title := attrs["title"]
		if title == "" {
			return fail("needs title=\"...\" so readers know what the video is")
		}
		st.doc.HasEmbed = true
		watch := "https://www.youtube.com/watch?v=" + id
		if t := attrs["start"]; t != "" {
			watch += "&t=" + t
		}
		return fmt.Sprintf("<figure class=\"embed embed-video\"><a class=\"embed-facade\" href=\"%s\" data-embed=\"youtube\" data-id=\"%s\" data-start=\"%s\"><span class=\"embed-play\" aria-hidden=\"true\">▶</span><span class=\"embed-title\">%s</span><span class=\"embed-note\">Plays from YouTube when clicked</span></a></figure>",
			e(watch), e(id), e(attrs["start"]), e(title)), nil
	case "video":
		src := attrs["src"]
		if src == "" {
			return fail("needs src=\"file.mp4\"")
		}
		if st.hooks.Link != nil {
			var err error
			if src, err = st.hooks.Link(src); err != nil {
				return fail("%v", err)
			}
		}
		title := attrs["title"]
		if title == "" {
			return fail("needs title=\"...\"")
		}
		poster := ""
		if p := attrs["poster"]; p != "" && st.hooks.Image != nil {
			img, err := st.hooks.Image(p)
			if err != nil {
				return fail("poster: %v", err)
			}
			poster = fmt.Sprintf(" poster=\"%s\"", e(img.Src))
		}
		var b strings.Builder
		fmt.Fprintf(&b, "<figure class=\"embed embed-video\"><video controls preload=\"none\" playsinline%s aria-label=\"%s\"><source src=\"%s\"><a href=\"%s\">Download the video: %s</a></video>", poster, e(title), e(src), e(src), e(title))
		if c := attrs["caption"]; c != "" {
			fmt.Fprintf(&b, "<figcaption>%s</figcaption>", e(c))
		}
		b.WriteString("</figure>")
		return b.String(), nil
	case "figure":
		src := attrs["src"]
		if src == "" {
			return fail("needs src=\"image.jpg\"")
		}
		return st.figureHTML(src, attrs["alt"], attrs["caption"], line), nil
	case "quote":
		// A quoted social post or article, shown as a card that links out.
		url := attrs["url"]
		if !strings.HasPrefix(url, "https://") {
			return fail("needs url=\"https://...\"")
		}
		author := attrs["author"]
		var b strings.Builder
		fmt.Fprintf(&b, "<figure class=\"quote-card\"><blockquote cite=\"%s\">%s</blockquote><figcaption>", e(url), inner)
		if author != "" {
			fmt.Fprintf(&b, "%s, ", e(author))
		}
		label := attrs["source"]
		if label == "" {
			label = "source"
		}
		fmt.Fprintf(&b, "<a href=\"%s\">%s</a>", e(url), e(label))
		if d := attrs["date"]; d != "" {
			fmt.Fprintf(&b, ", %s", e(d))
		}
		b.WriteString("</figcaption></figure>")
		return b.String(), nil
	}
	if st.hooks.Directive != nil {
		out, ok, err := st.hooks.Directive(name, attrs, inner)
		if err != nil {
			return fail("%v", err)
		}
		if ok {
			return out, nil
		}
	}
	return fail("unknown directive (built-ins: callout, youtube, video, figure, quote; or add directives/%s.html to the site)", name)
}

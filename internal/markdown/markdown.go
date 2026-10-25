// SPDX-License-Identifier: AGPL-3.0-or-later

// Package markdown renders post bodies to sanitized HTML.
//
// Author text goes through an allow-list sanitizer. HTML the engine itself
// generates (code, math, images, embeds) is trusted: it is written as an
// opaque token during rendering and swapped in after sanitizing, so the
// sanitizer never has to allow it.
package markdown

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Options controls the whole renderer.
type Options struct {
	RawHTML     bool
	Typographer bool
}

// Image is a processed image ready for a <picture> element.
type Image struct {
	Src     string // fallback URL
	Srcset  string // fallback srcset ("" when there is one size)
	Width   int
	Height  int
	Sources []ImageSource
}

// ImageSource is one <source> of a <picture>.
type ImageSource struct {
	Type   string
	Srcset string
}

// Hooks connect a render to the site: links, images and site directives.
// Every hook is optional.
type Hooks struct {
	// Link rewrites a link destination (for example "post:slug" or a
	// co-located file). It returns the new destination.
	Link func(dest string) (string, error)
	// Image processes an image source and returns how to show it.
	Image func(src string) (*Image, error)
	// Directive renders a site-defined directive. inner is the sanitized
	// HTML of a container directive's body.
	Directive func(name string, attrs map[string]string, inner string) (string, bool, error)
	// RequireAlt makes a missing alt text an error instead of a warning.
	RequireAlt bool
}

// Heading is one entry of the table of contents.
type Heading struct {
	Level int
	ID    string
	Text  string
}

// Diag is a problem found while rendering. Line is relative to the body.
type Diag struct {
	Line    int
	Message string
	Warning bool
}

// Doc is a rendered body.
type Doc struct {
	HTML     string
	Headings []Heading
	// Excerpt is plain text: the part before <!--more-->, or the first paragraph.
	Excerpt  string
	Words    int
	Links    []string
	HasMath  bool
	HasCode  bool
	HasEmbed bool
	Diags    []Diag
}

// Renderer renders Markdown. It is safe for concurrent use.
type Renderer struct {
	opts  Options
	katex *KaTeX
}

// New returns a renderer.
func New(opts Options) *Renderer {
	return &Renderer{opts: opts, katex: NewKaTeX()}
}

// KaTeX returns the renderer's math engine.
func (r *Renderer) KaTeX() *KaTeX { return r.katex }

func (r *Renderer) goldmark(st *state) goldmark.Markdown {
	exts := []goldmark.Extender{
		extension.Strikethrough,
		extension.Linkify,
		extension.TaskList,
		extension.Footnote,
		&syntaxExt{},
	}
	if r.opts.Typographer {
		exts = append(exts, extension.Typographer)
	}
	rendererOpts := []renderer.Option{
		renderer.WithNodeRenderers(util.Prioritized(&nodeRenderer{st: st}, 100)),
	}
	htmlOpts := []renderer.Option{}
	if r.opts.RawHTML {
		htmlOpts = append(htmlOpts, gmhtml.WithUnsafe())
	}
	return goldmark.New(
		goldmark.WithExtensions(exts...),
		goldmark.WithParserOptions(parser.WithAttribute()),
		goldmark.WithRendererOptions(append(htmlOpts, rendererOpts...)...),
		goldmark.WithExtensions(extension.NewTable(extension.WithTableCellAlignMethod(extension.TableCellAlignAttribute))),
	)
}

// Render renders a Markdown body.
func (r *Renderer) Render(src []byte, hooks Hooks) (*Doc, error) {
	st := newState(r, hooks, src)
	md := r.goldmark(st)
	root := md.Parser().Parse(text.NewReader(src))
	doc := &Doc{}
	st.doc = doc
	st.prepare(root)
	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, src, root); err != nil {
		return nil, err
	}
	out, err := st.finish(Sanitize(buf.String()))
	if err != nil {
		return nil, err
	}
	doc.HTML = out
	return doc, nil
}

// RenderHTML sanitizes an HTML body (plain-HTML posts) and extracts what
// lists and feeds need.
func (r *Renderer) RenderHTML(src []byte) *Doc {
	clean := Sanitize(string(src))
	words, excerpt, headings := htmlFacts(clean)
	return &Doc{HTML: clean, Words: words, Excerpt: excerpt, Headings: headings}
}

// state is per-render: trusted fragments, ids and diagnostics.
type state struct {
	r       *Renderer
	hooks   Hooks
	src     []byte
	doc     *Doc
	nonce   string
	trusted []string
	ids     map[string]int
	// directives holds container directives that wrap sanitized inner HTML.
	directives map[int]*directiveCall
	lineStarts []int
}

type directiveCall struct {
	name  string
	attrs map[string]string
	line  int
}

func newState(r *Renderer, hooks Hooks, src []byte) *state {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	st := &state{r: r, hooks: hooks, src: src, nonce: hex.EncodeToString(b), ids: map[string]int{}, directives: map[int]*directiveCall{}}
	st.lineStarts = []int{0}
	for i, c := range src {
		if c == '\n' {
			st.lineStarts = append(st.lineStarts, i+1)
		}
	}
	return st
}

func (st *state) line(offset int) int {
	return sort.Search(len(st.lineStarts), func(i int) bool { return st.lineStarts[i] > offset })
}

func (st *state) nodeLine(n ast.Node) int {
	for c := n; c != nil; c = c.Parent() {
		if c.Type() == ast.TypeBlock {
			if l := c.Lines(); l != nil && l.Len() > 0 {
				return st.line(l.At(0).Start)
			}
		}
		if t, ok := c.(*ast.Text); ok {
			return st.line(t.Segment.Start)
		}
	}
	return 0
}

func (st *state) diag(line int, warning bool, format string, args ...any) {
	st.doc.Diags = append(st.doc.Diags, Diag{Line: line, Message: fmt.Sprintf(format, args...), Warning: warning})
}

// trust stores engine HTML and returns the token that stands for it.
func (st *state) trust(h string) string {
	st.trusted = append(st.trusted, h)
	return st.token(len(st.trusted) - 1)
}

func (st *state) token(i int) string { return "skmd" + st.nonce + "t" + strconv.Itoa(i) + "z" }

func (st *state) openToken(i int) string  { return "skmd" + st.nonce + "o" + strconv.Itoa(i) + "z" }
func (st *state) closeToken(i int) string { return "skmd" + st.nonce + "c" + strconv.Itoa(i) + "z" }

var tokenRe = regexp.MustCompile(`skmd([0-9a-f]{16})([toc])(\d+)z`)

// finish swaps trusted fragments and container directives into sanitized HTML.
func (st *state) finish(s string) (string, error) {
	// Container directives, innermost (highest index) first.
	ids := make([]int, 0, len(st.directives))
	for id := range st.directives {
		ids = append(ids, id)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(ids)))
	for _, id := range ids {
		call := st.directives[id]
		open, close := st.openToken(id), st.closeToken(id)
		i := strings.Index(s, open)
		j := strings.Index(s, close)
		if i < 0 || j < i {
			continue
		}
		inner := strings.TrimSpace(s[i+len(open) : j])
		inner = strings.TrimPrefix(inner, "</p>")
		inner = strings.TrimSuffix(inner, "<p>")
		out, err := st.renderDirective(call.name, call.attrs, inner, call.line, false)
		if err != nil {
			return "", err
		}
		s = trimParagraphAround(s[:i]) + out + trimParagraphAfter(s[j+len(close):])
	}
	return tokenRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := tokenRe.FindStringSubmatch(m)
		if sub[1] != st.nonce || sub[2] != "t" {
			return m
		}
		n, _ := strconv.Atoi(sub[3])
		if n < len(st.trusted) {
			return st.trusted[n]
		}
		return m
	}), nil
}

func trimParagraphAround(s string) string {
	return strings.TrimSuffix(strings.TrimRight(s, "\n"), "<p>")
}
func trimParagraphAfter(s string) string { return strings.TrimPrefix(s, "</p>") }

// prepare walks the tree before rendering: heading ids, figures, links,
// images, excerpt and word count.
func (st *state) prepare(root ast.Node) {
	var words int
	var excerpt strings.Builder
	excerptDone := false
	hasMore := bytes.Contains(st.src, []byte("<!--more-->"))
	var figures []*ast.Paragraph

	_ = ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node := n.(type) {
		case *ast.Heading:
			t := plainText(node, st.src)
			id := ""
			if v, ok := node.AttributeString("id"); ok {
				if b, ok := v.([]byte); ok {
					id = string(b)
				}
			}
			if id == "" {
				id = headingID(t)
			}
			id = st.uniqueID(id)
			node.SetAttributeString("id", []byte(id))
			st.doc.Headings = append(st.doc.Headings, Heading{Level: node.Level, ID: id, Text: t})
		case *ast.Paragraph:
			if hasMore && !excerptDone {
				excerpt.WriteByte(' ')
			}
			if img := loneImage(node, st.src); img != nil {
				figures = append(figures, node)
			}
			if !excerptDone && !hasMore {
				if t := strings.TrimSpace(plainText(node, st.src)); t != "" && node.Parent() == root {
					excerpt.WriteString(t)
					excerptDone = true
				}
			}
		case *ast.HTMLBlock:
			if hasMore && !excerptDone && bytes.Contains(rawLines(node, st.src), []byte("<!--more-->")) {
				excerptDone = true
			}
		case *ast.RawHTML:
			if hasMore && !excerptDone && bytes.Contains(rawSegments(node, st.src), []byte("<!--more-->")) {
				excerptDone = true
			}
		case *ast.Link:
			st.link(node)
		case *ast.Text:
			if isInCode(node) {
				return ast.WalkContinue, nil
			}
			seg := node.Segment.Value(st.src)
			words += countWords(seg)
			if hasMore && !excerptDone && !isInsideHeading(node) {
				excerpt.Write(seg)
				if node.SoftLineBreak() || node.HardLineBreak() {
					excerpt.WriteByte(' ')
				}
			}
		case *ast.String:
			if hasMore && !excerptDone && !isInsideHeading(node) && !isInCode(node) {
				excerpt.WriteString(html.UnescapeString(string(node.Value)))
			}
		case *ast.FencedCodeBlock, *ast.CodeBlock:
			st.doc.HasCode = true
		}
		return ast.WalkContinue, nil
	})
	for _, p := range figures {
		fig := &figureNode{}
		img := loneImage(p, st.src)
		parent := p.Parent()
		parent.ReplaceChild(parent, p, fig)
		fig.AppendChild(fig, img)
	}
	st.doc.Words = words
	st.doc.Excerpt = Truncate(strings.Join(strings.Fields(excerpt.String()), " "), 300)
}

func (st *state) uniqueID(id string) string {
	if id == "" {
		id = "section"
	}
	n := st.ids[id]
	st.ids[id] = n + 1
	if n == 0 {
		return id
	}
	return id + "-" + strconv.Itoa(n+1)
}

func (st *state) link(node *ast.Link) {
	dest := string(node.Destination)
	if st.hooks.Link != nil {
		nd, err := st.hooks.Link(dest)
		if err != nil {
			st.diag(st.nodeLine(node), false, "%v", err)
		} else {
			dest = nd
			node.Destination = []byte(nd)
		}
	}
	st.doc.Links = append(st.doc.Links, dest)
}

// headingID makes a stable id from heading text: "Why Go?" -> "why-go".
func headingID(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			dash = false
		case r == '-' || r == '_' || unicode.IsSpace(r):
			if b.Len() > 0 && !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func loneImage(p *ast.Paragraph, src []byte) *ast.Image {
	var img *ast.Image
	for c := p.FirstChild(); c != nil; c = c.NextSibling() {
		switch node := c.(type) {
		case *ast.Image:
			if img != nil {
				return nil
			}
			img = node
		case *ast.Text:
			seg := node.Segment
			if len(bytes.TrimSpace(seg.Value(src))) != 0 {
				return nil
			}
		default:
			return nil
		}
	}
	return img
}

func isInCode(n ast.Node) bool {
	for p := n.Parent(); p != nil; p = p.Parent() {
		if p.Kind() == ast.KindCodeSpan {
			return true
		}
	}
	return false
}

func isInsideHeading(n ast.Node) bool {
	for p := n.Parent(); p != nil; p = p.Parent() {
		if p.Kind() == ast.KindHeading {
			return true
		}
	}
	return false
}

// plainText is the visible text of a node and its children.
func plainText(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := c.(type) {
		case *ast.Text:
			b.Write(t.Segment.Value(src))
			if t.SoftLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.WriteString(html.UnescapeString(string(t.Value)))
		case *mathNode:
			b.WriteString(t.tex)
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

func rawLines(n ast.Node, src []byte) []byte {
	var b bytes.Buffer
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		b.Write(seg.Value(src))
	}
	return b.Bytes()
}

func rawSegments(n *ast.RawHTML, src []byte) []byte {
	var b bytes.Buffer
	for i := 0; i < n.Segments.Len(); i++ {
		seg := n.Segments.At(i)
		b.Write(seg.Value(src))
	}
	return b.Bytes()
}

func countWords(b []byte) int {
	n := 0
	in := false
	for _, r := range string(b) {
		if unicode.IsSpace(r) {
			in = false
		} else if !in {
			in = true
			n++
		}
	}
	return n
}

// Truncate shortens s to at most max runes on a word boundary, adding "…".
func Truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	cut := max
	for cut > max/2 && !unicode.IsSpace(r[cut]) {
		cut--
	}
	return strings.TrimRight(string(r[:cut]), " ,;:.-") + "…"
}

// nodeRenderer renders the engine's own node kinds and overrides headings,
// images and code blocks.
type nodeRenderer struct{ st *state }

func (nr *nodeRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindHeading, nr.heading)
	reg.Register(ast.KindImage, nr.image)
	reg.Register(ast.KindFencedCodeBlock, nr.code)
	reg.Register(ast.KindCodeBlock, nr.code)
	reg.Register(kindFigure, nr.figure)
	reg.Register(kindMath, nr.math)
	reg.Register(kindDirective, nr.directive)
	reg.Register(extast.KindFootnoteList, nr.footnoteList)
}

func (nr *nodeRenderer) heading(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*ast.Heading)
	id, _ := n.AttributeString("id")
	idStr := ""
	if b, ok := id.([]byte); ok {
		idStr = string(b)
	}
	if entering {
		fmt.Fprintf(w, "<h%d id=\"%s\">", n.Level, html.EscapeString(idStr))
	} else {
		fmt.Fprintf(w, " <a class=\"heading-anchor\" href=\"#%s\" aria-hidden=\"true\" tabindex=\"-1\">#</a></h%d>\n", html.EscapeString(idStr), n.Level)
	}
	return ast.WalkContinue, nil
}

func (nr *nodeRenderer) footnoteList(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("<section class=\"footnotes\" aria-label=\"Footnotes\">\n<ol>\n")
	} else {
		_, _ = w.WriteString("</ol>\n</section>\n")
	}
	return ast.WalkContinue, nil
}

func (nr *nodeRenderer) image(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.Image)
	alt := plainText(n, src)
	_, _ = w.WriteString(nr.st.trust(nr.st.picture(string(n.Destination), alt, nr.st.nodeLine(n), false)))
	return ast.WalkSkipChildren, nil
}

func (nr *nodeRenderer) figure(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	img := node.FirstChild().(*ast.Image)
	alt := plainText(img, src)
	caption := string(img.Title)
	_, _ = w.WriteString(nr.st.trust(nr.st.figureHTML(string(img.Destination), alt, caption, nr.st.nodeLine(img))))
	_ = w.WriteByte('\n')
	return ast.WalkSkipChildren, nil
}

func (st *state) figureHTML(dest, alt, caption string, line int) string {
	var b strings.Builder
	b.WriteString("<figure>")
	b.WriteString(st.picture(dest, alt, line, true))
	if caption != "" {
		b.WriteString("<figcaption>" + html.EscapeString(caption) + "</figcaption>")
	}
	b.WriteString("</figure>")
	return b.String()
}

// picture renders an image through the Image hook.
func (st *state) picture(dest, alt string, line int, block bool) string {
	if strings.TrimSpace(alt) == "" {
		st.diag(line, !st.hooks.RequireAlt, "image %q has no alt text (describe it for readers who cannot see it; use alt=\"\" only through raw HTML for pure decoration)", dest)
	}
	if strings.HasPrefix(dest, "http://") || strings.HasPrefix(dest, "https://") || strings.HasPrefix(dest, "//") {
		st.diag(line, false, "image %q is remote: download it into the post folder so readers make no third-party requests", dest)
		return "<img src=\"\" alt=\"" + html.EscapeString(alt) + "\">"
	}
	img := &Image{Src: dest}
	if st.hooks.Image != nil {
		got, err := st.hooks.Image(dest)
		if err != nil {
			st.diag(line, false, "image %q: %v", dest, err)
			return "<img src=\"\" alt=\"" + html.EscapeString(alt) + "\">"
		}
		img = got
	}
	return PictureHTML(img, alt, block)
}

// PictureHTML is the markup for a processed image.
func PictureHTML(img *Image, alt string, block bool) string {
	var b strings.Builder
	sizes := "(max-width: 760px) 100vw, 760px"
	if len(img.Sources) > 0 {
		b.WriteString("<picture>")
		for _, s := range img.Sources {
			fmt.Fprintf(&b, "<source type=\"%s\" srcset=\"%s\" sizes=\"%s\">", html.EscapeString(s.Type), html.EscapeString(s.Srcset), sizes)
		}
	}
	fmt.Fprintf(&b, "<img src=\"%s\" alt=\"%s\"", html.EscapeString(img.Src), html.EscapeString(alt))
	if img.Srcset != "" {
		fmt.Fprintf(&b, " srcset=\"%s\" sizes=\"%s\"", html.EscapeString(img.Srcset), sizes)
	}
	if img.Width > 0 && img.Height > 0 {
		fmt.Fprintf(&b, " width=\"%d\" height=\"%d\"", img.Width, img.Height)
	}
	b.WriteString(" loading=\"lazy\" decoding=\"async\">")
	if len(img.Sources) > 0 {
		b.WriteString("</picture>")
	}
	return b.String()
}

func (nr *nodeRenderer) code(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	var info string
	if f, ok := node.(*ast.FencedCodeBlock); ok && f.Info != nil {
		info = string(f.Info.Segment.Value(src))
	}
	body := string(rawLines(node, src))
	lang, attrs := parseInfo(info)
	st := nr.st
	if lang == "math" {
		h, err := st.mathHTML(body, true, st.nodeLine(node))
		if err == nil {
			_, _ = w.WriteString(st.trust(h) + "\n")
		}
		return ast.WalkSkipChildren, nil
	}
	h, err := highlight(body, lang, attrs)
	if err != nil {
		st.diag(st.nodeLine(node), false, "code block: %v", err)
		h = "<pre><code>" + html.EscapeString(body) + "</code></pre>"
	}
	_, _ = w.WriteString(st.trust(h) + "\n")
	return ast.WalkSkipChildren, nil
}

// parseInfo splits a fence info string: `go {title="main.go" hl="3-5"}`.
func parseInfo(info string) (string, map[string]string) {
	info = strings.TrimSpace(info)
	lang := info
	rest := ""
	if i := strings.IndexAny(info, " \t{"); i >= 0 {
		lang, rest = info[:i], strings.TrimSpace(info[i:])
	}
	rest = strings.TrimSuffix(strings.TrimPrefix(rest, "{"), "}")
	attrs, _ := parseAttrs(rest)
	return strings.ToLower(lang), attrs
}

// Sanitize cleans author HTML with the content allow-list.
func Sanitize(s string) string { return contentPolicy().Sanitize(s) }

// RenderComment renders reader-written Markdown (comments): no raw HTML,
// no images, no directives, links marked nofollow ugc.
func RenderComment(src string) string {
	md := goldmark.New(
		goldmark.WithExtensions(extension.Strikethrough, extension.Linkify),
		goldmark.WithRendererOptions(gmhtml.WithHardWraps()),
	)
	var buf bytes.Buffer
	if err := md.Convert([]byte(src), &buf); err != nil {
		return ""
	}
	return CommentPolicy().Sanitize(buf.String())
}

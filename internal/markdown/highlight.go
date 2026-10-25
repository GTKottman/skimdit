// SPDX-License-Identifier: AGPL-3.0-or-later

package markdown

import (
	"fmt"
	"html"
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// highlight renders a code block at build time with CSS classes (no inline
// styles, no client JavaScript). attrs: title, hl ("3-5,8"), linenos, start.
func highlight(code, lang string, attrs map[string]string) (string, error) {
	lexer := lexers.Get(lang)
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)
	opts := []chromahtml.Option{
		chromahtml.WithClasses(true),
		chromahtml.TabWidth(4),
		chromahtml.PreventSurroundingPre(false),
	}
	start := 1
	if s := attrs["start"]; s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			return "", fmt.Errorf("start=%q must be a positive line number", s)
		}
		start = n
		opts = append(opts, chromahtml.BaseLineNumber(n))
	}
	if attrs["linenos"] == "true" {
		opts = append(opts, chromahtml.WithLineNumbers(true), chromahtml.LineNumbersInTable(false))
	}
	if hl := attrs["hl"]; hl != "" {
		ranges, err := parseRanges(hl, start)
		if err != nil {
			return "", err
		}
		opts = append(opts, chromahtml.HighlightLines(ranges))
	}
	it, err := lexer.Tokenise(nil, code)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := chromahtml.New(opts...).Format(&b, styles.Fallback, it); err != nil {
		return "", err
	}
	body := b.String()
	label := lang
	if label == "" || lexers.Get(lang) == nil {
		label = "text"
	}
	var out strings.Builder
	out.WriteString("<figure class=\"code\" data-lang=\"" + html.EscapeString(label) + "\">")
	out.WriteString("<figcaption class=\"code-head\"><span class=\"code-lang\">" + html.EscapeString(label) + "</span>")
	if t := attrs["title"]; t != "" {
		out.WriteString("<span class=\"code-title\">" + html.EscapeString(t) + "</span>")
	}
	out.WriteString("</figcaption>")
	out.WriteString(body)
	out.WriteString("</figure>")
	return out.String(), nil
}

func parseRanges(s string, start int) ([][2]int, error) {
	var out [][2]int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		a, b, found := strings.Cut(part, "-")
		x, err1 := strconv.Atoi(strings.TrimSpace(a))
		y := x
		var err2 error
		if found {
			y, err2 = strconv.Atoi(strings.TrimSpace(b))
		}
		if err1 != nil || err2 != nil || x < 1 || y < x {
			return nil, fmt.Errorf("hl=%q: use line numbers like \"3-5,8\"", s)
		}
		// chroma counts from the base line number.
		out = append(out, [2]int{x + start - 1, y + start - 1})
	}
	return out, nil
}

// HighlightCSS returns the code colour CSS for a chroma style, scoped under
// selector (for example ":root[data-theme=light]").
func HighlightCSS(style, selector string) (string, error) {
	s := styles.Get(style)
	var b strings.Builder
	f := chromahtml.New(chromahtml.WithClasses(true))
	if err := f.WriteCSS(&b, s); err != nil {
		return "", err
	}
	if selector == "" {
		return b.String(), nil
	}
	var out strings.Builder
	for _, line := range strings.Split(b.String(), "\n") {
		if i := strings.Index(line, ".chroma"); i >= 0 {
			line = line[:i] + selector + " " + line[i:]
		}
		out.WriteString(line + "\n")
	}
	return out.String(), nil
}

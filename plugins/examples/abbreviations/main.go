// SPDX-License-Identifier: AGPL-3.0-or-later

// Command abbreviations is an example Skimdit plugin. It explains
// abbreviations in posts: each word listed in its config is wrapped in
// <abbr title="..."> the first time it appears in a paragraph.
//
//	[[plugins]]
//	path = "plugins/abbreviations.wasm"
//	config = { terms = { HTML = "HyperText Markup Language", CSS = "Cascading Style Sheets" } }
package main

import (
	"html"
	"regexp"
	"sort"
	"strings"

	"github.com/GTKottman/skimdit/plugin"
)

type config struct {
	Terms map[string]string `json:"terms"`
}

func main() {
	plugin.Run(func(req *plugin.Request) (any, error) {
		switch req.Hook {
		case "manifest":
			return plugin.Manifest{Name: "abbreviations", Version: "1.0.0", EngineAPI: plugin.API, Hooks: []string{"post_render"}}, nil
		case "post_render":
			var cfg config
			if err := req.DecodeConfig(&cfg); err != nil {
				return nil, err
			}
			var post plugin.Post
			if err := req.Decode(&post); err != nil {
				return nil, err
			}
			return plugin.HTMLResult{HTML: expand(post.HTML, cfg.Terms)}, nil
		}
		return plugin.HTMLResult{}, nil
	})
}

var paraRe = regexp.MustCompile(`(?s)<p>.*?</p>`)

func expand(h string, terms map[string]string) string {
	keys := make([]string, 0, len(terms))
	for k := range terms {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	return paraRe.ReplaceAllStringFunc(h, func(p string) string {
		for _, k := range keys {
			re := regexp.MustCompile(`\b` + regexp.QuoteMeta(k) + `\b`)
			done := false
			p = splitText(p, func(text string) string {
				if done {
					return text
				}
				if loc := re.FindStringIndex(text); loc != nil {
					done = true
					return text[:loc[0]] + `<abbr title="` + html.EscapeString(terms[k]) + `">` + k + `</abbr>` + text[loc[1]:]
				}
				return text
			})
		}
		return p
	})
}

// splitText applies f to text outside tags only.
func splitText(s string, f func(string) string) string {
	var b strings.Builder
	for len(s) > 0 {
		i := strings.IndexByte(s, '<')
		if i < 0 {
			b.WriteString(f(s))
			break
		}
		b.WriteString(f(s[:i]))
		j := strings.IndexByte(s[i:], '>')
		if j < 0 {
			b.WriteString(s[i:])
			break
		}
		b.WriteString(s[i : i+j+1])
		s = s[i+j+1:]
	}
	return b.String()
}

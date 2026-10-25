// SPDX-License-Identifier: AGPL-3.0-or-later

// Command reading-list is an example Skimdit plugin. It adds a ::books
// directive that shows the books from its config, and writes the list as
// books.json on every build.
//
//	[[plugins]]
//	path = "plugins/reading-list.wasm"
//	config = { books = [ { title = "Middlemarch", author = "George Eliot", year = 1871 } ] }
//
// Then, in a post: ::books{year=1871}
package main

import (
	"encoding/json"
	"fmt"
	"html"
	"strconv"
	"strings"

	"github.com/GTKottman/skimdit/plugin"
)

type book struct {
	Title  string `json:"title"`
	Author string `json:"author"`
	Year   int    `json:"year"`
	URL    string `json:"url"`
}

type config struct {
	Books []book `json:"books"`
}

func main() {
	plugin.Run(func(req *plugin.Request) (any, error) {
		var cfg config
		if err := req.DecodeConfig(&cfg); err != nil {
			return nil, err
		}
		switch req.Hook {
		case "manifest":
			return plugin.Manifest{Name: "reading-list", Version: "1.0.0", EngineAPI: plugin.API, Hooks: []string{"directive", "build_done"}, Directives: []string{"books"}}, nil
		case "directive":
			var d plugin.Directive
			if err := req.Decode(&d); err != nil {
				return nil, err
			}
			year, _ := strconv.Atoi(d.Attrs["year"])
			var b strings.Builder
			b.WriteString(`<ul class="reading-list">`)
			for _, bk := range cfg.Books {
				if year != 0 && bk.Year != year {
					continue
				}
				title := "<cite>" + html.EscapeString(bk.Title) + "</cite>"
				if strings.HasPrefix(bk.URL, "https://") {
					title = `<a href="` + html.EscapeString(bk.URL) + `">` + title + `</a>`
				}
				fmt.Fprintf(&b, "<li>%s by %s (%d)</li>", title, html.EscapeString(bk.Author), bk.Year)
			}
			b.WriteString("</ul>")
			return plugin.HTMLResult{HTML: b.String()}, nil
		case "build_done":
			data, _ := json.MarshalIndent(cfg.Books, "", "  ")
			return plugin.FilesResult{Files: []plugin.File{{Path: "books.json", Content: string(data) + "\n"}}}, nil
		}
		return plugin.HTMLResult{}, nil
	})
}

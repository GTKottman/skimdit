// SPDX-License-Identifier: AGPL-3.0-or-later

// A plugin that misbehaves in every way the sandbox must stop.
package main

import (
	"os"

	"github.com/GTKottman/skimdit/plugin"
)

func main() {
	plugin.Run(func(req *plugin.Request) (any, error) {
		var d plugin.Directive
		_ = req.Decode(&d)
		switch req.Hook {
		case "manifest":
			return plugin.Manifest{Name: "hostile", Version: "1", EngineAPI: plugin.API, Hooks: []string{"directive", "post_render", "build_done"}, Directives: []string{"loop", "hog", "readfile", "inject"}}, nil
		case "post_render":
			return plugin.HTMLResult{HTML: `<p onclick="x()">hi</p><script>alert(1)</script><img src=x onerror=y>`}, nil
		case "build_done":
			return plugin.FilesResult{Files: []plugin.File{{Path: "evil.html", Content: "<script>x</script>"}}}, nil
		}
		switch d.Name {
		case "loop":
			for {
			}
		case "hog":
			var keep [][]byte
			for {
				keep = append(keep, make([]byte, 1<<20))
			}
		case "readfile":
			b, err := os.ReadFile("/etc/passwd")
			if err != nil {
				return plugin.HTMLResult{HTML: "<p>no file access: " + err.Error() + "</p>"}, nil
			}
			return plugin.HTMLResult{HTML: string(b)}, nil
		}
		return plugin.HTMLResult{HTML: `<p>ok</p>`}, nil
	})
}

# Plugins

A plugin is a WebAssembly program (WASI). Skimdit runs it once per call: it reads a JSON request on standard input and writes a JSON answer on standard output. Any language that compiles to WASI works; Go plugins can use the SDK in `plugin/`.

```toml
[[plugins]]
path = "plugins/abbreviations.wasm"
config = { terms = { HTML = "HyperText Markup Language" } }
```

## The sandbox

A plugin cannot read or write files, open network connections or see environment variables. Each call gets at most 32 MB of memory and 2 seconds, and may answer with at most 4 MB. Whatever HTML a plugin returns goes through the same allow-list as your posts: it can change text and markup, never add scripts, styles or event handlers. Plugins may add output files, but not `.html`, `.js` or `.svg` files, and nothing under `/assets`, `/admin` or `/api`. There is no plugin registry and nothing installs itself: you add the `.wasm` file and the config line.

## The protocol (engine_api 1)

Request:

```json
{"hook": "post_render", "api": 1, "config": { "...": "the plugin's config table" }, "data": { "...": "depends on the hook" }}
```

| Hook | `data` | Answer |
|---|---|---|
| `manifest` | none | `{"name", "version", "engine_api": 1, "hooks": [...], "directives": [...]}` |
| `post_render` | `{"slug", "title", "url", "tags", "html"}` | `{"html"}` (the post's new HTML) |
| `directive` | `{"name", "attrs", "inner"}` | `{"html"}` for `::name{...}` in posts |
| `build_done` | `{"site", "url", "posts", "files"}` | `{"files": [{"path", "content"}]}` extra output files |
| `route` | `{"method", "path", "query", "body"}` | `{"status", "content_type", "body"}` for `/api/plugins/<name>/<path>` (server only; JSON, text, CSV or XML) |

An answer of `{"error": "..."}` stops the build with that message.

## Example plugins

`plugins/examples/` holds two, built with `plugins/examples/build.sh`:

- **abbreviations** (`post_render`): wraps the terms from its config in `<abbr title>` the first time they appear in a paragraph.
- **reading-list** (`directive`, `build_done`): `::books{year=1871}` lists the books from its config; every build writes `books.json`.

A minimal Go plugin:

```go
package main

import "github.com/GTKottman/skimdit/plugin"

func main() {
	plugin.Run(func(req *plugin.Request) (any, error) {
		switch req.Hook {
		case "manifest":
			return plugin.Manifest{Name: "shout", Version: "1.0.0", EngineAPI: plugin.API, Hooks: []string{"directive"}, Directives: []string{"shout"}}, nil
		case "directive":
			var d plugin.Directive
			req.Decode(&d)
			return plugin.HTMLResult{HTML: "<p><strong>" + d.Attrs["text"] + "</strong></p>"}, nil
		}
		return nil, nil
	})
}
```

```sh
GOOS=wasip1 GOARCH=wasm go build -o shout.wasm .
```

The plugin protocol is versioned with `engine_api`, like themes: a breaking change bumps it, and the previous version keeps working for 12 months with a warning.

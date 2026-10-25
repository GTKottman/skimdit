# Command line

Every command takes `--site DIR` (default: the current folder).

| Command | What it does |
|---|---|
| `skimdit init [dir] [--title T]` | Create a new site with a sample post |
| `skimdit new "Title" [--page]` | Create a draft post or page |
| `skimdit dev [--addr 127.0.0.1:1414] [--no-drafts]` | Preview with live reload; drafts shown; build errors appear in the browser |
| `skimdit build [--out DIR] [--drafts]` | Build into `dist/` (replaced atomically: readers never see a half-built site) |
| `skimdit check` | Build into a scratch folder and report problems; exits 1 on errors |
| `skimdit version` | Print the version and `engine_api` |

`skimdit check` reports:

- frontmatter and config errors, with file and line
- links to posts, pages, files or anchors that do not exist
- images without alt text, remote images, broken math
- drafts committed to git
- pages over the weight budget (`check.page_budget_kb`, `check.js_budget_kb`)
- builds slower than `check.build_budget_seconds`

Run it in CI before every deploy.

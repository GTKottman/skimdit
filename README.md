# Skimdit

A blog engine for one author, written from scratch in Go.

Skimdit turns a folder of Markdown files into a fast, static, accessible site, and runs the parts a real blog needs next to it: a web editor, comments, a newsletter, privacy-friendly stats, webmentions and ActivityPub. One binary, no external database, no third-party scripts.

Its first site is [deeperthinking.xyz](https://deeperthinking.xyz).

> **Status: feature-complete, being proven on deeperthinking.xyz before the first stable release.**

## Principles

- **Your words stay yours.** Posts are plain Markdown files with YAML frontmatter in a git repo. Export is a folder.
- **Readers get HTML.** Every page is pre-built and works without JavaScript. No cookies, no trackers, no third-party requests.
- **Boring formats, long life.** Markdown, TOML, HTML, Atom/RSS. Old links never break: slugs are frozen and every URL change gets a redirect.
- **Built to last ten years.** A single Go binary with few dependencies, deterministic builds, and golden-file tests on every release.

## Non-goals

Skimdit will not become any of these. Issues asking for them will be closed.

- Multi-user or multi-author publishing
- A page builder or visual layout editor
- E-commerce, paid memberships or paywalls
- A hosted service, theme marketplace or plugin registry
- Client-side rendered pages

## Roadmap

Everything below ships in 1.0. Each milestone must pass its checks before the next one starts.

| Milestone | Scope |
|---|---|
| M0 | Repository, licence, CI on Linux/macOS/Windows, release pipeline |
| M1 | Content core: Markdown, frontmatter schema, images, highlighting, math, diagrams, `build`, `dev`, `check` |
| M2 | Default dark theme, every page type, accessibility and size budgets, theme contract and overrides |
| M3 | Feeds, sitemap, robots.txt, meta tags, JSON-LD, social cards, redirect ledger, search |
| M4 | `serve`, SQLite, admin login, web editor, drafts, publish-to-git, scheduling |
| M5 | Comments with moderation, reply by email, webmentions |
| M6 | Newsletter over SMTP, cookieless stats |
| M7 | WebFinger, ActivityPub, Mastodon and Bluesky cross-posting |
| M8 | Sandboxed WebAssembly plugins, WordPress/Ghost/Markdown import, export |
| M9 | Security review, fuzzing, load test, restore drill, full documentation |
| M10 | 1.0 release |

## What it does

- **Writing:** Markdown with footnotes, tables, math (rendered at build time), highlighted code, callouts and safe embeds; a web editor with live preview, autosave, versions, image uploads and scheduling; or plain files and `git push`.
- **Reading:** a fast, accessible, dark-first theme (light mode included); search, tags, series, archives, related posts and backlinks; no cookies, no trackers, no JavaScript required.
- **Reaching readers:** Atom, RSS and JSON feeds, sitemap, social cards, structured data; a double opt-in newsletter; webmentions; ActivityPub; Mastodon and Bluesky cross-posting.
- **Hearing back:** moderated comments with spam defences that do not track anyone; replies by email; cookieless stats.
- **Lasting:** old URLs never break (every move is redirected); deterministic builds; continuous database backups and a tested restore; import from WordPress, Ghost, Hugo and Jekyll; export to a zip that is a working site.
- **Extending:** themes with a stable, versioned contract and per-file overrides; sandboxed WebAssembly plugins.

## Quick start

```sh
go install github.com/GTKottman/skimdit/cmd/skimdit@latest
skimdit init my-blog && cd my-blog
skimdit new "My first post"
skimdit dev            # http://127.0.0.1:1414, reloads as you write
skimdit check          # links, images, drafts, feeds, page weight
skimdit build          # static site in dist/
skimdit serve          # the full server: editor, comments, newsletter...
```

Requires Go 1.27 or newer to build. Release binaries and a container image (`ghcr.io/gtkottman/skimdit`) come with each tagged release.

## Documentation

[Writing](docs/WRITING.md) · [CLI](docs/CLI.md) · [Configuration](docs/CONFIG.md) · [Themes](docs/THEMES.md) · [Feeds and SEO](docs/DISTRIBUTION.md) · [Server](docs/SERVER.md) · [Comments and webmentions](docs/REPLIES.md) · [Newsletter and stats](docs/NEWSLETTER.md) · [Fediverse](docs/FEDIVERSE.md) · [Plugins](docs/PLUGINS.md) · [Import and export](docs/IMPORT.md) · [Deploying](docs/DEPLOY.md)

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Skimdit is maintained by one person, so replies can take about a week.

## Security

Please report vulnerabilities privately. See [SECURITY.md](SECURITY.md).

## Licence

Skimdit is licensed under the [GNU Affero General Public License v3.0 or later](LICENSE).

Themes and plugins that only use Skimdit's documented theme and plugin interfaces are separate works and may use any licence. Your posts are yours; the engine's licence does not apply to the content you publish with it.

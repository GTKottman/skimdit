# Configuration reference

A site is configured by `config.toml` in its root. Only `site.title` and `site.url` are required. Unknown keys are errors, so a typo never passes silently. Secrets (passwords, tokens) never go in this file; they come from environment variables.

## [site]

| Key | Default | Meaning |
|---|---|---|
| `title` | (required) | Site name |
| `url` | (required) | Public address, e.g. `"https://deeperthinking.xyz"`. A path (`/blog/`) becomes the base path |
| `description` | `""` | Used on the home page and in feeds |
| `language` | `"en"` | Page language; also picks `locales/<language>.toml` |
| `timezone` | `"UTC"` | IANA zone for dates written without one, e.g. `"America/Chicago"` |
| `base_path` | from `url` | Serve the site under a subfolder (staging, previews) |
| `intro` | `""` | Markdown shown at the top of the home page |
| `posts_per_page` | `10` | Posts per list page |
| `copyright` | `© <year> <author>` | Footer text |

## [author]

`name`, `email` (enables "Reply by email"), `url`, `avatar`.

## [permalinks]

Patterns with `:slug`, `:year`, `:month`, `:day`, ending in `/` (pretty URLs) or `.html`.

| Key | Default |
|---|---|
| `post` | `"/posts/:slug/"` |
| `page` | `"/:slug/"` |
| `tag` | `"/tags/:slug/"` |
| `series` | `"/series/:slug/"` |

## [build]

`content` (`"content"`), `output` (`"dist"`), `cache` (`".skimdit/cache"`; safe to delete).

## [markdown]

| Key | Default | Meaning |
|---|---|---|
| `raw_html` | `true` | Allow raw HTML in Markdown (always sanitized) |
| `typographer` | `true` | Curly quotes, dashes and ellipses |
| `toc_min_headings` | `3` | Headings needed for an automatic table of contents |

## [images]

| Key | Default | Meaning |
|---|---|---|
| `widths` | `[480, 800, 1200, 1600]` | Responsive widths (never upscaled) |
| `formats` | `["avif", "webp"]` | Modern formats, plus a JPEG/PNG fallback |
| `quality` | `70` | 1-100 |
| `require_alt` | `true` | Missing alt text is an error (otherwise a warning) |
| `max_file_size` | `26214400` | Largest source image in bytes |

## [theme]

`name` (`"default"`, or a folder path relative to the site) and `accent` (a CSS colour such as `"#6ea8fe"`). Any theme file can be overridden by putting a file at the same path under `theme/` in the site. See [THEMES.md](THEMES.md).

## [taxonomies]

`tag_pages` (`true`), `noindex_tags` (`false`; set `true` if tag pages are thin), `archive_months` (`true`), `archive_days` (`false`).

## [check]

`page_budget_kb` (`30`, compressed HTML+CSS per page), `js_budget_kb` (`100`), `build_budget_seconds` (`0` = off).

## [feeds]

`atom`, `rss`, `json` (all `true`), `per_tag` and `per_series` (`true`), `limit` (`20`), `full_content` (`true`).

## [robots]

`ai`: `"allow"` (default: every crawler, AI included), `"block-training"` (asks AI training crawlers to stay out, allows search) or `"block-all"`. `disallow`: extra paths every crawler is asked to skip. robots.txt is a request, not enforcement.

## [social]

`cards` (`true`: generate a sharing image per post), `image` (default sharing image, a site path), `links` (profile URLs for the footer, `rel=me` and structured data).

## [search]

`enabled` (`true`).

## [server] (for `skimdit serve`)

| Key | Default | Meaning |
|---|---|---|
| `listen` | `":8080"` | Address to listen on |
| `data` | `".skimdit/data"` | Folder for the database and uploads; back it up |
| `repo` | the site root | Git checkout the server builds from |
| `push` | `false` | Push publish commits to the git remote |
| `branch` | `"main"` | Branch for publish commits |
| `trust_proxy` | `false` | Read the client IP from `X-Forwarded-For` (behind Coolify/Traefik) |

Environment: `SKIMDIT_SECRET` (session and token signing key, at least 32 characters).

## [comments]

`enabled` (`true`), `default` (`true`: posts take comments unless their frontmatter says `comments: false`), `reply_by_email` (`true`, needs `author.email`), `max_links` (`2`), `max_length` (`5000`), `pow_bits` (`18`, proof-of-work difficulty).

## [newsletter]

| Key | Default | Meaning |
|---|---|---|
| `enabled` | `false` | Signup form and send-on-publish |
| `from` | | Sender, e.g. `"Deeper Thinking <letters@deeperthinking.xyz>"` |
| `smtp_host`, `smtp_port`, `smtp_user` | `465` | Mail server; password in `SKIMDIT_SMTP_PASSWORD` |
| `smtp_security` | `"tls"` | `tls`, `starttls` or `none` |
| `bounce_imap`, `bounce_user` | | Mailbox that receives bounces (`host:993`); password in `SKIMDIT_IMAP_PASSWORD` |
| `content` | `"full"` | `full` post or `excerpt` with a link |
| `batch_size`, `batch_pause_seconds` | `20`, `30` | Throttle |

## [stats]

`enabled` (`true`): cookieless page-view counts, kept as daily totals only.

## [webmention]

`send` and `receive` (both `true`).

## [fediverse]

`enabled` (`false`), `handle` (the user part of `@handle@domain`), `summary` (profile text).

## [crosspost.mastodon] and [crosspost.bluesky]

`enabled` (`false`). Mastodon: `instance` (`"https://mastodon.social"`), token in `SKIMDIT_MASTODON_TOKEN`. Bluesky: `handle`, `pds` (`"https://bsky.social"`), app password in `SKIMDIT_BLUESKY_PASSWORD`.

## [[plugins]]

```toml
[[plugins]]
path = "plugins/reading-list.wasm"
config = { shelf = "2026" }
```

See [PLUGINS.md](PLUGINS.md).

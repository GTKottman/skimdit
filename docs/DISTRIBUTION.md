# Feeds, search engines and sharing

Every build writes these next to the pages:

| File | What it is |
|---|---|
| `feed.xml` | Atom feed (the main one), full posts, with a stylesheet so it reads well in a browser |
| `rss.xml`, `feed.json` | RSS 2.0 and JSON Feed 1.1 for older and newer readers |
| `tags/<tag>/feed.xml`, `series/<series>/feed.xml` | one feed per tag and per series |
| `sitemap.xml` | every listed page, with the date it last changed |
| `robots.txt` | crawler rules from `[robots]`; never blocks ordinary search engines |
| `search-index.json`, `search/index-N.json` | the search index, loaded only when someone searches |
| `social/<slug>-<hash>.png` | a 1200x630 sharing image per post |
| `_redirects` | every redirect (`from to 301`) and removed page (`from from 410`), read by `skimdit serve` |

Feed entry ids are tag URIs (`tag:example.com,2026-10-20:my-post`): they stay the same when you change the permalink pattern, so readers never see old posts as new. An edit only changes `updated` when you set `updated:` in the frontmatter.

Pages carry Open Graph and Twitter cards, `rel=canonical`, microformats2 (`h-entry`, `h-card`) and schema.org structured data (`BlogPosting`, `WebSite`, `BreadcrumbList`).

## Published URLs never break

`published-urls.txt` lists every URL the site has published and the file it came from. Commit it.

- Change a slug and run `skimdit build`: the old URL is recorded in `redirects.toml` and redirects to the new one.
- `skimdit check` (in CI) fails if a URL from the list would stop working.
- Removed a post on purpose? Add `"/posts/old/" = "gone"` to `redirects.toml`; it answers 410 Gone.

```toml
# redirects.toml
[redirects]
"/2019/my-old-post/" = "/posts/my-old-post/"
"/posts/retired/" = "gone"
```

## Staging

Build with `SKIMDIT_STAGING=1` for any copy that is not the real site: robots.txt turns every crawler away and every page is marked `noindex`.

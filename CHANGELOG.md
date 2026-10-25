# Changelog

All notable changes to Skimdit are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Repository setup: AGPL-3.0 licence, contribution guide, code of conduct, security policy.
- `skimdit version` and `skimdit help`.
- CI on Linux, macOS and Windows; signed release pipeline with SBOMs and a container image.
- Content core (M1): Markdown (CommonMark, GFM, footnotes), strict frontmatter with line-numbered errors, build-time syntax highlighting, KaTeX math, directives (callout, youtube, video, figure, quote, site-defined), sanitized raw HTML and HTML posts.
- Image pipeline: responsive AVIF/WebP/JPEG/PNG, EXIF and GPS stripped, orientation fixed, cached by content.
- `skimdit init`, `new`, `build`, `dev` (live reload, error page) and `check` (links, anchors, drafts in git, page weight).
- Drafts, scheduled, unlisted posts; frozen slugs with aliases; series, tags, related posts, backlinks, archives by year and month.
- Atomic output swap; deterministic builds; base-path hosting.
- Default theme (M2): dark-first "night study" design with Newsreader for reading text, light mode, theme toggle, table of contents, related posts, backlinks, footnote popovers, image lightbox, copy-code band, print styles; WCAG 2.2 AA contrast tested; `skimdit theme check` verifies any theme against the contract; site-level theme overrides; every UI string in one locale file.
- Distribution (M3): Atom, RSS and JSON feeds (site, per tag, per series) with full content and stable ids; sitemap; robots.txt with an AI-crawler policy; JSON-LD; generated social cards; chunked client-side search; URL ledger with automatic redirects; staging mode.

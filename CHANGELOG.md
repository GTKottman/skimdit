# Changelog

All notable changes to Skimdit are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [1.0.1] - 2026-10-25

### Fixed

- Release binaries and the container image were dynamically linked against glibc (the image/codec libraries load system libraries through purego), so the Alpine-based image could not start. Release builds now use the `nodynamic` tag: static binaries, WebAssembly codecs only. CI checks the Linux build is static.

## [1.0.0] - 2026-10-25

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
- Hardening (M9): independent security review with every finding fixed (fediverse ownership checks, login redirect, forged bounces, CSV formulas, IPv6 lockout grouping, SVG doctype); fuzzing of the renderer, sanitizers, signatures and bounce parser; load test (10,000 requests/s, p99 20 ms); container image with Litestream backups and a tested destroy-and-restore drill; strict health check and an external monitor script; deploy and restore runbook.
- Plugins, import and export (M8): sandboxed WebAssembly plugins (no files or network, memory and time limits, output sanitized) with post_render, directive, build_done and route hooks, a Go SDK and two example plugins; import from WordPress, Ghost and Markdown folders (Hugo, Jekyll) with aliases and local images; `skimdit export` to a working site zip with drafts and the database.
- Fediverse (M7): the site as an ActivityPub actor with WebFinger and NodeInfo, signed follow handling, signed delivery of new posts, replies/likes/boosts into moderation, deletes on unpublish; one-time Mastodon and Bluesky cross-posting with syndication links on the post.
- Newsletter and stats (M6): double opt-in sign-up, consent records, throttled send-on-publish over SMTP, one-click unsubscribe headers, bounce and complaint processing over IMAP, CSV export and erase; cookieless server-side stats with daily totals, top pages, referring domains and feed fetches.
- Replies (M5): moderated comments with proof-of-work, honeypot and rate limits; threaded owner replies; double opt-in reply emails with one-click unsubscribe; owner notifications; webmentions received (verified, microformats parsed, moderated) and sent on publish; responses built into static pages; GitHub push webhook.
- Server and editor (M4): `skimdit serve` with SQLite (migrations backed up automatically), one-time account setup, passkeys, argon2id passwords, TOTP, CSRF and lockouts; web editor with autosave, versions, uploads (EXIF stripped, SVG checked), live preview in the real theme; publish only after a passing build, committed to git; scheduling kept private until its time; unpublish to 410; compressed, cached static serving with redirects; background job queue with retries; `skimdit admin reset`.
- Distribution (M3): Atom, RSS and JSON feeds (site, per tag, per series) with full content and stable ids; sitemap; robots.txt with an AI-crawler policy; JSON-LD; generated social cards; chunked client-side search; URL ledger with automatic redirects; staging mode.

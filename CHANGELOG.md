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

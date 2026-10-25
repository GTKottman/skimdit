# Writing posts

Posts and pages are Markdown files with YAML frontmatter.

```
content/
  posts/
    my-first-post/
      index.md       # the post
      photo.jpg      # images and files used by the post
  pages/
    about.md         # a page (no date, not in feeds)
```

A post can also be a single file (`content/posts/my-first-post.md`) or plain HTML (`index.html`). Files and folders starting with `_` or `.` are ignored.

Create a draft with `skimdit new "My first post"` (or `--page` for a page).

## Frontmatter

```yaml
---
title: My first post          # required
date: 2026-10-25T09:00:00     # required for posts; no timezone = site timezone
updated: 2026-10-26           # shown as "Updated" and used by feeds
status: published             # draft | published | scheduled | unlisted
slug: my-first-post           # default: the folder or file name
tags: [go, writing]
series: Learning Go
series_order: 1
excerpt: Shown in lists.      # default: the text before <!--more-->, or the first paragraph
description: For search engines and social previews. # default: the excerpt
cover: cover.jpg
cover_alt: What the cover shows   # required with cover
social_image: card.png        # replaces the generated sharing image
aliases: [/old/url/]          # old addresses that redirect here
canonical: https://elsewhere.example/original  # if first published elsewhere
lang: en
toc: true                     # force the table of contents on or off
comments: false               # turn comments off for this post
schema_version: 1
extra: { anything: you like } # free-form data for themes and plugins
---
```

Unknown fields are errors, with the line number and a suggestion for typos.

**Status**

| Status | Built | In lists, feeds, sitemap, search |
|---|---|---|
| `draft` | only in `skimdit dev` | no |
| `published` | yes, once `date` has passed | yes |
| `scheduled` | yes, once `date` has passed | yes |
| `unlisted` | yes, at its URL, marked `noindex` | no |

A post with a future `date` stays hidden until a build runs after that time. `skimdit serve` rebuilds at the right moment; a static deploy needs a rebuild on a timer.

**Slugs never change after publishing.** If you must change one, add the old URL to `aliases`, so links keep working. `skimdit check` fails when a published URL disappears without a redirect.

## Markdown

CommonMark plus GitHub Flavored Markdown (tables, task lists, strikethrough, autolinks) and footnotes:

```markdown
A claim.[^1]

[^1]: The source.
```

Headings get stable ids (`## Why Go?` becomes `#why-go`); set your own with `## Why Go? {#why}`.

Raw HTML is allowed and cleaned: scripts, styles, event handlers, iframes and forms are removed. Use directives for embeds.

### Links

| Write | Means |
|---|---|
| `[x](post:other-slug)` | another post, by slug; the build fails if it does not exist |
| `[x](page:about#team)` | a page, with an anchor |
| `[x](notes.pdf)` | a file in the post folder; it is published next to the post |
| `[x](/archive/)` | a site path (the base path is added for you) |

### Images

```markdown
![A red barn at dusk](barn.jpg "Optional caption")
```

Images in the post folder (or in `static/`, written as `/images/x.png`) are resized to several widths, converted to AVIF and WebP, and stripped of EXIF and GPS data. Width and height are set so the page does not jump while loading. An image alone on its line becomes a figure; the title becomes its caption.

Alt text is required (`images.require_alt`). Remote images are refused, so readers never make third-party requests.

### Code

````markdown
```go {title="main.go" hl="3-5" linenos=true start=10}
package main
```
````

Highlighting happens at build time; no JavaScript is needed.

### Math

`$e^{i\pi} + 1 = 0$` inline, and blocks:

```markdown
$$
\int_0^\infty e^{-x^2}\,dx = \frac{\sqrt{\pi}}{2}
$$
```

Math is rendered to HTML and MathML at build time with KaTeX. A dollar sign followed by a digit (`$5`) is never math. A typo in a formula fails the build with its line number.

### Directives

```markdown
:::callout{type=warning title="Careful"}
Text with **Markdown**.
:::

::youtube{id=dQw4w9WgXcQ title="What the video is" start=30}

::video{src=clip.mp4 title="Demo" poster=poster.jpg caption="A caption"}

::figure{src=chart.png alt="Sales by month" caption="Source: our books"}

:::quote{url="https://example.com/post/1" author="Ada" source="Her blog" date="2026"}
The quoted words.
:::
```

`callout` types: `note`, `tip`, `warning`, `important`. YouTube embeds load nothing from YouTube until the reader clicks.

Nest containers by using more colons on the outer one (`::::callout` … `::::`).

**Site directives:** add `directives/<name>.html` to the site, a Go template that receives `.Attrs` (a map) and `.Inner` (the body's HTML). `::name{key="value"}` then renders it.

### Excerpts

The excerpt is the text before `<!--more-->`, or the first paragraph, or `excerpt` from the frontmatter.

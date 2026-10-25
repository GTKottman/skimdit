# Themes

A theme is a folder:

```
my-theme/
  theme.toml
  templates/
    base.html          # the page shell; defines blocks "main"
    home.html  post.html  page.html  list.html  tags.html
    series-list.html  archive.html  search.html  404.html  redirect.html
    partials/*.html    # included with {{template "partials/name" .}}
  assets/              # copied to /assets/<hash>/ (cached forever)
```

```toml
# theme.toml
name = "my-theme"
version = "1.0.0"
engine_api = 1
description = "What it looks like"
license = "MIT"
author = "You"
```

Use it with `[theme] name = "themes/my-theme"` in `config.toml`. Check it with:

```
skimdit theme check themes/my-theme
```

This builds a sample site that uses every page type and content feature with your theme, then runs every check `skimdit check` runs (links, anchors, feeds, sitemap, structured data, page weight). Run it in your theme's CI.

## Overriding one file

A site never needs to fork a theme. Put a file at the same path under `theme/` in the site, and it wins:

```
my-site/theme/templates/partials/footer.html   # replaces the footer only
my-site/theme/assets/css/main.css              # replaces the stylesheet only
```

The accent colour needs no override: set `[theme] accent = "#7fb0ff"`.

## The contract (engine_api 1)

Every page template receives a `Page`. The field names below are frozen: new fields may be added, but nothing is renamed or removed without a new `engine_api`. A theme built for the previous `engine_api` keeps working for 12 months, with a warning in every build, before support ends.

### Page

| Field | Type | Meaning |
|---|---|---|
| `Site` | `SiteData` | the whole site |
| `Kind` | string | `home`, `post`, `page`, `tag`, `tags`, `series`, `series-list`, `archive`, `search`, `404`, `redirect` |
| `Title`, `Description` | string | for `<title>` and meta tags |
| `URL`, `Permalink`, `Canonical` | string | site path, absolute URL, canonical URL |
| `NoIndex` | bool | emit `<meta name="robots" content="noindex">` |
| `Lang` | string | page language |
| `Image` | string | absolute URL of the sharing image |
| `Entry` | `*Entry` | the post or page (post and page kinds) |
| `List` | `[]*Entry` | entries on a list page |
| `Term` | `*Term` | the tag (tag pages) |
| `Series` | `*SeriesData` | the series (series pages) |
| `Pagination` | `*Pagination` | page links for long lists |
| `Archive` | `*Archive` | years and months (archive pages) |
| `Target` | string | destination (redirect pages) |
| `JSONLD` | JS | structured data; put it in `<script type="application/ld+json">` |
| `Recent` | `[]*Entry` | the five newest posts |
| `Assets` | string | asset folder URL, e.g. `/assets/3fa2c1d0/` |
| `SiteCSS` | string | URL of the CSS generated from the config |
| `NeedsJS` | bool | the page has an embed or search |

### SiteData

`Title`, `Description`, `URL`, `BasePath`, `Language`, `Copyright`, `Intro` (HTML), `Author` (`Name`, `Email`, `URL`, `Avatar`), `Social` (links), `Pages` (listed pages, for navigation), `Tags`, `Series`, `Feeds` (`Title`, `URL`, `Type`), `Features` (`Search`, `TagPages`, `Comments`, `ReplyByEmail`, `Newsletter`, `Webmentions`, `Fediverse`, `FediverseHandle`, `API`), `Generator`, `Dev`, `Year`.

### Entry

`Kind`, `Title`, `Slug`, `URL`, `Permalink`, `Date`, `Updated`, `Content` (HTML), `Excerpt`, `Description`, `Tags` (`[]*Term`), `Series`, `SeriesPart`, `Words`, `ReadingTime` (minutes), `TOC` (`Level`, `ID`, `Text`), `ShowTOC`, `Cover` (`HTML`, `URL`, `AbsURL`, `Width`, `Height`, `Alt`), `Prev`, `Next`, `SeriesPrev`, `SeriesNext`, `Related`, `Backlinks`, `HasMath`, `HasCode`, `HasEmbed`, `Status`, `Unlisted`, `Lang`, `Canonical`, `Comments`, `Extra` (frontmatter `extra`), `Source`.

### Term, SeriesData, Pagination, Archive

- `Term`: `Name`, `Slug`, `URL`, `Count`, `Entries`
- `SeriesData`: `Name`, `Slug`, `URL`, `Entries` (reading order)
- `Pagination`: `Page`, `Pages`, `PrevURL`, `NextURL`, `Links` (`Number`, `URL`, `Current`)
- `Archive`: `Years` (`Year`, `URL`, `Months`, `Entries`), and `Year`, `Month`, `Day` on single-period pages

### Template functions

| Function | Example |
|---|---|
| `t key [name value]...` | `{{t "reading_time" "n" .ReadingTime}}` (strings from the locale file) |
| `date`, `isoDate`, `dateOnly` | `{{date .Date}}`, `{{isoDate .Date}}` |
| `monthName` | `{{monthName 3}}` |
| `rel`, `abs` | `{{rel "/archive/"}}` adds the base path; `{{abs .URL}}` makes it absolute |
| `asset` | `{{asset "css/main.css"}}` |
| `postsCount` | `{{postsCount .Count}}` |
| `json` | `{{json .Site.Social}}` |
| `mailto` | `{{mailto .Site.Author.Email "Re: hello"}}` |
| `add`, `sub`, `lower`, `first`, `dict` | helpers |

## Rules for themes

- Pages must work without JavaScript. Scripts are enhancements.
- No third-party requests: fonts, scripts and images ship in `assets/`.
- No inline `<script>` (the server's Content-Security-Policy blocks it); use files in `assets/`.
- Stay inside the page budget (`check.page_budget_kb`, compressed HTML+CSS).
- Meet WCAG 2.2 AA in every colour mode.

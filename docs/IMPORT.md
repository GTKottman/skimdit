# Moving in and out

## Importing

```sh
skimdit import wordpress export.xml      # WordPress: Tools › Export › All content
skimdit import ghost export.json         # Ghost: Settings › Labs › Export your content
skimdit import markdown path/to/content  # Hugo, Jekyll, Eleventy, or another Skimdit site
```

Flags: `--site DIR`, `--no-download` (keep remote images as links), `--images DIR` (copy images from a downloaded uploads folder instead of fetching them), `--base-url https://old.example` (resolve relative image paths and Ghost's `__GHOST_URL__`), `--overwrite`.

What happens:

- Posts and pages become `content/posts/<slug>/index.md` and `content/pages/<slug>/index.md`, with their images downloaded next to them.
- Each post's old address becomes an alias, so old links redirect to the new one.
- HTML (WordPress, Ghost) is converted to Markdown. WordPress captions become image captions; other shortcodes are removed and listed.
- Tags and categories become tags. Ghost's internal `#tags` are left out.
- Drafts keep `status: draft`. Do not commit them to a public repository (`skimdit check` warns).
- Images without a description get "Image" and a warning: replace it with a real description.
- Comments are not imported (the count is reported).

Hugo and Jekyll frontmatter (`draft`, `categories`, `aliases`, `redirect_from`, `permalink`, `lastmod`, `summary`, `image`...) maps to Skimdit fields; anything else lands in `extra`. Hugo `{{< >}}` and Jekyll `{% %}` shortcodes are reported for you to rewrite as directives.

Run `skimdit check` afterwards and fix what it reports.

## Exporting

```sh
skimdit export site-backup.zip
```

The zip is a complete, working site folder: content, settings, theme overrides and plugins, unpublished drafts as Markdown (`drafts/`), and a copy of the database (`data/skimdit.db`: comments, subscribers, stats). Unzip it anywhere and `skimdit build` produces the same site, byte for byte (this is tested). The database contains personal data (subscriber emails): store the export safely.

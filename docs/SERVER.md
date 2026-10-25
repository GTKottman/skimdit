# Running the server

`skimdit serve` serves the built site and runs everything dynamic next to it: the web editor, comments, the newsletter, stats, webmentions and ActivityPub. Readers only ever receive pre-built files.

```sh
export SKIMDIT_SECRET="$(openssl rand -base64 48)"   # keep it stable across restarts
skimdit serve --site /path/to/site
```

On the first start the log prints a one-time link:

```
NO ACCOUNT YET. Create it within this run at: https://example.com/admin/setup?token=...
```

Open it, choose a password (12+ characters), then add a passkey and a code app in **Settings**. Only someone who can read the server log can create the account.

## Environment

| Variable | Needed | Meaning |
|---|---|---|
| `SKIMDIT_SECRET` | yes | 32+ random characters; signs tokens in links and forms |
| `SKIMDIT_SMTP_PASSWORD` | newsletter | mail server password |
| `SKIMDIT_IMAP_PASSWORD` | newsletter | bounce mailbox password |
| `SKIMDIT_WEBHOOK_SECRET` | git webhook | shared secret of the GitHub push webhook |
| `SKIMDIT_MASTODON_TOKEN`, `SKIMDIT_BLUESKY_PASSWORD` | cross-posting | account credentials |
| `SKIMDIT_STAGING=1` | staging only | every page `noindex`, robots.txt turns crawlers away |
| `SKIMDIT_RP_ID`, `SKIMDIT_EXTRA_ORIGINS` | rarely | passkey domain overrides for unusual setups |

## How publishing works

1. You write in the editor at `/admin/`. Every change autosaves to the database; a version is kept at most once a minute.
2. **Publish** writes `content/posts/<slug>/index.md` (and uploaded files) into the site folder, builds the whole site, and only if the build passes commits the change to git (and pushes when `server.push = true`). A failed build puts every file back; nothing half-done reaches readers or git.
3. **Schedule** keeps the post private in the database until its time, then publishes it the same way. Scheduled posts never sit in the public git repository early.
4. **Unpublish** removes the post, answers its address with 410 Gone, commits that, and puts the text back in your drafts.

Writing locally still works: commit Markdown files and push. A GitHub push webhook (`/api/hooks/git`) makes the server pull and rebuild.

Uploaded images are decoded and re-encoded, which removes EXIF and GPS data. SVG files are accepted only when they contain no scripts, event handlers or outside links.

## Security model

- Readers get static files with a strict Content-Security-Policy (no inline scripts), `nosniff`, a referrer policy and HSTS.
- The dynamic surface is `/admin`, `/api`, `/webmention`, `/ap` and `/.well-known`.
- One account. Sign in with a passkey, or a password (argon2id) plus an optional code app (TOTP).
- Sessions are random 256-bit tokens, stored only as hashes, in `HttpOnly`, `SameSite=Lax` cookies scoped to `/admin/`, `Secure` over HTTPS.
- Every state-changing admin request needs the same origin and a per-session CSRF token.
- After 5 failed sign-ins from one address in 15 minutes, that address is locked out for 15 minutes; after 100 failures site-wide in an hour, password sign-in pauses (passkeys still work).
- Outgoing requests (webmention checks, fediverse fetches) refuse private and local addresses.

Lost your password and devices? On the server:

```sh
skimdit admin reset --site /site
```

It sets a new password, turns off the code app, removes every passkey and signs every browser out.

## Data

Everything dynamic lives in `server.data` (default `.skimdit/data`): `skimdit.db` (SQLite), uploads waiting to be published, and the built site. Back it up; see [DEPLOY.md](DEPLOY.md). The database is backed up automatically before every schema migration (`backups/`).

`GET /healthz` answers `ok` when the database and the built site are in place, and reports a failed last build.

# Deploying, backing up and restoring

This is the tested way to run Skimdit in production: one container on a server running [Coolify](https://coolify.io) (any Docker host works the same), with posts in a GitHub repository and the database copied continuously to an SFTP server such as a Hetzner Storage Box.

```
GitHub repo (posts) ──clone/pull──▶ container: skimdit serve ──▶ readers
        ▲                             │      │
        └──── publish commits ────────┘      └─ Litestream ──▶ Storage Box (database copy)
```

Nothing important lives only on the server: posts are in git, the database is on the Storage Box. A dead server is replaced in about half an hour (see *Restore* below).

## 1. The site repository

1. Create the posts repository (for example `GTKottman/deeperthinking`) with `config.toml` and `content/`. `skimdit init` makes a starting point.
2. In `config.toml`:
   ```toml
   [server]
   push = true          # publish commits go back to GitHub
   trust_proxy = true   # Coolify's proxy sets X-Forwarded-For
   ```
3. Create an SSH **deploy key** with write access (repository *Settings › Deploy keys*, tick *Allow write access*). Keep the private key for step 2.

## 2. The container in Coolify

1. *New resource › Docker image*: `ghcr.io/gtkottman/skimdit:<version>` (or *Dockerfile* with `deploy/Dockerfile.source` from this repository to build from source).
2. Port **8080**. Domain: `https://yourdomain` (and `https://www.yourdomain`; Skimdit redirects www to the main address itself).
3. A persistent volume mounted at **`/data`**.
4. Environment variables:

| Variable | Value |
|---|---|
| `SKIMDIT_REPO` | `git@github.com:you/your-site.git` |
| `SKIMDIT_DEPLOY_KEY` | the deploy key's private key (multi-line) |
| `SKIMDIT_SECRET` | `openssl rand -base64 48` (keep it: changing it signs everyone out of their links) |
| `LITESTREAM_REPLICA_URL` | `sftp://u123456-sub1@u123456.your-storagebox.de:23/home/yourdomain` (use a Storage Box sub-account limited to one folder; its files appear under `/home`) |
| `LITESTREAM_SFTP_KEY` | private SSH key allowed on the Storage Box |
| `LITESTREAM_SFTP_HOST_KEY` | the host key Litestream's SSH client is offered, which for a Hetzner Storage Box is the ECDSA one: `ssh-keyscan -p 23 -t ecdsa u123456.your-storagebox.de`, without the host name (check it against Hetzner's published fingerprint). The ed25519 key fails with `host key mismatch` |
| `SKIMDIT_WEBHOOK_SECRET` | `openssl rand -hex 32` |
| `SKIMDIT_SMTP_PASSWORD`, `SKIMDIT_IMAP_PASSWORD` | if the newsletter is on |

5. Deploy. The log prints `NO ACCOUNT YET ... /admin/setup?token=...` once: open that link to create your account, then add a passkey and a code app in *Settings*.
6. In GitHub: repository *Settings › Webhooks › Add*: payload URL `https://yourdomain/api/hooks/git`, content type `application/json`, the `SKIMDIT_WEBHOOK_SECRET` as secret, *just the push event*. Now `git push` from your laptop updates the site within seconds.

The container's own health check calls `/healthz`. Updating Skimdit = change the image tag and redeploy; the database is backed up automatically before any schema change.

## 3. DNS and TLS

- `A` (and `AAAA` if the server has IPv6) records for `@` and `www` pointing at the server. Coolify gets and renews Let's Encrypt certificates.
- A `CAA` record limiting certificates to Let's Encrypt: `0 issue "letsencrypt.org"`.
- At the registrar: auto-renew on, two-factor sign-in, transfer lock.
- Skimdit sends `Strict-Transport-Security` once HTTPS works.

## 4. Monitoring

Run `deploy/monitor.sh` every 5 minutes from **another machine** (a home server, a second VPS):

```sh
*/5 * * * * SITE=https://yourdomain NTFY=https://ntfy.sh/your-private-topic /path/to/monitor.sh
```

It alerts when the site is down, the last build failed, a background job (newsletter, delivery) failed, or the TLS certificate expires within 14 days. `GET /healthz?strict=1` is the same check for any uptime service.

## 5. Backups

| What | Where | How |
|---|---|---|
| Posts, settings, redirects | GitHub, plus a mirror | `git clone --mirror`, then a weekly `git remote update` to the Storage Box or Codeberg |
| Database (comments, subscribers, stats, drafts) | Storage Box | Litestream, continuously (seconds behind) |
| Everything, in one file | anywhere | `skimdit export backup.zip` (also a good monthly habit) |
| The server itself | Hetzner snapshots | optional: the server is disposable |

## 6. Restore (tested)

The container restores by itself: when `/data` has no database and `LITESTREAM_REPLICA_URL` is set, it pulls the newest copy before starting. To move to a new server:

1. Install Coolify on the new server (or any Docker host).
2. Create the same container (step 2) with the same environment variables and an empty `/data` volume.
3. Deploy. The log shows `cloning ...` and `restoring from the backup`; no setup link appears, because your account came back with the database.
4. Point DNS at the new server.

Do this once on a scratch server before relying on it. The project's test suite runs the same sequence locally (start, write, destroy the volume, start again, sign in).

To restore by hand somewhere else: `litestream restore -o skimdit.db sftp://...` gives you the database file; put it in the data folder.

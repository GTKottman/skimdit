# Fediverse and cross-posting

Both are off by default.

## ActivityPub

```toml
[fediverse]
enabled = true
handle = "blog"          # people follow @blog@yourdomain
summary = "Long-form notes on ideas worth sitting with."
```

The site becomes one fediverse account. People on Mastodon and similar servers can search for `@blog@yourdomain` and follow it. Each new post is delivered to followers as an article (title, summary, full text, link). Replies, likes and boosts come back into **Admin › Comments** under "Mentions" and wait for your approval like any other response. When a post is unpublished, followers' servers are told to delete it.

What it serves: WebFinger (`/.well-known/webfinger`), NodeInfo, the actor (`/ap/actor`), an outbox of recent posts, a follower count (never the list), and each post as an object (`/ap/posts/<slug>`).

Security: every incoming activity must carry a valid HTTP signature from the actor it claims to come from; the date must be within 12 hours and the body digest must match. Fetches of remote actors are signed (so servers with "authorized fetch" answer) and never reach private network addresses. Delivery to each server is its own job, retried with backoff; a server answering "gone" loses its followers.

## Mastodon and Bluesky

```toml
[crosspost.mastodon]
enabled = true
instance = "https://mastodon.social"   # token in SKIMDIT_MASTODON_TOKEN

[crosspost.bluesky]
enabled = true
handle = "you.bsky.social"            # app password in SKIMDIT_BLUESKY_PASSWORD
```

Each new post is announced once on each network: its title, the start of its excerpt and its link (Bluesky gets a link card). The resulting posts are shown under the post as "Also on Mastodon, Bluesky" (`u-syndication`). A retry never posts twice.

- Mastodon: create an application under *Preferences › Development* with the `write:statuses` scope and use its access token.
- Bluesky: create an app password under *Settings › Privacy and security › App passwords*.

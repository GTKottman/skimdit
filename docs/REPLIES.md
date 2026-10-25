# Comments, replies and mentions

Everything readers send waits for your approval in **Admin › Comments** before it appears. Approved responses are built into the static post page, so readers need no JavaScript to read them.

## Comments

Readers write in a small subset of Markdown (emphasis, links, lists, code, quotes). Raw HTML and images are removed; links get `rel="nofollow ugc"`.

Spam defences, all on by default:

- **Moderation:** nothing is public until approved.
- **Proof of work:** the form's script solves a small puzzle (`comments.pow_bits`, default 18) before sending. Cheap for one reader, expensive for a bot sending thousands.
- **Honeypot:** a hidden field people never fill in.
- **Limits:** 5 comments per address per 10 minutes, 20 per day; at most `comments.max_links` links and `comments.max_length` characters.

The form needs JavaScript (for the puzzle). The "Reply by email" link under every post works without it.

Turn comments off for one post with `comments: false` in its frontmatter, or for the site with `[comments] enabled = false`.

### Emails

With a mail server configured ([newsletter] `smtp_*`):

- you get an email for each new comment waiting for approval (sent to `author.email`);
- a commenter who ticks "Email me when someone replies" first gets a confirmation link (double opt-in); after confirming, each approved reply sends them one email with a one-click unsubscribe.

Reply to a comment from the moderation page; your reply is published at once.

The only personal data kept: the name, the optional email and website a commenter typed, and a daily-rotating hash of their IP address for rate limits, erased after 30 days.

## Webmentions

Other sites can tell you they linked to a post (`POST /webmention`). Skimdit fetches the page, checks it really links to you, and reads its microformats: who wrote it, and whether it is a reply, like, repost or mention. It then waits for approval. If the page later disappears or drops the link, its webmention is removed when they resend.

When you publish a post, Skimdit sends webmentions to every page it links to that accepts them, once per post.

Author photos are not shown: showing them would make readers' browsers fetch images from other sites.

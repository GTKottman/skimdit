# Newsletter and stats

## Newsletter

Readers sign up with the form under posts and on the home page. Each new post is emailed to every confirmed subscriber once, in throttled batches.

```toml
[newsletter]
enabled = true
from = "Deeper Thinking <letters@deeperthinking.xyz>"
smtp_host = "mail.example.com"     # your mail server, e.g. Stalwart
smtp_port = 465
smtp_security = "tls"              # tls (465), starttls (587) or none
smtp_user = "letters@deeperthinking.xyz"
bounce_imap = "mail.example.com:993"
bounce_user = "bounces@deeperthinking.xyz"
content = "full"                   # or "excerpt"
batch_size = 20
batch_pause_seconds = 30
```

Passwords come from `SKIMDIT_SMTP_PASSWORD` and `SKIMDIT_IMAP_PASSWORD`.

**How consent works.** Signing up sends a confirmation link; nothing else is sent until it is clicked (double opt-in). The time and page of consent are recorded. The sign-up page answers the same way for new and existing addresses, so nobody can probe the list. Confirmation and unsubscribe links lead to a page with a button, so mail scanners that open every link never confirm or unsubscribe by accident.

**Every email** has a plain-text and an HTML part, a visible unsubscribe link, and `List-Unsubscribe` / `List-Unsubscribe-Post` headers for one-click unsubscribe in mail apps (RFC 8058). There are no tracking pixels and no click tracking.

**Bounces.** Point the `Return-Path`/bounce address of your mail server at the `bounce_user` mailbox. Every 30 minutes Skimdit reads new messages there: a hard bounce or three soft bounces stop mail to that address, and a spam complaint (feedback loop report) unsubscribes it.

**Only new posts are sent:** posts older than 7 days when the server first sees them (imports, back-dated posts) are never emailed, and each post goes out at most once.

**Admin › Subscribers** shows the counts, each post's sending progress, and lets you unsubscribe or erase an address (for deletion requests) and export everything as CSV. *Email me the newest post* sends a test to `author.email`.

### Before the first real send

Mail from a new domain is easily marked as spam. On the DNS of the sending domain:

1. **SPF:** a TXT record allowing your mail server, e.g. `v=spf1 mx -all` or `v=spf1 ip4:<server IP> -all`.
2. **DKIM:** create a signing key in Stalwart for the domain and publish its TXT record (`<selector>._domainkey`).
3. **DMARC:** a TXT record at `_dmarc`, starting with `v=DMARC1; p=none; rua=mailto:<you>` and moving to `p=quarantine` once reports are clean.
4. **Reverse DNS** of the server IP should match the mail server's name.

Then send a test (*Email me the newest post*) to a mail-tester.com address and to a Gmail and an Outlook inbox, and check it lands in the inbox with SPF, DKIM and DMARC passing.

## Stats

```toml
[stats]
enabled = true
```

The server counts page views as it serves pages. It keeps only daily totals per page, the referring site's domain, and feed fetches. No cookie is set, no script runs, and no IP address or browser detail is stored, so there is nothing to ask consent for. Bots and link-preview fetchers are left out; feed readers are counted separately because they poll.

**Admin › Stats** shows views per day, top pages and the sites that sent readers, for 7 or 30 days.

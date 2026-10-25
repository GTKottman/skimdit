#!/bin/sh
# Checks a Skimdit site from another machine and sends an alert when
# something needs attention: the site is down, the last build failed, a
# background job failed, or the TLS certificate expires within 14 days.
#
#   SITE=https://deeperthinking.xyz NTFY=https://ntfy.sh/your-topic ./monitor.sh
#
# Run it every 5 minutes (cron or a systemd timer). Alerts go to an ntfy
# topic (NTFY) or, with MAILTO set, through the local `mail` command.
set -u
SITE="${SITE:?set SITE to the site's address}"
HOST=$(printf '%s' "$SITE" | sed -E 's#^https?://##; s#/.*##')
problems=""

body=$(curl -fsS --max-time 15 "$SITE/healthz?strict=1" 2>&1) || problems="$problems
Health check failed: $(printf '%s' "$body" | head -3)"

end=$(echo | openssl s_client -servername "$HOST" -connect "$HOST:443" 2>/dev/null | openssl x509 -noout -enddate 2>/dev/null | cut -d= -f2)
if [ -z "$end" ]; then
	problems="$problems
Could not read the TLS certificate of $HOST"
else
	left=$(( ( $(date -d "$end" +%s) - $(date +%s) ) / 86400 ))
	[ "$left" -lt 14 ] && problems="$problems
TLS certificate of $HOST expires in $left days ($end)"
fi

[ -z "$problems" ] && exit 0
msg="Skimdit: $SITE needs attention:$problems"
if [ -n "${NTFY:-}" ]; then
	curl -fsS --max-time 15 -H "Title: $HOST needs attention" -H "Priority: high" -d "$msg" "$NTFY" >/dev/null
fi
if [ -n "${MAILTO:-}" ]; then
	printf '%s\n' "$msg" | mail -s "$HOST needs attention" "$MAILTO"
fi
printf '%s\n' "$msg" >&2
exit 1

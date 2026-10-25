#!/bin/sh
# Starts Skimdit in a container: clones (or updates) the site repository,
# restores the database from the backup if it is missing, and runs
# `skimdit serve` under Litestream so the database is backed up continuously.
#
# Environment:
#   SKIMDIT_REPO            git URL of the site (posts) repository   (required on first start)
#   SKIMDIT_BRANCH          branch to use                            (default: main)
#   SKIMDIT_DEPLOY_KEY      SSH private key with write access to the repository (for publishing)
#   SKIMDIT_SECRET          32+ random characters                    (required)
#   SKIMDIT_SITE            where the checkout lives                 (default: /data/site)
#   SKIMDIT_DATA            database, uploads and built site         (default: /data/state)
#   LITESTREAM_REPLICA_URL  backup target, e.g. sftp://u123@u123.your-storagebox.de:23/skimdit
#   LITESTREAM_SFTP_KEY     SSH private key for the backup target
#   LITESTREAM_SFTP_HOST_KEY  the backup server's public host key (one line, "ssh-ed25519 AAAA...")
set -eu

SITE="${SKIMDIT_SITE:-/data/site}"
export SKIMDIT_DATA="${SKIMDIT_DATA:-/data/state}"
mkdir -p "$SKIMDIT_DATA" "$HOME/.ssh"
chmod 700 "$HOME/.ssh"
cp /etc/skimdit/known_hosts "$HOME/.ssh/known_hosts"

if [ -n "${SKIMDIT_DEPLOY_KEY:-}" ]; then
	printf '%s\n' "$SKIMDIT_DEPLOY_KEY" > "$HOME/.ssh/deploy_key"
	chmod 600 "$HOME/.ssh/deploy_key"
	export GIT_SSH_COMMAND="ssh -i $HOME/.ssh/deploy_key -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes"
fi

if [ ! -d "$SITE/.git" ]; then
	if [ -z "${SKIMDIT_REPO:-}" ]; then
		echo "skimdit: set SKIMDIT_REPO to the site's git URL" >&2
		exit 1
	fi
	echo "skimdit: cloning $SKIMDIT_REPO"
	git clone --quiet --branch "${SKIMDIT_BRANCH:-main}" "$SKIMDIT_REPO" "$SITE"
else
	echo "skimdit: updating $SITE"
	git -C "$SITE" fetch --quiet origin "${SKIMDIT_BRANCH:-main}" && git -C "$SITE" merge --quiet --ff-only "origin/${SKIMDIT_BRANCH:-main}" \
		|| echo "skimdit: warning: could not fast-forward the site repository; serving it as it is" >&2
fi

DB="$SKIMDIT_DATA/skimdit.db"
if [ -n "${LITESTREAM_REPLICA_URL:-}" ]; then
	CONF=/tmp/litestream.yml
	{
		echo "dbs:"
		echo "  - path: $DB"
		echo "    replicas:"
		echo "      - url: $LITESTREAM_REPLICA_URL"
		if [ -n "${LITESTREAM_SFTP_KEY:-}" ]; then
			printf '%s\n' "$LITESTREAM_SFTP_KEY" > "$HOME/.ssh/backup_key"
			chmod 600 "$HOME/.ssh/backup_key"
			echo "        key-path: $HOME/.ssh/backup_key"
		fi
		if [ -n "${LITESTREAM_SFTP_HOST_KEY:-}" ]; then
			echo "        host-key: \"$LITESTREAM_SFTP_HOST_KEY\""
		fi
	} > "$CONF"
	if [ ! -f "$DB" ]; then
		echo "skimdit: no database yet; restoring from the backup if there is one"
		litestream restore -config "$CONF" -if-replica-exists "$DB"
	fi
	exec litestream replicate -config "$CONF" -exec "skimdit serve --site $SITE"
fi

echo "skimdit: LITESTREAM_REPLICA_URL is not set, so the database is NOT backed up continuously" >&2
exec skimdit serve --site "$SITE"

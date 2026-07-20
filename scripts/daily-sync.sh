#!/usr/bin/env bash
# Regenerates the AI Engineer channel tracking collection from the
# channel's uploads playlist, then syncs every collection into the
# database so snapshots accrue. Interim FE-3 implementation — see
# .agent/tasks/followups.md. Run daily.
#
# Requires: YOUTUBE_API_KEY and DISCOVERY_DATABASE_URL (reads ./.env),
# curl, python3, go.
set -euo pipefail
cd "$(dirname "$0")/.."

set -a; [ -f .env ] && source .env; set +a
: "${YOUTUBE_API_KEY:?YOUTUBE_API_KEY not set (see .env.example)}"

python3 scripts/draft_event_collections.py
python3 scripts/draft_channel_collections.py

for f in collections/*.json; do
  [ "$f" = "collections/example.json" ] && continue
  go run ./cmd/discovery validate "$f"
done

for f in collections/*.json; do
  [ "$f" = "collections/example.json" ] && continue
  echo "--- sync $f"
  DISCOVERY_COLLECTION_PATH="$f" go run ./cmd/discovery sync
done

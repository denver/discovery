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

# Containers ship the compiled binary; local dev uses the toolchain.
if command -v discovery >/dev/null 2>&1; then
  DISCOVERY="discovery"
else
  DISCOVERY="go run ./cmd/discovery"
fi

python3 scripts/draft_event_collections.py
python3 scripts/draft_channel_collections.py

for f in collections/*.json; do
  [ "$f" = "collections/example.json" ] && continue
  $DISCOVERY validate "$f"
done

for f in collections/*.json; do
  [ "$f" = "collections/example.json" ] && continue
  echo "--- sync $f"
  DISCOVERY_COLLECTION_PATH="$f" $DISCOVERY sync
done

# Mixes are computed FROM the freshly synced pools (db query), then
# synced themselves.
if [ -n "${DISCOVERY_DATABASE_URL:-}" ]; then
  python3 scripts/draft_mix_collections.py
  for f in collections/denvers-radar.json; do
    $DISCOVERY validate "$f"
    echo "--- sync $f"
    DISCOVERY_COLLECTION_PATH="$f" $DISCOVERY sync
  done
fi

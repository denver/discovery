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

# UC... channel ID -> UU... uploads playlist ID.
UPLOADS="UULKPca3kwwd-B59HNr-_lvA"
OUT="collections/ai-engineer-channel.json"

python3 - "$UPLOADS" "$OUT" <<'PY'
import json, sys, urllib.request, urllib.parse, os

playlist, out = sys.argv[1], sys.argv[2]
key = os.environ["YOUTUBE_API_KEY"]
ids, token = [], None
while True:
    params = {"part": "contentDetails", "playlistId": playlist,
              "maxResults": "50", "key": key}
    if token:
        params["pageToken"] = token
    with urllib.request.urlopen(
        "https://www.googleapis.com/youtube/v3/playlistItems?"
        + urllib.parse.urlencode(params), timeout=30
    ) as r:
        page = json.load(r)
    ids += [i["contentDetails"]["videoId"] for i in page["items"]]
    token = page.get("nextPageToken")
    if not token:
        break

seen = set()
videos = [{"youtubeId": v} for v in ids if not (v in seen or seen.add(v))]
collection = {
    "schemaVersion": "1.0",
    "slug": "ai-engineer-channel",
    "title": "AI Engineer (full channel)",
    "description": "Every upload from the AI Engineer channel, tracked daily. "
                   "The pool that curated collections split off from.",
    "source": {"type": "channel", "homepage": "https://www.youtube.com/@aiDotEngineer"},
    "defaultRanking": "views",
    "videos": videos,
}
with open(out, "w") as f:
    json.dump(collection, f, indent=2)
    f.write("\n")
print(f"wrote {out}: {len(videos)} videos")
PY

go run ./cmd/discovery validate "$OUT"

# Sync each collection separately (config takes one path at a time).
for f in collections/ai-engineer-channel.json collections/test.json; do
  echo "--- sync $f"
  DISCOVERY_COLLECTION_PATH="$f" go run ./cmd/discovery sync
done

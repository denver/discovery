#!/usr/bin/env python3
"""Generate rule-based "mix" collections: cross-source, time-bounded
slices over already-tracked pools (FE-5).

A mix's membership is computed from the database (which the pool syncs
keep fresh): videos belonging to any source collection, published within
the window. Zero YouTube quota. Must run AFTER pools are synced; the
daily script orders this correctly.

Requires: psql on PATH and the discovery database (db mode).
"""
import json
import os
import subprocess

from draft_event_collections import AUTHOR

MIXES = [
    {
        "slug": "top-creators",
        "title": "Top Creators",
        "description": "The all-time best across Lenny's Podcast, Greg Isenberg, "
                       "How I AI, Dwarkesh Patel, Dave Ebbelaar, and Peter Yang. "
                       "One leaderboard, six channels.",
        "sources": ["lennys-podcast", "greg-isenberg", "how-i-ai",
                    "dwarkesh-patel", "dave-ebbelaar", "peter-yang"],
        "window_days": None,
        # Episodes, not clips: without this, 98 of the top 100 are
        # sub-4-minute Dwarkesh clips (measured 2026-07-23).
        "min_minutes": 10,
    },
    {
        "slug": "denvers-radar",
        "title": "Denver's Radar",
        "description": "What's hot in the last six weeks across Dan Martell, "
                       "Lenny's Podcast, How I AI, AI Engineer, Anthropic, and "
                       "OpenAI. Rule-based mix, regenerated daily.",
        "sources": ["dan-martell", "lennys-podcast", "how-i-ai",
                    "ai-engineer-channel", "anthropic", "openai"],
        "window_days": 42,
    },
]


def member_ids(sources: list[str], window_days: int | None,
               min_minutes: int | None = None) -> list[str]:
    slugs = ",".join(f"'{s}'" for s in sources)
    recency = ""
    if window_days is not None:
        recency = f"AND v.published_at >= now() - interval '{window_days} days'"
    duration = ""
    if min_minutes is not None:
        duration = f"AND v.duration_seconds >= {min_minutes * 60}"
    sql = f"""
    SELECT DISTINCT v.youtube_id
    FROM videos v
    JOIN collection_videos cv ON cv.video_id = v.id
    JOIN collections c ON c.id = cv.collection_id
    WHERE c.slug IN ({slugs})
      {recency}
      {duration}
    ORDER BY v.youtube_id;
    """
    # DISCOVERY_DATABASE_URL in hosted environments; local default socket
    # database otherwise.
    target = os.environ.get("DISCOVERY_DATABASE_URL") or "dbname=discovery"
    out = subprocess.run(["psql", target, "-t", "-A", "-c", sql],
                         capture_output=True, text=True, check=True)
    return [line for line in out.stdout.splitlines() if line.strip()]


def main() -> None:
    for mix in MIXES:
        ids = member_ids(mix["sources"], mix["window_days"], mix.get("min_minutes"))
        coll = {
            "schemaVersion": "1.0",
            "slug": mix["slug"],
            "title": mix["title"],
            "description": mix["description"],
            "author": AUTHOR,
            "source": {"type": "mix"},
            "defaultRanking": "views",
            "videos": [{"youtubeId": vid} for vid in ids],
        }
        path = f"collections/{mix['slug']}.json"
        with open(path, "w") as f:
            json.dump(coll, f, indent=2, ensure_ascii=False)
            f.write("\n")
        span = f"last {mix['window_days']} days" if mix["window_days"] else "all time"
        print(f"{path}: {len(ids)} videos from {len(mix['sources'])} sources, {span}")


if __name__ == "__main__":
    main()

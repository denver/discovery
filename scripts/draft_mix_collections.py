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
import subprocess

from draft_event_collections import AUTHOR

MIXES = [
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


def member_ids(sources: list[str], window_days: int) -> list[str]:
    slugs = ",".join(f"'{s}'" for s in sources)
    sql = f"""
    SELECT DISTINCT v.youtube_id
    FROM videos v
    JOIN collection_videos cv ON cv.video_id = v.id
    JOIN collections c ON c.id = cv.collection_id
    WHERE c.slug IN ({slugs})
      AND v.published_at >= now() - interval '{window_days} days'
    ORDER BY v.youtube_id;
    """
    out = subprocess.run(["psql", "-d", "discovery", "-t", "-A", "-c", sql],
                         capture_output=True, text=True, check=True)
    return [line for line in out.stdout.splitlines() if line.strip()]


def main() -> None:
    for mix in MIXES:
        ids = member_ids(mix["sources"], mix["window_days"])
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
        print(f"{path}: {len(ids)} videos from {len(mix['sources'])} sources, "
              f"last {mix['window_days']} days")


if __name__ == "__main__":
    main()

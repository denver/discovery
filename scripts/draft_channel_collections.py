#!/usr/bin/env python3
"""Generate one tracking collection per followed channel (FE-3).

Each entry in CHANNELS becomes collections/<slug>.json containing every
upload from that channel, resolved live via the Data API (handle ->
channel -> uploads playlist). Editorial metadata is added later by the
curator or per-event drafters; these files are the tracked pools.
"""
import json

from draft_event_collections import AUTHOR, api_get, playlist_videos

# handle (without @), slug, optional description override.
CHANNELS = [
    {"handle": "howiaipodcast", "slug": "how-i-ai"},
    {"handle": "LennysPodcast", "slug": "lennys-podcast"},
    {"handle": "danmartell", "slug": "dan-martell"},
]


def channel_info(handle: str) -> dict:
    items = api_get("channels", part="id,snippet,statistics", forHandle=handle)["items"]
    if not items:
        raise SystemExit(f"channel handle not found: @{handle}")
    c = items[0]
    return {
        "id": c["id"],
        "title": c["snippet"]["title"],
        "uploads": "UU" + c["id"][2:],  # UC... -> UU... uploads playlist
        "videoCount": c["statistics"].get("videoCount", "?"),
    }


def main() -> None:
    for ch in CHANNELS:
        info = channel_info(ch["handle"])
        videos = [{"youtubeId": vid} for vid, _ in playlist_videos(info["uploads"])]
        coll = {
            "schemaVersion": "1.0",
            "slug": ch["slug"],
            "title": info["title"],
            "description": ch.get("description",
                                  f"Every upload from {info['title']}, tracked daily."),
            "author": AUTHOR,
            "source": {"type": "channel",
                       "homepage": f"https://www.youtube.com/@{ch['handle']}"},
            "defaultRanking": "views",
            "videos": videos,
        }
        path = f"collections/{ch['slug']}.json"
        with open(path, "w") as f:
            json.dump(coll, f, indent=2, ensure_ascii=False)
            f.write("\n")
        print(f"{path}: {len(videos)} videos ({info['title']})")


if __name__ == "__main__":
    main()

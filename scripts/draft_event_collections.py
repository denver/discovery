#!/usr/bin/env python3
"""Draft per-event collection files from the AI Engineer channel's own
playlist curation (interim FE-2 drafter; see .agent/tasks/followups.md).

For each event in EVENTS: fetch membership from the named playlists,
dedupe (first playlist listing a video wins its track), best-effort
parse speakers/organizations from the channel's title convention
("Talk Title — Speaker Name, Org"), and write collections/<slug>.json.

Editorial output is a DRAFT: the curator reviews before publishing.
Requires YOUTUBE_API_KEY in the environment (run via a shell that
sourced .env, or through scripts/sync-aie-channel.sh conventions).
"""
import json
import os
import re
import sys
import urllib.parse
import urllib.request

CHANNEL_ID = "UCLKPca3kwwd-B59HNr-_lvA"
CHANNEL_URL = "https://www.youtube.com/@aiDotEngineer"
AUTHOR = {"name": "Denver Peterson", "url": "https://denverpeterson.com"}

# (playlist title, track label or None). Order matters: the first
# playlist that lists a video decides its track.
EVENTS = [
    {
        "slug": "ai-engineer-summit-2023",
        "title": "AI Engineer Summit 2023",
        "event": {"name": "AI Engineer Summit", "year": 2023, "city": "San Francisco"},
        "playlists": [
            ("AI Engineer Summit 2023 - Talks", "Talks"),
            ("AI Engineer Summit 2023 - Remote Talks", "Remote Talks"),
            ("AI Engineer Summit 2023 Workshops", "Workshops"),
        ],
    },
    {
        "slug": "ai-engineer-worlds-fair-2024",
        "title": "AI Engineer World's Fair 2024",
        "event": {"name": "AI Engineer World's Fair", "year": 2024, "city": "San Francisco"},
        "playlists": [
            ("Keynote: AIE World's Fair 2024", "Keynotes"),
            ("Agents: AIE World's Fair 2024", "Agents"),
            ("Multimodality: AIE World's Fair 2024", "Multimodality"),
            ("GPUs & Inference: AIE World's Fair 2024", "GPUs & Inference"),
            ("RAG & LLM Frameworks: AIE World's Fair 2024", "RAG & LLM Frameworks"),
            ("Evals & LLM Ops: AIE World's Fair 2024", "Evals & LLM Ops"),
            ("AI Leadership: AIE World's Fair 2024", "AI Leadership"),
            ("Expo Sessions: AIE World's Fair 2024", "Expo"),
            ("Workshop: AIE World's Fair 2024", "Workshops"),
        ],
    },
    {
        "slug": "ai-engineer-summit-ny-2025",
        "title": "AI Engineer Summit NY 2025",
        "event": {"name": "AI Engineer Summit", "year": 2025, "city": "New York"},
        "playlists": [
            ("AI Engineer Summit NY 2025 Workshops", "Workshops"),
            ("AI Engineer Summit Online 2025", "Online"),
            ("Complete AI Engineer Summit NY 2025 Playlist", None),
        ],
    },
    {
        "slug": "ai-engineer-worlds-fair-2025",
        "title": "AI Engineer World's Fair 2025",
        "event": {"name": "AI Engineer World's Fair", "year": 2025, "city": "San Francisco"},
        "playlists": [
            ("SWE Agents: AI Engineer World's Fair 2025", "SWE Agents"),
            ("Infra: AI Engineer World's Fair 2025", "Infra"),
            ("RL + Reasoning : AI Engineer World's Fair 2025", "RL + Reasoning"),
            ("Design Engineering: AI Engineer World's Fair 2025", "Design Engineering"),
            ("Agent Reliability: AI Engineer World's Fair 2025", "Agent Reliability"),
            ("Tiny Teams: AI Engineer World's Fair 2025", "Tiny Teams"),
            ("LLM Recommendation Systems: AI Engineer World's Fair 2025", "LLM Recommendation Systems"),
            ("Full Workshops: AI Engineer World's Fair 2025", "Workshops"),
            ("AIE World's Fair 2025 Online", "Online"),
            ("AIEWF 2025 Complete Playlist", None),
        ],
    },
    {
        "slug": "aie-code-2025",
        "title": "AIE CODE 2025",
        "event": {"name": "AIE CODE", "year": 2025, "city": "New York"},
        "playlists": [
            ("AIE CODE 2025: Coding Model and Agent Labs", "Coding Model & Agent Labs"),
            ("AIE CODE 2025: AI Leadership", "AI Leadership"),
            ("AIE CODE 2025 Online Track", "Online"),
        ],
    },
    {
        "slug": "aie-europe-2026",
        "title": "AIE Europe 2026",
        # City deliberately omitted pending confirmation (docs/aie-events.md).
        "event": {"name": "AIE Europe", "year": 2026},
        "playlists": [
            ("AIE Europe 2026 Keynotes", "Keynotes"),
            ("AIE Europe 2026: Workshops", "Workshops"),
            ("AIE Europe 2026: Online Track", "Online"),
            ("AIE Europe 2026 Complete Playlist", None),
        ],
    },
    {
        "slug": "ai-engineer-worlds-fair-2026",
        "title": "AI Engineer World's Fair 2026",
        "event": {"name": "AI Engineer World's Fair", "year": 2026,
                  "city": "San Francisco", "venue": "Moscone West"},
        "playlists": [
            ("AIE World's Fair 2026 Complete Playlist", None),
            ("AI Engineer World's Fair Online Track 2026", "Online"),
        ],
    },
    {
        "slug": "ai-engineer-melbourne-2026",
        "title": "AI Engineer Melbourne 2026",
        "event": {"name": "AI Engineer Melbourne", "year": 2026, "city": "Melbourne"},
        "playlists": [],
        # No playlist exists; membership from Event: description blocks.
        "explicit_ids": ["wjXowoQ7E8c", "NmjGfdZLNIs"],
    },
]

API = "https://www.googleapis.com/youtube/v3"


def api_get(resource: str, **params) -> dict:
    params["key"] = os.environ["YOUTUBE_API_KEY"]
    url = f"{API}/{resource}?{urllib.parse.urlencode(params)}"
    with urllib.request.urlopen(url, timeout=30) as r:
        return json.load(r)


def paged(resource: str, **params):
    token = None
    while True:
        page = api_get(resource, **params, **({"pageToken": token} if token else {}))
        yield from page["items"]
        token = page.get("nextPageToken")
        if not token:
            return


def playlist_index() -> dict:
    return {
        p["snippet"]["title"]: p["id"]
        for p in paged("playlists", part="snippet", channelId=CHANNEL_ID, maxResults="50")
    }


def playlist_videos(playlist_id: str) -> list[tuple[str, str]]:
    """Returns (videoId, title) preserving playlist order."""
    out = []
    for item in paged("playlistItems", part="snippet,contentDetails",
                      playlistId=playlist_id, maxResults="50"):
        vid = item["contentDetails"]["videoId"]
        title = item["snippet"]["title"]
        if title not in ("Private video", "Deleted video"):
            out.append((vid, title))
    return out


# "Talk Title — Speaker Name, Org" (em or en dash). Conservative: on any
# doubt, emit no speakers rather than wrong ones.
TITLE_SPLIT = re.compile(r"\s+[—–]\s+")
NAME_OK = re.compile(r"^[A-Z][\w.'-]*(?:\s+[A-Z][\w.'-]*){0,3}$")


def slugify(name: str) -> str:
    s = re.sub(r"[^a-z0-9]+", "-", name.lower()).strip("-")
    return s


def parse_speakers(video_title: str) -> tuple[list[dict], list[str]]:
    parts = TITLE_SPLIT.split(video_title)
    if len(parts) < 2:
        return [], []
    tail = parts[-1]
    name_part, _, org = tail.partition(",")
    names = re.split(r"\s*(?:&|\band\b)\s*", name_part)
    speakers = []
    for n in (n.strip() for n in names):
        if not n or not NAME_OK.match(n):
            return [], []  # any doubtful name voids the whole parse
        slug = slugify(n)
        if not slug:
            return [], []
        speakers.append({"name": n, "slug": slug})
    orgs = [org.strip()] if org.strip() else []
    return speakers, orgs


def build(event: dict, titles_to_ids: dict) -> dict:
    entries: dict[str, dict] = {}
    order: list[str] = []
    for title, track in event["playlists"]:
        pid = titles_to_ids.get(title)
        if pid is None:
            print(f"  WARNING: playlist not found: {title!r}", file=sys.stderr)
            continue
        for vid, vtitle in playlist_videos(pid):
            if vid in entries:
                if entries[vid].get("track") is None and track:
                    entries[vid]["track"] = track
                continue
            speakers, orgs = parse_speakers(vtitle)
            e: dict = {"youtubeId": vid}
            if speakers:
                e["speakers"] = speakers
            if orgs:
                e["organizations"] = orgs
            e["event"] = event["event"]
            if track:
                e["track"] = track
            entries[vid] = e
            order.append(vid)
    for vid in event.get("explicit_ids", []):
        if vid not in entries:
            entries[vid] = {"youtubeId": vid, "event": event["event"]}
            order.append(vid)
    videos = []
    for vid in order:
        e = entries[vid]
        if e.get("track") is None:
            e.pop("track", None)
        videos.append(e)
    return {
        "schemaVersion": "1.0",
        "slug": event["slug"],
        "title": event["title"],
        "description": f"Talks from {event['title']}, drafted from the AI Engineer "
                       "channel's own playlists. Editorial metadata is best-effort "
                       "parsed and under review.",
        "author": AUTHOR,
        "source": {"type": "curated", "homepage": CHANNEL_URL},
        "defaultRanking": "views",
        "videos": videos,
    }


UPLOADS_PLAYLIST = "UULKPca3kwwd-B59HNr-_lvA"  # UC -> UU


def build_pool(event_index: dict) -> dict:
    """The full-channel tracking collection, with event/track stamped on
    every video that any event collection claims."""
    videos = []
    for vid, _ in playlist_videos(UPLOADS_PLAYLIST):
        e: dict = {"youtubeId": vid}
        if vid in event_index:
            e.update(event_index[vid])
        videos.append(e)
    return {
        "schemaVersion": "1.0",
        "slug": "ai-engineer-channel",
        "title": "AI Engineer (full channel)",
        "description": "Every upload from the AI Engineer channel, tracked daily. "
                       "The pool that curated collections split off from.",
        "source": {"type": "channel", "homepage": CHANNEL_URL},
        "defaultRanking": "views",
        "videos": videos,
    }


def main() -> None:
    titles_to_ids = playlist_index()
    event_index: dict[str, dict] = {}
    for event in EVENTS:
        coll = build(event, titles_to_ids)
        path = f"collections/{event['slug']}.json"
        with open(path, "w") as f:
            json.dump(coll, f, indent=2, ensure_ascii=False)
            f.write("\n")
        for v in coll["videos"]:
            if v["youtubeId"] not in event_index:
                stamp = {"event": v["event"]}
                if v.get("track"):
                    stamp["track"] = v["track"]
                event_index[v["youtubeId"]] = stamp
        with_speakers = sum(1 for v in coll["videos"] if v.get("speakers"))
        print(f"{path}: {len(coll['videos'])} videos, {with_speakers} with parsed speakers")

    pool = build_pool(event_index)
    with open("collections/ai-engineer-channel.json", "w") as f:
        json.dump(pool, f, indent=2, ensure_ascii=False)
        f.write("\n")
    stamped = sum(1 for v in pool["videos"] if "event" in v)
    print(f"collections/ai-engineer-channel.json: {len(pool['videos'])} videos, {stamped} stamped with events")


if __name__ == "__main__":
    main()

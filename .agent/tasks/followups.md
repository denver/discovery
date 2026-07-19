# Follow-ups and Future Enhancements

Not in MVP scope. Revisit after Wave 3.

## FE-2: Collection drafter (playlist → draft collection file)

Problem: YouTube metadata has NO event field — nothing marks a video as
belonging to "AI Engineer World's Fair 2026." Event attribution is and
must remain editorial (our schema's `event` block). But discovering and
drafting the video list for an event is toil worth automating.

Concept: `discovery draft --playlist <url-or-id> --event "AI Engineer
World's Fair" --year 2026 --city "San Francisco" [--track-from-section]`
emits a draft collection JSON to stdout/file for human review:

- Fetch membership via Data API `playlistItems.list` (1 unit per 50
  videos; playlists are the channel's own event curation — the strongest
  signal that exists). NOT HTML scraping: fragile, ToS-gray, and the API
  already provides this.
- Prefill per entry: youtubeId, `event` from flags, `speakers`/
  `organizations` parsed from the channel's title convention
  ("Talk Title — Speaker Name, Org"), addedAt = now.
- Weaker fallback signals when no playlist exists: channel + publish
  window (`search.list`, 100 units/call — quota-expensive, warn), and
  description text matching. Both produce drafts flagged for review.
- Output must pass `discovery validate`; a `notes` field marks
  low-confidence parses. The human reviews and commits — curation stays
  editorial, per PRD boundaries ("not a YouTube search engine").

Scope notes: fits the existing youtube package (same key, same batching
discipline); new API surface is CLI-only at first. Pairs with FE-1
(single-URL resolve) as the curator toolkit.

Origin: Denver, 2026-07-19 — needed for the real AIE collection, where
event membership isn't in provider metadata.

## FE-1: Resolve endpoint (curator helper)

`GET /api/v1/resolve?url=<youtube-url>` — takes a YouTube URL, returns the
normalized video JSON plus a collection-file-shaped `entry` block the curator
can paste into a collection file.

- Reuses `internal/youtube` normalization (allowed hosts only) + one
  batched fetch.
- Must share the sync endpoint's rate limiting (spends YouTube quota).
- Requires an OpenAPI contract amendment before implementation.
- Origin: Denver, 2026-07-19. Small task, Lane D shape, depends on T06.

# Follow-ups and Future Enhancements

Not in MVP scope. Revisit after Wave 3.

## FE-3: Channel tracking pool (Denver's product model, 2026-07-19)

The channel itself is a collection. Daily sync of every upload from a
channel (e.g. @aiDotEngineer for the World's Fair) into database mode:

- The channel collection surfaces rankings on whatever we choose:
  views, likes, trends (windowed strategies over accrued snapshots).
- Snapshot history enables "new & trending" (views_24h/7d, movers) and
  "all-time best" (views) as different sorts over the same pool.
- The curator (Denver, as admin of his lists) adds editorial tags/
  metadata in his own collection files.
- Curated collections split off from the channel total as sub-set
  collections (e.g. WF26 keynotes). Because videos are deduplicated
  across collections, a curated subset inherits the full snapshot
  history the channel pool already accrued — curation is a view over
  the pool, not a re-ingest.

Interim implementation (live as of 2026-07-19): scripts/daily-sync.sh
regenerates collections/ai-engineer-channel.json from the channel's
uploads playlist via the Data API, then syncs all collections into local
postgres. Run daily (cron or manually). Proper implementation is FE-2's
drafter plus a first-class "channel source" concept.

Known gap: config supports one DISCOVERY_COLLECTION_PATH but the sync
engine accepts multiple paths — the script works around it by syncing
per-file. Config should grow multi-path support.

## FE-7: Admin console (requested 2026-07-23, post-deploy)

Denver's ask: log in and update the playlists/channels without pushing
code. The calculus changed once hosting went live — remote curation now
requires a code push plus cron cycle, which is real friction.

Design direction (keeps FE-4's "no user accounts before multi-tenant"):
- **Auth = the existing DISCOVERY_ADMIN_TOKEN**, not accounts: a /admin
  login form that accepts the token and sets a signed HttpOnly cookie.
  One admin, no user table, no password reset. "Login" in the UX sense,
  token in the security sense.
- **The real work is sources-as-data, not the login.** Today the
  channel/event/mix registries are Python config (CHANNELS, EVENTS,
  MIXES in scripts/). To edit them without code pushes they must move
  to data the cron reads: a `sources` table (or a sources.json the
  admin console edits in the DB). Cron becomes: read registry from DB →
  draft → sync.
- Console v1 scope: list/add/remove tracked channels (by handle),
  playlist→collection mappings, mix rules (sources + window); trigger
  sync now; view last cron result. NOT in v1: per-video editorial
  editing (files/export remain canonical for fine-grained curation).
- Export remains the escape hatch: anything the console writes must
  round-trip through `discovery export` so git can still capture
  curation history.

## FE-8: Recency surfacing (requested 2026-07-23)

Denver's ask: slice videos by date, and surface new videos on big
channels — a new Dan Martell upload will never crack his all-time
top-25, but it must be discoverable.

Three pieces, cheapest first:
1. **"Newest" sort** — a publishedAt-desc ranking strategy. Trivial
   (pure Ranker, no history needed); gives every collection a "latest
   uploads" view immediately.
2. **Date window filter** — `?published=30d|6w|1y` as a filter
   dimension alongside event/track/topic, service-level (we have
   publishedAt on every video). Composes with every sort: "this
   month's uploads by engagement."
3. **"Rising" sort surfaced in the UI** — views_24h/growth already
   built; light up the tabs once hosted cron history matures (2+ days).
   This is the real answer to "new videos need surfacing": rising
   beats newest because it ranks new-AND-resonating.

Note: mixes (FE-5/denvers-radar) already prove the pattern at the
collection level; FE-8 brings time-slicing to every collection as a
query, no new collections needed.

## FE-4: Admin surface, staged (decided 2026-07-19)

Question: should there be login + a web UI for the admin (Denver) to
create collections that write to the db? Decision: not yet. Files + git
+ CLI are the admin surface: versioned, diffable, authenticated by repo
ownership, and the import/export round-trip keeps the file canonical in
db mode. Topics/tags are purely editorial and only come from collection
files; nothing is inferred from YouTube's (boilerplate) tags.

Staged path:
1. Now: files + CLI (`discovery validate/import/export`), FE-2 drafter
   removes the JSON hand-writing pain. No auth exists; keep it that way.
2. Hosted single-admin: DISCOVERY_ADMIN_TOKEN bearer check on two write
   endpoints (POST import collection, POST sync). ~30 lines, no
   sessions, no user table. Remote admin = curl with the token.
3. Multi-curator / multi-tenant "denver's list": real accounts and
   per-collection ownership. A product phase (post-MVP by PRD), not a
   bolt-on. Do not build sessions before this phase exists.

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

### Signal audit (empirical, 2026-07-19, AI Engineer channel)

Checked live against @aiDotEngineer (channel UCLKPca3kwwd-B59HNr-_lvA,
919 videos, 82 playlists):

- **Tags: useless.** Every video sampled (8/8, talks and livestreams
  alike) carries the identical boilerplate set: ai, ai engineer,
  ai engineering, software development, tech, startups, software
  architecture, machine learning. Zero event signal.
- **Description `Event:` block: livestreams only.** Day-stream videos
  (e.g. I2cbIws9j10, "WF26: ...") have a structured block — `Event: AI
  Engineer World's Fair 2026 / Date / Venue: Moscone West` — but 0 of 7
  individual talk uploads have it; only 1 of 7 even mentions the fair in
  its description. Title prefix "WF26:" appears on streams, not talks.
- **Playlists: the real event metadata.** The channel curates precisely
  what we need:
  - `PLDyBmFH9HlVc` — AIE World's Fair 2026 Complete Playlist (36 videos)
  - `PLcfpQ4tk2k0V1LNigteMgExP1rb4Hy8wn` — World's Fair Online Track 2026 (97)
  - Plus per-track playlists ("<Track> @ AI Engineer") usable for track
    tagging, and equivalents for AIE Europe / AIE CODE.

Conclusion: FE-2's playlist-first design is confirmed as the only
reliable mechanism. T18 (Wave 3) should draft the real collection from
PLDyBmFH9HlVc.

## FE-1: Resolve endpoint (curator helper)

`GET /api/v1/resolve?url=<youtube-url>` — takes a YouTube URL, returns the
normalized video JSON plus a collection-file-shaped `entry` block the curator
can paste into a collection file.

- Reuses `internal/youtube` normalization (allowed hosts only) + one
  batched fetch.
- Must share the sync endpoint's rate limiting (spends YouTube quota).
- Requires an OpenAPI contract amendment before implementation.
- Origin: Denver, 2026-07-19. Small task, Lane D shape, depends on T06.

## FE-6: Headless frontend option (Vercel FE consuming the API)

Denver's original mental model — frontend on Vercel, Go API behind it —
is fully supported by the architecture but not enabled: the API sends no
CORS headers (same-origin only today, by design). When/if a separate
branded frontend happens (hosted "denver's list" product phase):

- Add config-gated CORS middleware to internal/api
  (DISCOVERY_CORS_ORIGIN, exact origin, no wildcard) — ~10 lines.
- The server-rendered UI stays: it is the reference consumer and the
  self-hosted default. A JS frontend is additive, never a replacement.
- Until then: server-rendered monolith on Railway is the deliberate
  choice (PRD: "lightweight server-rendered web UI").

Recorded 2026-07-23 after deploy discussion.

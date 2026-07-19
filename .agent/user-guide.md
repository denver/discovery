# Discovery Engine — Operator's Guide

For the human running this project. Last updated: 2026-07-19 (end of Wave 1).
The public-facing README ships in Wave 3 (T19); this guide is yours.

## What this is

Discovery Engine turns a curated JSON (or YAML) list of YouTube videos into a
ranked, filterable, living leaderboard. You supply the editorial facts (who
spoke, what track, what topics). YouTube supplies the provider facts (title,
thumbnail, views, likes, comments). PostgreSQL, when enabled, records what
changes over time. First real deployment: AI Engineer World's Fair talks.

## Where the project stands

| Piece | Status |
|-------|--------|
| Collection schema + validation (JSON/YAML, field-path errors) | Done |
| YouTube URL normalizer + Data API client (batching, retries) | Done |
| Ranking: views, likes, comments, engagement (+ views_24h, views_7d, growth_percent_24h ready for db mode) | Done |
| File-mode store (in-memory + cache file) + config | Done |
| Sync engine (one code path for CLI/API/scheduler) | Done |
| REST API endpoints | Wave 2 |
| Leaderboard web UI | Wave 2 |
| CLI (`discovery validate/sync/serve/import/export`) | Wave 2 |
| PostgreSQL mode, history, rank movement | Wave 2 |
| Docker, README, real AIE collection | Wave 3 |

Until Wave 2 lands, the server only serves `GET /health`. The engine works,
but only tests drive it.

## Your setup tasks

### 1. Get a YouTube Data API key (one time, ~5 minutes)

1. Go to https://console.cloud.google.com/ and create (or pick) a project.
2. APIs & Services → Library → search "YouTube Data API v3" → Enable.
3. APIs & Services → Credentials → Create Credentials → API key.
4. Recommended: restrict the key to the YouTube Data API v3.

Quota math: the free tier gives 10,000 units/day. One sync costs
ceil(videos/50) units. A 200-video collection synced every 6 hours uses
16 units/day. You will not hit the ceiling.

### 2. Configure the environment

```sh
cp .env.example .env
# edit .env: set YOUTUBE_API_KEY and DISCOVERY_COLLECTION_PATH
```

The key stays server-side always. It never appears in API responses, logs,
or error messages (tested).

Database mode is a later decision: leave `DISCOVERY_DATABASE_URL` unset and you run
file mode with a local cache file. Set it and you get history, movers, and
rank movement across restarts. Same binary, no other changes.

## Writing a collection file

Minimum viable file:

```json
{
  "schemaVersion": "1.0",
  "slug": "my-collection",
  "title": "My Collection",
  "videos": [
    { "youtubeUrl": "https://www.youtube.com/watch?v=VIDEO_ID" },
    { "youtubeId": "dQw4w9WgXcQ" }
  ]
}
```

Everything else is optional: `description`, `author`, `source`,
`refreshInterval` ("6h" style), `defaultRanking`, and per-video `speakers`,
`event`, `track`, `topics`, `organizations`, `featured`, `published`,
`titleOverride`, `descriptionOverride`, `addedAt`, `notes`.

Full worked example: `collections/example.json`.
Formal schema: `openapi/collection.schema.json`.

Rules that will bite you if ignored:

- Slugs (collection and speaker) are lowercase-hyphen only: `swyx`, not `Swyx`.
- URLs must be youtube.com or youtu.be hosts. Vimeo and friends are rejected
  by design; the engine never fetches arbitrary URLs.
- The same video twice in one file is a validation error.
- `published: false` hides a video from listings without deleting the entry.
- Validation errors come back with exact paths: `videos[3].youtubeUrl: host
  "vimeo.com" is not YouTube`. Fix what it names.

## Ranking, in one minute

- `views`, `likes`, `comments`: the raw counter, descending.
- `engagement`: `likes + comments × 3`.
- Ties break deterministically: newer publish date first, then video ID.
- Videos YouTube won't return stats for rank last, never crash.
- Db-mode strategies (`views_24h`, `views_7d`, `growth_percent_24h`,
  `rank_change_24h`) need snapshot history; in file mode the API returns a
  clear 501.
- Movement arrows compare against the previous sync run (file mode) or the
  snapshot history (db mode). New videos show no arrow, by design.

## Commands you can run today

```sh
make build          # compile everything
make test           # full test suite
make lint           # vet + gofmt
make lint-openapi   # validate the API contract (needs npx)
make run            # serve :8080 (only /health until Wave 2)
```

After Wave 2:

```sh
discovery validate ./collections/my-list.json   # check a file, exit non-zero on problems
discovery sync                                  # one-shot fetch + rank (cron-friendly)
discovery serve                                 # API + leaderboard
discovery import ./file.json                    # file → postgres
discovery export my-collection                  # postgres → file
```

And the API per `openapi/openapi.yaml`: `/api/v1/collections/{slug}/rankings?sort=views&topic=agents&limit=25`, etc.

## How this build runs (your role in it)

The build fans out to parallel agents in waves. Contracts were frozen first
(Wave 0) so agents build against interfaces, never each other's code. Each
lane works in an isolated git worktree, must land green (`build`/`lint`/
`test`, `-race` where it matters), and merges back to main between waves.

Where things live:

- `.agent/prd/prd.md` — the spec
- `.agent/tasks/plan.md` — all 20 tasks, acceptance criteria, dependency graph
- `.agent/tasks/todo.md` — live checklist, updated as lanes land
- `.agent/tasks/followups.md` — deferred ideas (FE-1: the resolve endpoint)
- `.agent/architecture/` — ADR-001 and the db schema notes

You are the checkpoint authority. The process stops for you at:

1. **Contract freezes.** Changing anything in `internal/collections` types,
   the Store/Ranker interfaces, the OpenAPI contract, or migrations while
   lanes are running is the one way to make parallel agents collide. Ask
   for a coordination stop instead; they're cheap between waves.
2. **Wave boundaries.** Review the summary, say go.
3. **Anything irreversible or external** (deploys, publishing, real API
   keys).

Open items needing you right now:

- [ ] Put a real `YOUTUBE_API_KEY` in `.env` so the smoke sync can run.
- [ ] Say go on Wave 2 (API, leaderboard, CLI, postgres — four parallel lanes).

## Troubleshooting

- **Validation fails**: the error names the exact field path. The schema
  reference is `openapi/collection.schema.json`.
- **Sync reports failed IDs**: those videos are private, deleted, or the ID
  is wrong. The rest of the run completed; failures are listed in the sync
  result, never fatal.
- **Quota errors (403)**: the client stops retrying immediately and says so.
  Check the key's restrictions in Google Cloud Console.
- **Weird stale data in file mode**: delete `.discovery-cache.json` and
  re-sync. The cache is disposable by design; the collection file and
  YouTube are the sources of truth.
- **Corrupt cache**: ignored automatically with a logged warning; the next
  sync rebuilds it.

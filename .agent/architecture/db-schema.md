# Database Schema Notes

Migrations: `migrations/000001_init.*.sql` (golang-migrate file naming).
Verified against PostgreSQL 14: up/down clean, constraints tested.

## Model

- `collections` — one row per collection file. Editorial header fields flattened (author_*, source_*).
- `videos` — one row per unique YouTube ID (`UNIQUE` + format `CHECK`), shared across collections. Provider facts plus denormalized current stats for cheap reads; `video_snapshots` is the historical record.
- `collection_videos` — membership with per-collection editorial metadata (overrides, track, event, featured, published, notes). `position` preserves source-file ordering; unique per collection and deferrable so re-imports can reorder in one transaction.
- `speakers`, `topics`, `organizations` + join tables — global entities keyed by slug/name, joined to videos per the PRD table list.
- `video_snapshots` — append-only metric observations. UPDATE blocked by trigger; DELETE allowed for future retention policies.
- `rank_snapshots` — computed positions per (collection, strategy) at each sync, append-only.

## Rank movement derivation

Movement over window W for strategy S in collection C:
compare the latest `rank_snapshots` row per video with the closest row at or before `now - W`. Videos with no earlier row are "new" (previousPosition null). Windowed metric strategies (views_24h, growth_percent_24h) do the same against `video_snapshots`, using delta = latest - baseline, with zero/missing baselines scored as 0 (never divide by zero).

We store rank snapshots rather than deriving ranks from metric snapshots at query time so movement stays cheap and stable even if a strategy's formula changes between deploys.

## Tradeoffs accepted

- Speakers/topics/orgs attach to `videos` globally (per PRD), not to membership. If two collections list different speakers for the same video, last import wins. Revisit if it bites.
- Current stats denormalized on `videos`; snapshot append and column update happen in the same sync transaction.

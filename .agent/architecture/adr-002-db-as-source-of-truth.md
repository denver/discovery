# ADR-002: Database as source of truth; files become interchange

Date: 2026-07-23
Status: proposed (Denver to approve)
Supersedes: the file-canonical stance in ADR-001 and FE-4's "files are
the admin surface" (kept for engine self-hosters; changed for the
hosted instance's operating model).

## Context

The original model: collection files in git are editorial truth; the
database reflects them; the source registries (which channels, which
event playlists, which mix rules) are Python constants baked into the
cron image. That made git the admin surface — right for a laptop-run
MVP, wrong for a hosted product. Live friction, experienced twice in
one day: adding channels required code pushes; dropping a collection
is impossible (no delete exists anywhere); the placeholder collection
can't be removed without hand SQL.

Denver's direction: the DB is the source of truth so collections can be
dropped and lists curated without deploys.

## Decision

1. **Sources become rows.** New table:

   ```sql
   CREATE TABLE sources (
     id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
     kind       TEXT NOT NULL CHECK (kind IN ('channel','event','mix')),
     slug       TEXT NOT NULL UNIQUE,   -- collection it produces
     enabled    BOOLEAN NOT NULL DEFAULT TRUE,
     config     JSONB NOT NULL,         -- kind-specific, mirrors today's dicts:
                                        -- channel: {"handle": "boundaryml", "title_override": null}
                                        -- event:   {"event": {...}, "playlists": [[title, track], ...]}
                                        -- mix:     {"sources": [...], "window_days": 42, "min_minutes": 10}
     created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
     updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
   );
   ```

   The daily pipeline reads this table; the Python CHANNELS/EVENTS/
   MIXES constants are deleted in the same change (one truth, no
   transition period). Disabling a source stops refreshing its
   collection without deleting anything.

2. **Collections get a lifecycle.** `discovery collections rm <slug>`
   (and later an admin API) deletes a collection and its memberships.
   Videos and snapshots are shared assets and survive — history is
   never destroyed by curation changes. Dropping a source offers to
   drop its collection.

3. **Files are demoted to interchange, not truth.** Exactly what the
   PRD always required: importable and exportable. `discovery import`
   seeds or restores; `discovery export` snapshots curation to JSON for
   backup or git archival. The repo's collections/ directory becomes
   examples + seeds, not the live catalog.

4. **Curation edits happen against the DB** through a widening set of
   doors, in order: SQL/Railway data panel (today), CLI verbs
   (`discovery sources add-channel boundaryml`, `sources rm`,
   `collections rm`), then the FE-7 admin console as a UI over the
   same tables — token-cookie login, no accounts (FE-4 stance holds).

## What we consciously give up

- **Git as automatic curation history.** Mitigations: sources rows
  carry timestamps; a periodic `discovery export --all` snapshot (CI
  or cron) can re-create the git trail as an archive rather than the
  truth. Accepted: the audit story is thinner until the console adds
  an audit log.
- **"Repo clone = full instance" simplicity.** A fresh instance now
  needs a seeded sources table; `discovery import` + a seed file keep
  this a one-command story.

## What self-hosters keep

File mode and file-canonical operation remain fully supported for the
OSS engine — this ADR changes the hosted instance's operating model,
not the engine's capabilities. The sources pipeline is db-mode only.

## Phasing

- **A1**: migration 000003 (sources), seed from current configs,
  drafters read the table, constants deleted.
- **A2**: CLI verbs: `sources ls|add-channel|add-mix|rm`,
  `collections rm`. Railway usage: `railway run discovery sources ...`
  or the Railway data panel.
- **A3 (FE-7)**: admin console = UI over the same operations plus
  sync-now and last-run status.

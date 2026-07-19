# ADR-001: Operating modes, storage abstraction, and frozen contracts

Date: 2026-07-19
Status: accepted

## Context

Discovery Engine must run usefully with no database (file mode) and gain
history/analytics with PostgreSQL (database mode). The MVP is built by
parallel agents that must not depend on each other's code.

## Decisions

1. **One `Store` interface, two implementations.** `internal/collections.Store`
   is the persistence contract. File mode: in-memory + JSON cache file,
   retains only the previous sync run (enough for previousPosition), returns
   `ErrHistoryUnavailable` from `History`. Database mode: PostgreSQL with
   append-only `video_snapshots`/`rank_snapshots`. Mode selected solely by
   `DISCOVERY_DATABASE_URL` presence. Both implementations pass one shared conformance
   test suite.

2. **Contract-first fan-out.** Frozen after Wave 0: the collection JSON
   Schema (`openapi/collection.schema.json`), the domain + normalized model
   types (`internal/collections`), the `Store` interface, the
   `Ranker`/`History` interfaces (`internal/rankings`), the OpenAPI contract
   (`openapi/openapi.yaml`), and the DB schema (`migrations/`). Changes to
   these require stopping parallel lanes.

3. **Ranking strategies are pure.** `Ranker.Score(video, history, now)` does
   no I/O and takes an injected `now`. Windowed strategies read history
   through the `History` interface and fail with `ErrHistoryRequired` in
   file mode, which the API maps to 501.

4. **One sync path.** CLI `discovery sync`, `POST /api/v1/sync`, and the
   scheduler all invoke the same sync engine: load file, validate, resolve
   IDs, dedupe, batch-fetch YouTube (50 IDs/request), upsert provider data,
   record snapshots + rankings. Per-video failures are logged and reported,
   never fatal to the run.

5. **Rank snapshots stored, not derived.** See db-schema.md. Stability of
   history beats storage cost at this scale.

6. **Dependencies.** Standard library first: net/http, html/template,
   database/sql. Approved deps: gopkg.in/yaml.v3 (YAML collections), a
   postgres driver (pgx via database/sql, added in T14), golang-migrate
   (embedded FS, added in T14 if a hand-rolled runner proves uglier).

## Consequences

- Lanes A–H can build in parallel against interfaces only.
- File mode ships alone if the postgres lane slips.
- `previousPosition` semantics differ by mode (previous run vs. time
  window) but share one API shape; documented on the Store interface.

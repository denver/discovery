# Implementation Plan: Discovery Engine MVP

Source spec: `.agent/prd/prd.md`

## Overview

Discovery Engine is a Go API + server-rendered leaderboard over curated YouTube collections. File mode (no database) is the default; PostgreSQL is an optional mode adding snapshots, history, and rank movement. This plan breaks the MVP into 20 tasks across 5 phases, structured so a contract-first Phase 0 unlocks wide parallel fan-out in Phases 1–3.

## Architecture Decisions (to be recorded as ADRs in `.agent/architecture/`)

- **Storage abstraction**: a single `Store` interface with two implementations (in-memory + file cache, PostgreSQL). Mode selection is config-driven (`DATABASE_URL` present → db mode). This is the load-bearing decision that lets file mode and db mode develop in parallel.
- **Contract-first**: JSON Schema for collections, OpenAPI for the API, and the `Store`/`Ranker` Go interfaces are frozen in Phase 0. All parallel agents build against these contracts, never against each other's code.
- **Ranking as pure functions**: `Ranker` interface takes `[]Video` + context, returns scored order. No I/O in strategies. Time-window strategies (views_24h etc.) receive snapshot data through the same interface; in file mode they are simply unavailable.
- **Standard library first**: `net/http` + `html/template`, `database/sql` + `pgx` driver, `golang-migrate` or embedded SQL migrations. No web framework, no ORM.
- **Sync is one code path**: CLI `discovery sync`, POST `/api/v1/sync`, and the internal scheduler all call the same sync engine.

## Dependency Graph

```
Phase 0 (sequential, single agent)
  T01 scaffold ──► T02 domain types + JSON Schema ──► T03 contracts (OpenAPI + Store/Ranker interfaces)
                                                └───► T04 DB schema + migrations (parallel with T03)

Phase 1 (3 parallel lanes, after T03)
  Lane A: T05 URL normalization ──► T06 YouTube client
  Lane B: T07 ranking strategies
  Lane C: T08 memory store + file cache ──► T09 sync engine (needs T06, T08)

Phase 2 (4 parallel lanes, after Phase 1)          Phase 3 (1 lane, after T04 + T08)
  Lane D: T10 read API ──► T11 sync API              Lane H: T14 postgres store
  Lane E: T12 web leaderboard                                 ──► T15 snapshots
  Lane F: T13 CLI                                             ──► T16 movement + history API
  Lane G: (scheduler folded into T11)                         (runs concurrently with Phase 2)

Phase 4 (after everything)
  T17 Docker ──► T18 example collection ──► T19 README/docs ──► T20 E2E QA
```

## Parallelization Map for Subagent Fan-Out

| Wave | Agents | Tasks | Blocked by |
|------|--------|-------|------------|
| 0 | 1 | T01 → T02 → T03, T04 | nothing |
| 1 | 3 | Lane A (T05→T06), Lane B (T07), Lane C (T08) | wave 0 |
| 1.5 | 1 | T09 (sync engine) | T06 + T08 |
| 2 | 4 | Lane D (T10→T11), Lane E (T12), Lane F (T13), Lane H (T14→T15→T16) | wave 1.5 (Lane H only needs T04+T08) |
| 3 | 1–2 | T17→T18→T19, then T20 | wave 2 |

Rules for parallel agents:

- Agents never edit files outside their lane's package list (see per-task "Files likely touched").
- Shared types live in `internal/collections` (domain) and are frozen after Phase 0; changes to them require a coordination stop.
- Every lane lands with passing `go build ./... && go test ./...` and `go vet ./...`.
- Lane H (postgres) is the long pole; start it as early as its deps allow, concurrently with Phase 2.

---

## Task List

### Phase 0: Contracts and Foundation (sequential, one agent)

## Task 01: Repository scaffold

**Description:** Initialize the Go module and repository skeleton: directory tree per PRD, `go.mod`, `Makefile` (build/test/lint/run), `.env.example`, `.gitignore`, a `cmd/server` main that serves `GET /health`, and a GitHub Actions workflow running build/test/vet.

**Acceptance criteria:**
- [ ] `make build test lint` all pass on a fresh clone
- [ ] `go run ./cmd/server` serves `GET /health` returning `{"status":"ok"}`
- [ ] Repo tree matches the PRD structure (empty packages have doc.go placeholders)

**Verification:** `make build && make test && curl localhost:8080/health`

**Dependencies:** None
**Files likely touched:** `go.mod`, `Makefile`, `.env.example`, `.github/workflows/ci.yml`, `cmd/server/main.go`, package placeholders
**Estimated scope:** M

## Task 02: Collection schema, domain types, validation

**Description:** Define the versioned collection JSON Schema (`openapi/collection.schema.json`), the Go domain types (`Collection`, `VideoEntry`, `Speaker`, `Event`), and a validator that loads JSON (and YAML) files and returns actionable errors with exact field paths (`videos[3].youtubeUrl: missing`). Required fields per PRD: schemaVersion, slug, title, videos; each video needs youtubeUrl or youtubeId.

**Acceptance criteria:**
- [ ] Valid example file parses into domain types; invalid files produce field-path errors
- [ ] JSON Schema document published in repo and matches the validator's behavior
- [ ] Table-driven tests cover missing required fields, bad URLs, bad dates, unknown schemaVersion

**Verification:** `go test ./internal/collections/...`

**Dependencies:** T01
**Files likely touched:** `internal/collections/types.go`, `internal/collections/validate.go`, `internal/collections/validate_test.go`, `openapi/collection.schema.json`, `collections/example.json` (minimal placeholder)
**Estimated scope:** M

## Task 03: API contract and core interfaces

**Description:** Write the OpenAPI 3 contract for all v1 endpoints (including db-mode-only history/movers, marked as conditional), the normalized API video model as Go types, and the two core interfaces: `Store` (get/list collections, videos, upsert metadata, record/read snapshots) and `Ranker` (name + rank). Interfaces must be satisfiable by both file mode and postgres mode. This freezes the contracts every parallel lane builds against.

**Acceptance criteria:**
- [ ] `openapi/openapi.yaml` covers all PRD endpoints, query params, and the normalized video model
- [ ] `Store` and `Ranker` interfaces compile and have doc comments defining semantics (including snapshot behavior in file mode: best-effort previous-run only)
- [ ] OpenAPI validates with a linter (e.g. `vacuum` or `redocly` via Makefile target, or documented manual check)

**Verification:** `go build ./...`; OpenAPI lint passes

**Dependencies:** T02
**Files likely touched:** `openapi/openapi.yaml`, `internal/collections/model.go` (normalized model), `internal/collections/store.go`, `internal/rankings/ranker.go`
**Estimated scope:** M

## Task 04: PostgreSQL schema and migrations

**Description:** Design and write SQL migrations for collections, videos, collection_videos, video_snapshots, speakers, video_speakers, topics, video_topics, organizations, video_organizations. Enforce PRD constraints: videos unique per YouTube ID and shared across collections, append-only snapshots, membership preserves editorial ordering/metadata. Not wired to code yet — schema only, reviewed against the Store interface.

**Acceptance criteria:**
- [ ] Migrations apply cleanly up and down against a fresh postgres (via docker)
- [ ] Constraints: unique video per youtube_id, FK integrity, snapshot table has no update path (documented convention + no updated_at)
- [ ] An `.agent/architecture/db-schema.md` note explains the model and the rank-movement derivation approach

**Verification:** `docker run postgres` + migrate up/down cleanly

**Dependencies:** T02 (can run parallel with T03)
**Files likely touched:** `migrations/*.sql`, `.agent/architecture/db-schema.md`
**Estimated scope:** M

### Checkpoint: Contracts frozen
- [ ] Build and tests pass; OpenAPI, JSON Schema, Store/Ranker interfaces, and DB schema reviewed by Denver before fan-out
- [ ] Any later change to `internal/collections` types requires stopping parallel lanes

### Phase 1: Core Engine (3 parallel lanes)

## Task 05 (Lane A): YouTube URL normalization

**Description:** Pure functions resolving all YouTube URL forms (watch, youtu.be, shorts, embed, with params) and raw IDs into canonical 11-char video IDs, with strict rejection of non-YouTube hosts (PRD security: no arbitrary URL fetching).

**Acceptance criteria:**
- [ ] All common URL forms resolve to the correct ID; invalid/foreign URLs return typed errors
- [ ] Table-driven tests with ≥15 cases including hostile inputs

**Verification:** `go test ./internal/youtube/...`

**Dependencies:** T03
**Files likely touched:** `internal/youtube/normalize.go`, `internal/youtube/normalize_test.go`
**Estimated scope:** S

## Task 06 (Lane A): YouTube Data API client

**Description:** Client for YouTube Data API v3 `videos.list` fetching snippet, contentDetails, statistics. Batches up to 50 IDs per request, dedupes IDs within a sync, request timeouts, bounded exponential backoff on transient failures, per-video error collection (missing/private videos logged, not fatal). API key from config only, never logged.

**Acceptance criteria:**
- [ ] Batched fetch returns normalized metadata + stats for N videos in ⌈N/50⌉ requests
- [ ] Transient 5xx/429 retried with backoff; permanent errors surface per-video
- [ ] Tests use an httptest fake server; no real API calls in tests; key never appears in logs or errors

**Verification:** `go test ./internal/youtube/...`

**Dependencies:** T05
**Files likely touched:** `internal/youtube/client.go`, `internal/youtube/client_test.go`
**Estimated scope:** M

## Task 07 (Lane B): Ranking strategies

**Description:** Implement `Ranker` strategies: views, likes, comments, engagement (`likes + comments*3`, formula documented in code and later README). Registry maps strategy name → Ranker so the API/CLI resolve by string. Deterministic tie-breaking (publishedAt, then ID). Safe handling of missing stats and zero denominators. Stub registrations for db-mode strategies (views_24h, views_7d, growth_percent_24h, rank_change_24h) returning a typed "requires history" error in file mode.

**Acceptance criteria:**
- [ ] Four MVP strategies rank correctly with deterministic ties; unknown strategy → typed error
- [ ] Videos with missing statistics rank last, never panic
- [ ] Adding a strategy requires only a new file + registry entry (documented in package doc)

**Verification:** `go test ./internal/rankings/...`

**Dependencies:** T03
**Files likely touched:** `internal/rankings/*.go`, `internal/rankings/*_test.go`
**Estimated scope:** M

## Task 08 (Lane C): In-memory store and file cache

**Description:** Implement `Store` for file mode: in-memory state loaded from the collection file, YouTube metadata cached to a local JSON cache file (path configurable) so restarts don't refetch. Keeps the previous sync's stats/ranks in the cache to support `previousPosition` without a database. Concurrency-safe. Plus `internal/config`: env-var loading (`DISCOVERY_COLLECTION_PATH`, `YOUTUBE_API_KEY`, `DATABASE_URL`, port, refresh interval) with validation.

**Acceptance criteria:**
- [ ] Store round-trips: load collection → upsert metadata → read videos/rankings; safe under concurrent reads during sync
- [ ] Cache file survives restart (metadata served without YouTube calls) and tolerates corruption (falls back to refetch)
- [ ] Config errors are actionable (missing key names named)

**Verification:** `go test ./internal/collections/... ./internal/config/...`

**Dependencies:** T03
**Files likely touched:** `internal/collections/memstore.go`, `internal/collections/cache.go`, `internal/config/config.go`, tests
**Estimated scope:** M

## Task 09: Sync engine (joins Lanes A + C)

**Description:** The single sync path used by CLI, API, and scheduler: load + validate collection → normalize IDs → dedupe → batch-fetch YouTube → upsert into Store → compute rankings → record snapshot (store-dependent). Partial failure tolerated: failed video IDs logged and reported in the sync result, sync completes.

**Acceptance criteria:**
- [ ] One sync run fetches each unique video exactly once across collections
- [ ] Sync result reports counts: fetched, failed (with IDs), duration
- [ ] Integration test with fake YouTube server + memory store passes

**Verification:** `go test ./internal/collections/...` (or a `internal/sync` package)

**Dependencies:** T06, T08
**Files likely touched:** `internal/collections/sync.go` (or `internal/sync/`), tests
**Estimated scope:** M

### Checkpoint: Core engine
- [ ] `go test ./...` green across all lanes
- [ ] A throwaway main can sync a real collection with a real API key and print ranked titles (manual smoke test)

### Phase 2: Surfaces (4 parallel lanes) — runs concurrently with Phase 3

## Task 10 (Lane D): Read API

**Description:** Implement the OpenAPI read endpoints over the Store: health, list collections, get collection, list videos, rankings — with sort/topic/track/speaker/limit/offset query params. JSON errors with proper status codes. Responses match the normalized model exactly.

**Acceptance criteria:**
- [ ] All GET endpoints return contract-shaped responses; filters and pagination compose correctly
- [ ] Unknown slug/ID → 404 with JSON error; bad params → 400
- [ ] Handler tests against the memory store cover happy path + each filter

**Verification:** `go test ./internal/api/...`; spot-check against `openapi/openapi.yaml`

**Dependencies:** T07, T08, T09
**Files likely touched:** `internal/api/*.go`, `internal/api/*_test.go`, `cmd/server/main.go` (wiring)
**Estimated scope:** M

## Task 11 (Lane D): Sync endpoint, rate limiting, scheduler

**Description:** `POST /api/v1/sync` triggering the sync engine with a simple in-process rate limit (e.g. 1 concurrent sync, min interval between manual syncs). Scheduler goroutine honoring the collection/config `refreshInterval`, with clean shutdown. One-shot sync remains available via CLI for external schedulers.

**Acceptance criteria:**
- [ ] POST /sync runs a sync and returns the sync result; concurrent/too-frequent requests → 429
- [ ] Scheduler fires on interval and stops cleanly on shutdown (test with short interval)
- [ ] API key never appears in any response or log

**Verification:** `go test ./internal/api/... ./internal/scheduler/...`

**Dependencies:** T10
**Files likely touched:** `internal/api/sync.go`, `internal/scheduler/scheduler.go`, tests
**Estimated scope:** S–M

## Task 12 (Lane E): Server-rendered leaderboard

**Description:** `html/template` leaderboard page per collection: title/description, last-refreshed timestamp, ranking controls, topic/track filters, ranked cards (rank prominent, thumbnail, title, speakers, channel, published date, view/like/comment counts, movement arrow when history exists, YouTube link). Product Hunt feel, responsive, no JS framework (vanilla JS acceptable for filter controls). Talks to the Store/service layer directly (same process), not over HTTP.

**Acceptance criteria:**
- [ ] `/` lists collections; `/c/{slug}` renders the full leaderboard with working sort + filter controls (URL-param driven, server-rendered)
- [ ] Rank movement renders when previousPosition exists, absent otherwise
- [ ] Responsive at 375px and 1280px; all external links go to youtube.com

**Verification:** `go test ./internal/web/...` (template render tests) + manual browser check

**Dependencies:** T07, T08, T09
**Files likely touched:** `internal/web/*.go`, `web/templates/*.html`, `web/static/*`
**Estimated scope:** M

## Task 13 (Lane F): CLI

**Description:** `cmd/discovery` with subcommands: `validate <file>`, `sync`, `serve`, `import <file>`, `export <slug>`. `validate` prints field-path errors and exits non-zero on invalid. `sync` is the one-shot for external cron. `import`/`export` are file-mode no-ops-with-message until db mode lands (Lane H wires them). Stdlib `flag`-based, no cobra unless justified.

**Acceptance criteria:**
- [ ] `discovery validate` on good/bad files behaves correctly with exit codes
- [ ] `discovery sync` performs one sync and prints the result summary
- [ ] `discovery serve` runs the server (same wiring as cmd/server)
- [ ] `--help` output documents every subcommand

**Verification:** `go test ./cmd/discovery/...` + manual runs

**Dependencies:** T09 (and T10 wiring for serve)
**Files likely touched:** `cmd/discovery/*.go`
**Estimated scope:** M

### Phase 3 (Lane H, sequential within lane; starts after T04 + T08, runs alongside Phase 2)

## Task 14 (Lane H): PostgreSQL store

**Description:** Implement `Store` against PostgreSQL using the T04 schema: import/sync collection into tables, dedupe videos across collections, membership preserves editorial ordering, migrations run on startup (or via CLI flag). Mode selected by `DATABASE_URL` presence.

**Acceptance criteria:**
- [ ] Postgres store passes the same store test suite as the memory store (shared conformance tests)
- [ ] A video in two collections exists once in `videos` with two membership rows
- [ ] Migrations auto-apply idempotently on startup

**Verification:** `go test ./internal/database/...` against dockerized postgres (guarded by env/testcontainer)

**Dependencies:** T04, T08 (interface + conformance tests)
**Files likely touched:** `internal/database/*.go`, store conformance test file
**Estimated scope:** M–L (split if it grows: import vs. read paths)

## Task 15 (Lane H): Metric snapshots and import/export

**Description:** Append-only `video_snapshots` written on every sync in db mode; rank snapshots derived or stored per ADR. Wire CLI `import`/`export`: import a collection file into postgres, export a slug back to canonical JSON (round-trip safe for editorial fields).

**Acceptance criteria:**
- [ ] Each sync appends one snapshot per video; nothing updates old snapshots
- [ ] `import` then `export` round-trips a collection file (editorial fields preserved)
- [ ] Sync in db mode also refreshes the API-served current stats

**Verification:** `go test ./internal/database/...`; manual import/export round-trip

**Dependencies:** T14
**Files likely touched:** `internal/database/snapshots.go`, `cmd/discovery/import.go`, `cmd/discovery/export.go`, tests
**Estimated scope:** M

## Task 16 (Lane H): Rank movement, history, and windowed strategies

**Description:** Db-mode analytics: `GET /videos/{id}/history`, `GET /collections/{slug}/movers`, ranking strategies views_24h, views_7d, growth_percent_24h, rank_change_24h computed from snapshots. Safe handling of videos with <2 snapshots and zero-baseline growth (no division by zero; documented behavior).

**Acceptance criteria:**
- [ ] History endpoint returns time-ordered snapshots; movers returns biggest rank changes over a window
- [ ] Windowed strategies rank correctly against seeded snapshot fixtures; new videos handled per documented rule
- [ ] File mode returns 501/clear error for these endpoints, matching OpenAPI conditional marking

**Verification:** `go test ./internal/database/... ./internal/rankings/... ./internal/api/...`

**Dependencies:** T15, T10
**Files likely touched:** `internal/rankings/windowed.go`, `internal/api/history.go`, `internal/database/queries.go`, tests
**Estimated scope:** M

### Checkpoint: Feature-complete
- [ ] File mode: sync + API + web + CLI work end-to-end with a real API key
- [ ] Db mode: snapshots accumulate, movement renders on the leaderboard
- [ ] Review with Denver before packaging

### Phase 4: Packaging (mostly sequential, one agent)

## Task 17: Docker and Compose

**Description:** Multi-stage Dockerfile (small static binary), `docker-compose.yml` where plain `docker compose up` runs file mode and `--profile postgres` adds the database with `DATABASE_URL` wired. `.env.example` covers both modes.

**Acceptance criteria:**
- [ ] `cp .env.example .env && docker compose up` serves the leaderboard in file mode (no postgres container started)
- [ ] `docker compose --profile postgres up` runs db mode with migrations applied
- [ ] Image builds in CI

**Verification:** Run both compose commands locally

**Dependencies:** T11, T12, T13, T16
**Files likely touched:** `Dockerfile`, `docker-compose.yml`, `.env.example`, `.dockerignore`, CI workflow
**Estimated scope:** S–M

## Task 18: AI Engineer World's Fair example collection

**Description:** A realistic `collections/example.json` with multiple real AIE talks, multiple speakers, topics, tracks, event/year/city — exercising every schema feature. No AIE-specific logic in the engine (verify by grep).

**Acceptance criteria:**
- [ ] `discovery validate collections/example.json` passes
- [ ] Example demonstrates: multi-speaker videos, topics, tracks, featured flag, overrides
- [ ] Sync against the real API succeeds for the example (videos are live)

**Verification:** validate + real sync + leaderboard renders

**Dependencies:** T13
**Files likely touched:** `collections/example.json`
**Estimated scope:** S

## Task 19: README and documentation

**Description:** README covering all 10 PRD points (what it is/isn't, API key how-to, collection authoring, file mode, db mode, ranking + adding strategies, deployment, hosted branding note) with leaderboard screenshot(s) or placeholders. Link OpenAPI and JSON Schema docs.

**Acceptance criteria:**
- [ ] All 10 PRD README requirements present
- [ ] A newcomer can go from clone → running leaderboard following only the README (test the steps literally)
- [ ] Ranking formulas documented and match code

**Verification:** Follow the README steps in a clean directory

**Dependencies:** T17, T18
**Files likely touched:** `README.md`, `docs/` (optional)
**Estimated scope:** M

## Task 20: End-to-end QA pass

**Description:** Full pass against MVP acceptance criteria: both modes, all endpoints vs OpenAPI, both ranking modes on the web UI, filters, Docker flows, CLI commands, and the security checklist (key never client-side, rate limit works, no arbitrary URL fetch). File issues as follow-up tasks; fix blockers.

**Acceptance criteria:**
- [ ] Every PRD "MVP Acceptance Criteria" bullet checked and evidenced
- [ ] Security checklist verified
- [ ] Known gaps recorded in `.agent/tasks/followups.md`

**Verification:** Checklist with evidence (commands + output)

**Dependencies:** T19
**Estimated scope:** M

### Checkpoint: Complete
- [ ] All MVP acceptance criteria met, ready for public repo

---

## Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Contract drift between parallel lanes | High | Phase 0 freeze; shared types only change at coordination stops; conformance test suite shared by both stores |
| YouTube API quota limits during dev | Med | Fake server in all tests; batch 50/request; cache file avoids refetch; one real-key smoke test per checkpoint |
| Postgres lane (H) becomes the long pole | Med | Start H at wave 2 alongside Phase 2; file mode is shippable without it |
| `previousPosition` semantics differ between modes | Med | Define in T03 Store docs: file mode = previous sync run; db mode = previous snapshot; UI treats both identically |
| Web UI scope creep (Product Hunt polish) | Med | MVP is server-rendered + URL-param filters; polish pass only in T20 follow-ups |
| Merge conflicts from parallel agents | Med | Package-level ownership per lane; wiring files (`cmd/server/main.go`) touched only by Lane D and at checkpoints |

## Open Questions (defaults chosen, flag if wrong)

- **YAML support**: PRD mentions JSON or YAML. Default: JSON in MVP, YAML accepted via a small decoder in T02 (`goccy/go-yaml` or convert-on-load). Cut YAML if it adds friction.
- **Migration tool**: default `golang-migrate` with embedded FS. Alternative: hand-rolled versioned SQL runner (fewer deps).
- **Repo name**: PRD says `discovery-engine`, local folder is `discovery`. Default: keep module `github.com/denver/discovery`.

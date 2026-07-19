# Handoff: Discovery Engine — Wave 3 (Packaging)

You are picking up a Go project that is feature-complete and needs its
packaging pass: Docker, the real example collection, the public README,
and a final QA sweep. Waves 0–2 (T01–T16) are done, merged to main, and
verified. Read this file fully before touching anything.

## Orient yourself (read in this order)

1. `.agent/prd/prd.md` — the product spec (note: env var is
   DISCOVERY_DATABASE_URL, renamed from the original DATABASE_URL after a
   live collision with an ambient variable; the PRD text is already updated)
2. `.agent/tasks/plan.md` — all 20 tasks; you are doing T17–T20
3. `.agent/tasks/todo.md` — live checklist; keep it updated
4. `.agent/architecture/adr-001-contracts-and-modes.md` and `db-schema.md`
5. `.agent/tasks/followups.md` — deferred items; do NOT pull them into scope
6. `.agent/user-guide.md` — the operator's guide for Denver (update it if
   your work changes any commands or workflows)

## Current state

- All 11 packages green: `make build && make lint && make test` and
  `go test -race ./...`. Postgres tests run against local PostgreSQL 14
  (brew, default socket) and skip cleanly when unavailable.
- Working demo: `go run ./cmd/discovery serve` → http://localhost:8080/c/test-videos
  serves 3 real AI Engineer talks from `collections/test.json`.
- `.env` exists locally with Denver's real YouTube API key. It is
  gitignored. NEVER print it, commit it, or echo its values.
- Repo pushed to github.com/denver/discovery (currently PRIVATE).
- Deps: gopkg.in/yaml.v3, jackc/pgx/v5 (stdlib driver). Nothing else.
  Keep it that way unless a task genuinely demands more.

## Hard rules (violations burned trust once already — do not repeat)

1. **Process hygiene.** If you background a server, verify it died with
   `lsof -nP -iTCP:8080 -sTCP:LISTEN` before finishing the task. Kill by
   PID from lsof, never by shell job (`kill %1` leaves `go run` children
   orphaned). NO task ends with a process of yours still listening.
2. **Port 8080, always.** A port conflict means a stale process to kill,
   never a reason to change ports. Do not edit PORT in Denver's .env.
3. **Prefer handing Denver the run command** over running servers
   yourself. He wants to learn how his app operates. Ask him to run it
   and tell you what he sees when a human check matters.
4. **Frozen contracts.** Do not modify: `internal/collections` types/
   interfaces, `internal/rankings` interfaces, `openapi/openapi.yaml`
   response shapes, existing `migrations/*.sql`. Additive migrations
   (000003+) are allowed with justification. If a contract blocks you,
   stop and ask Denver.
5. **Secrets.** YouTube key and any database URL never appear in code,
   logs, errors, compose files, or committed docs. The codebase already
   enforces this pattern; match it.
6. Config reads process env first, then `./.env` (see
   `internal/config/dotenv.go`). `DISCOVERY_DATABASE_URL` (namespaced) is
   the ONLY database-mode switch; plain `DATABASE_URL` is deliberately
   ignored — preserve that in compose/docs.

## The work

### T17: Docker and Compose
Multi-stage Dockerfile (static binary, small image), `docker-compose.yml`
where `docker compose up` = file mode (NO postgres container starts) and
`docker compose --profile postgres up` = database mode with
DISCOVERY_DATABASE_URL wired to the postgres service and migrations
auto-applied (the app does this on startup). `.env.example` already
documents both modes. Mount `./collections` so users edit files without
rebuilds. Add an image-build step to CI. Acceptance: both compose
commands work from a clean checkout following only the README.
Note: Denver's local Docker daemon is often not running — build/test
what you can, and ask him to run the compose smoke test himself (rule 3).

### T18: Real AI Engineer World's Fair collection
Replace/extend `collections/example.json` with a real curated collection
of AIE World's Fair 2026 talks (10+ videos, multiple speakers, tracks,
topics, orgs — exercise every schema feature). `collections/test.json`
(3 talks, Denver's picks) shows the expected editorial quality. Source
is already identified (see the signal audit in followups.md FE-2): the
channel's own curation, playlist `PLDyBmFH9HlVc` ("AIE World's Fair 2026
Complete Playlist", 36 videos) via Data API `playlistItems.list` — do
NOT rely on tags (boilerplate) or descriptions (no event info on talk
uploads). Draft the collection from that playlist, parse speakers/orgs
from the title convention, and have Denver approve the list before
committing. Validate with `go run ./cmd/discovery validate`. Verify by
asking Denver to run a sync (spends his quota; ~1 unit, but it's his key).
Grep the engine for AIE-specific logic afterward — there must be none.

### T19: README
Cover all 10 PRD points (what it is/isn't, API key how-to, collection
authoring, file mode, db mode, ranking + adding a strategy, deployment,
hosted branding). Steal freely from `.agent/user-guide.md` but the README
is for strangers, not Denver. Include a real leaderboard screenshot —
ask Denver to take it, or coordinate one carefully per rule 1/3. Link
`openapi/openapi.yaml` and `openapi/collection.schema.json`. Add a
LICENSE (ask Denver: MIT was assumed in the OpenAPI info block).

### T20: Final QA vs MVP acceptance criteria
Walk every bullet in the PRD's "MVP Acceptance Criteria" section plus
the security list, with evidence (commands + output) recorded in
`.agent/tasks/qa-evidence.md`. Both modes. Fix blockers; file the rest
in `.agent/tasks/followups.md`.

### Known small cleanups (fold into T20, ~30 min total)
- `SyncResult.Failed` doc comment says "YouTube IDs" but may contain raw
  URLs for entries that never resolved — fix the comment only.
- CLI could use a `-quiet` flag for cron (engine slog interleaves with
  the summary); optional, small.
- Leaderboard pagination: DONE (2026-07-19, post-handoff-authoring) —
  25/50/100 size selector, prev/next, global rank continuity. Nothing
  to do here.

### Explicitly OUT of scope
Accounts, voting, submissions, semantic search, multi-tenant hosting,
FE-1 resolve endpoint, pagination UI. The PRD's "is not" list governs.

## Conventions

- Commits: imperative summary + short body when non-obvious; the repo
  history shows the style. Small, verifiable commits per task.
- Verification before any "done" claim: `make build && make lint &&
  make test` minimum; `-race` for anything touching concurrency.
- Update `.agent/tasks/todo.md` checkboxes as tasks land.
- Wave 3 is sequential — no parallel agents needed. Work T17 → T18 →
  T19 → T20 and pause for Denver at anything marked "ask Denver."

## Needs Denver (collect early, don't block on all of them)

- [ ] Playlist link or talk list for T18 (or approval of your researched list)
- [ ] License choice for T19 (MIT assumed)
- [ ] Public/private decision for the repo once README lands
- [ ] Compose smoke test on his machine (his Docker daemon)
- [ ] The leaderboard screenshot for the README

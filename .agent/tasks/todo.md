# Discovery Engine MVP — Task Checklist

Details, acceptance criteria, and verification per task: `.agent/tasks/plan.md`
Spec: `.agent/prd/prd.md`

## Wave 0 — Contracts (1 agent, sequential)
- [x] T01: Repository scaffold (go.mod, Makefile, tree, /health, CI)
- [x] T02: Collection JSON Schema + domain types + field-path validation
- [x] T03: OpenAPI contract + normalized model + Store/Ranker interfaces
- [x] T04: PostgreSQL schema + migrations (parallel with T03)
- [ ] CHECKPOINT: contracts reviewed and frozen (awaiting Denver's review)

## Wave 1 — Core engine (3 agents in parallel)
- [x] T05 (Lane A): YouTube URL normalization
- [x] T06 (Lane A): YouTube Data API client (batch, retry, per-video errors)
- [x] T07 (Lane B): Ranking strategies (views_24h/7d/growth also done; rank_change_24h → T16)
- [x] T08 (Lane C): In-memory store + file cache + config
- [x] T09: Sync engine (joins A+C; 1 agent after T06+T08)
- [x] CHECKPOINT: go test green (-race clean); real-key smoke sync passed 2026-07-19

## Wave 2 — Surfaces + DB mode (4 agents in parallel)
- [x] T10 (Lane D): Read API endpoints + filters + pagination
- [x] T11 (Lane D): POST /sync + rate limit + scheduler
- [x] T12 (Lane E): Server-rendered leaderboard (Product Hunt feel)
- [x] T13 (Lane F): CLI (validate, sync, serve, import, export)
- [x] T14 (Lane H): PostgreSQL store (conformance tests shared with memstore)
- [x] T15 (Lane H): Append-only snapshots + import/export round-trip
- [x] T16 (Lane H): Rank movement, history, movers, windowed strategies
- [ ] CHECKPOINT: both modes work end-to-end; review before packaging

## Wave 3 — Packaging (1 agent, sequential)
- [ ] T17: Dockerfile + compose (file mode default, postgres profile)
- [ ] T18: AI Engineer World's Fair example collection
- [ ] T19: README + docs (all 10 PRD points, screenshots)
- [ ] T20: End-to-end QA pass vs MVP acceptance criteria
- [ ] CHECKPOINT: MVP complete

## Fan-out rules
- Each lane owns its packages only; shared types in `internal/collections` are frozen after Wave 0.
- Every lane lands green: `go build ./... && go vet ./... && go test ./...`
- `cmd/server/main.go` wiring is Lane D's; other lanes expose constructors, don't wire.

## Deployment — Denver's instance (see deploy-railway.md)
- [x] Phase 1: Dockerfile + admin token guard (agent) — done 2026-07-19
- [x] Phase 2: Railway project, Postgres, cron service — live 2026-07-23
- [x] Phase 3: list.denverpeterson.com live (CNAME + TXT at registrar)
- [x] Phase 4: verified live 2026-07-23 (health, 17 collections, 401 guard, UI 200); day-2 trending check remains
- [ ] Later: OSS extraction to discovery-engine repo (post-usage-lock-in)

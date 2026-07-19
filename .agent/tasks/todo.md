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
- [ ] T09: Sync engine (joins A+C; 1 agent after T06+T08)
- [ ] CHECKPOINT: `go test ./...` green; real-key smoke sync

## Wave 2 — Surfaces + DB mode (4 agents in parallel)
- [ ] T10 (Lane D): Read API endpoints + filters + pagination
- [ ] T11 (Lane D): POST /sync + rate limit + scheduler
- [ ] T12 (Lane E): Server-rendered leaderboard (Product Hunt feel)
- [ ] T13 (Lane F): CLI (validate, sync, serve, import, export)
- [ ] T14 (Lane H): PostgreSQL store (conformance tests shared with memstore)
- [ ] T15 (Lane H): Append-only snapshots + import/export round-trip
- [ ] T16 (Lane H): Rank movement, history, movers, windowed strategies
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

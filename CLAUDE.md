# CLAUDE.md

Discovery Engine: a Go app that turns curated YouTube collection files
into ranked, filterable leaderboards. Read `.agent/tasks/plan.md` and
`.agent/architecture/` before structural changes.

## The OSS boundary (important)

This repo is two things at once, and the line between them must stay
sharp because the engine is intended to be open source:

**The engine (generic, OSS)** — `internal/`, `cmd/`, `web/`,
`openapi/`, `migrations/`, `Makefile`, Docker files. Rules:
- No personal or instance-specific references, ever: no
  denverpeterson.com, no "Denver", no hardcoded channel IDs, event
  names, or collection slugs. If a feature needs a name, it comes from
  a collection file or config.
- Verify with: `grep -ri "denver\|denverpeterson" internal/ cmd/ web/`
  — must return nothing.

**The instance (Denver's deployment and curation)** — allowed to be
personal, quarantined to exactly these locations:
- `collections/*.json` — Denver's curation. Intentionally public;
  author blocks with his name and domain are correct here.
- `scripts/*.py` config blocks (`CHANNELS`, `MIXES`, `EVENTS`,
  `AUTHOR`) — instance configuration. Keep config at the top of each
  script, clearly separated from mechanism, so a fork edits one block.
- `.agent/` — build history, plans, and architecture docs. Contains
  personal references and stays that way; see below.

**The plan of record (2026-07-19):** this repo is Denver's private
instance and deploys to Railway as-is (.agent/tasks/deploy-railway.md).
Once usage is locked in, the engine gets extracted to a new public repo
(working name: discovery-engine); this repo then becomes instance-only,
consuming the engine. The boundary rules above exist so that extraction
is mechanical — never solve a future-OSS concern by scrubbing engine
code; there should be nothing to scrub.

`.env` is gitignored and holds real secrets. Never print, commit, or
echo its values. Railway/CI hold their own copies.

## Conventions

- Verify before claiming done: `make build && make lint && make test`;
  `-race` for anything touching concurrency. OpenAPI changes:
  `make lint-openapi`.
- Frozen contracts (`internal/collections` types + Store,
  `internal/rankings` interfaces, `openapi/openapi.yaml` shapes,
  existing migrations): additive changes only, with justification.
  New migrations get new numbered files.
- Env vars are DISCOVERY_-prefixed; plain `DATABASE_URL` is
  deliberately ignored (ambient-collision lesson, see ADR/config docs).
- Servers: port 8080 always. A port conflict means a stale process to
  kill (find it with `lsof -nP -iTCP:8080 -sTCP:LISTEN`), never a
  reason to change ports. Verify your background processes are dead
  before ending a task; prefer handing the human the run command.
- Collection files are the admin surface: editorial changes happen in
  JSON via git, not in the database. `scripts/daily-sync.sh` is the
  whole refresh pipeline and must stay idempotent.

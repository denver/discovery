# Task: Deploy Denver's Instance to Railway

Decision (2026-07-19): deploy this repo as Denver's private instance
now. Open-sourcing waits until usage is locked in, then the engine gets
extracted to a new repo (working name: discovery-engine). This repo can
stay personal in the meantime; the CLAUDE.md OSS boundary still applies
to engine code so the later extraction is mechanical.

Target: `discovery.denverpeterson.com` → Railway app + Postgres + daily
cron. Vercel is DNS-only; the main site is untouched.

## Phase 1 — code prep (agent work, do before touching Railway)

- [ ] **Dockerfile + .dockerignore.** Multi-stage, static binary,
  `CMD ["discovery", "serve"]`, collections/ copied in. This is T17's
  first half, pulled forward; compose can wait for the OSS repo.
- [ ] **DISCOVERY_ADMIN_TOKEN guard** on `POST /api/v1/sync`: when the
  env var is set, require `Authorization: Bearer <token>` (401
  otherwise); when unset (local dev), behavior unchanged. ~30 lines +
  tests (FE-4 stage 2). Required before public traffic.
- [ ] **CI green on main** (it already is; keep it that way).

## Phase 2 — Railway setup (Denver in dashboard, agent can pair)

1. New Project → Deploy from GitHub → `denver/discovery` (private repo
   works; grant the Railway GitHub app access). Auto-deploy on main.
2. Add PostgreSQL to the project.
3. App service variables:

   | Variable | Value |
   |---|---|
   | `YOUTUBE_API_KEY` | (secret) |
   | `DISCOVERY_DATABASE_URL` | `${{Postgres.DATABASE_URL}}` |
   | `DISCOVERY_ADMIN_TOKEN` | (generate: `openssl rand -hex 32`) |
   | `PORT` | injected by Railway automatically |

   Deliberately NOT set: `DISCOVERY_COLLECTION_PATH` and
   `DISCOVERY_REFRESH_INTERVAL`. In db mode the app serves whatever the
   database holds; the cron service owns all syncing. The app's startup
   sync logs a "no collection paths" failure and continues — expected,
   harmless (revisit if it offends).
4. Cron service: same repo, start command `./scripts/daily-sync.sh`,
   schedule `0 14 * * *` (7am PT), same variables as the app plus
   `DISCOVERY_COLLECTION_PATH` unset is fine (script enumerates
   collections/ itself). Needs python3 + go in the image: use the
   builder stage image or a dedicated cron Dockerfile — agent decides
   during Phase 1 and documents in the Dockerfile.
5. First data load: run the cron service once manually (Railway
   "Deploy" → run) or `railway run ./scripts/daily-sync.sh` locally
   pointed at the Railway Postgres.

## Phase 3 — DNS (deferred by decision, 2026-07-19)

Launch on the Railway-generated domain (`<app>.up.railway.app`) first;
Railway provides TLS on it automatically. Bind the vanity subdomain
later, once Denver picks the name — candidates:
`discovery.denverpeterson.com` or `list.denverpeterson.com`.

When ready: App service → Settings → Networking → Custom Domain →
enter the chosen subdomain; copy the CNAME target; add the record in
Vercel → Domains → denverpeterson.com → DNS Records
(`CNAME <name> → <target>.up.railway.app`). TLS automatic on both
sides. Changing or adding the subdomain later is non-breaking; the
Railway domain keeps working alongside it.

## Phase 4 — verification checklist

- [ ] `https://discovery.denverpeterson.com/health` → `{"status":"ok"}`
- [ ] `/` lists all 16 collections; `/c/denvers-radar` renders ranked
- [ ] `/api/v1/collections/ai-engineer-worlds-fair-2026/rankings?sort=engagement` matches local
- [ ] `POST /api/v1/sync` without token → 401; with token → 200/429
- [ ] No secrets in logs (`railway logs` — config line shows `[set]`,
  never values)
- [ ] Day 2: movement arrows and `?sort=views_24h` light up after the
  second cron run
- [ ] Local `.env` keeps working for development (file mode + local db)

## Rollback

Railway keeps previous deploys: Deploys tab → redeploy prior build.
Data is append-only, so a code rollback never loses history. DNS
rollback = delete the CNAME (subdomain goes dark; apex unaffected).

## Cost

Hobby plan ~$5/month covers app + Postgres at this footprint; cron
adds ~60s of compute daily.

## Explicitly out of scope for this task

Docker Compose polish, README, the OSS repo split, GitHub Actions
committing regenerated collection files (Railway cron writes only to
the DB; file versioning moves to CI when the OSS repo exists).

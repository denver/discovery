# AI Engineer Event Registry

Derived 2026-07-19 from the @aiDotEngineer channel's own playlists (81
playlists, 919 uploads) plus the `Event:` description blocks on
livestreams. This registry is Discovery Engine editorial metadata: the
canonical structured list of events that the channel itself only encodes
implicitly in playlist names.

Confidence: playlist-derived facts are solid; cities/venues marked (?)
are inferred and need confirmation before use in a published collection.

| Event | When | Where | Channel curation (playlist evidence) |
|-------|------|-------|--------------------------------------|
| AI Engineer Summit 2023 | Oct 2023 | San Francisco | Talks (26), Remote Talks (11), Workshops (6) |
| AI Engineer World's Fair 2024 | Jun 2024 | San Francisco | Keynote (19), Agents (8), Multimodality (7), GPUs & Inference (8), RAG & LLM Frameworks (9), Evals & LLM Ops (11), AI Leadership (13), Expo (11), Workshop (21) |
| AI Engineer Summit NY 2025 | Feb 2025 | New York | Complete (18), Workshops (7), Online (30) |
| AI Engineer World's Fair 2025 | Jun 2025 | San Francisco | AIEWF 2025 Complete (284), Online (70), plus per-track: SWE Agents (19), Infra (13), RL + Reasoning (10), Design Engineering (10), Agent Reliability (9), Tiny Teams (7), LLM RecSys (7), Full Workshops (19) |
| AIE CODE 2025 | Nov 2025 | New York | Coding Model & Agent Labs (32), AI Leadership (18), Online Track (17) |
| AIE Europe 2026 | 2026 | Europe (Paris?) | Complete (243), Keynotes (18), Workshops (28), Online Track (18) |
| AI Engineer World's Fair 2026 | Jul 1–3 2026 | San Francisco, Moscone West | Complete (36, actively growing), Online Track (97) — venue/date from livestream `Event:` blocks |
| AI Engineer Melbourne | 2026 | Melbourne | 2 videos with `Event: AI Engineer Melbourne` description blocks; regional/meetup format |

Also present: evergreen topic playlists ("<Topic> @ AI Engineer") that
cut across events — Voice (25), Evals & Benchmarks (33), Graphs (25),
Generative Media (16), and sponsor/org playlists (OpenAI, Anthropic,
Google DeepMind, Microsoft) usable for `organizations` tagging.

## Why this exists

- YouTube has no event field. Only 5 of 919 video descriptions carry an
  `Event:` line (3 WF26 livestreams, 2 Melbourne). Playlists are the
  channel's only real event metadata, and they encode it as free-text
  names with at least four different naming conventions ("AIE World's
  Fair 2024", "AI Engineer World's Fair 2025", "AIEWF 2025", "WF26").
- Discovery Engine collections normalize this into structured `event`
  blocks: name, year, city, venue. This file is the mapping table
  between their playlist names and our canonical event names.
- Each row is a future curated collection: FE-2's drafter pointed at the
  row's playlists, stamped with the row's event block.

## Canonical event names for collection files

Use these exact `event.name` values so filtering and future
cross-collection queries stay consistent:

- `AI Engineer Summit` (year + city distinguish editions)
- `AI Engineer World's Fair`
- `AIE CODE`
- `AIE Europe`
- `AI Engineer Melbourne`

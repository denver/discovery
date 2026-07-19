# Metadata Format

How Discovery Engine's metadata is layered, who owns each layer, and how
the layers merge into the normalized API model. Companion to
`openapi/collection.schema.json` (input contract) and
`openapi/openapi.yaml` (output contract).

## The three layers

One video's metadata comes from three sources with three different
owners and lifetimes. The engine never mixes ownership: editors cannot
change view counts, YouTube cannot change topics.

```mermaid
flowchart LR
    subgraph editorial["EDITORIAL - the curator owns it"]
        CF["collection.json<br/>slug, title, author<br/>videos[]: speakers, event,<br/>track, topics, organizations,<br/>featured, published, overrides"]
    end
    subgraph provider["PROVIDER - YouTube owns it"]
        YT["Data API v3<br/>title, description, thumbnail,<br/>channel, publishedAt, duration,<br/>viewCount, likeCount, commentCount"]
    end
    subgraph temporal["TEMPORAL - time owns it"]
        SNAP["snapshots (append-only)<br/>counts at each sync<br/>rank positions per strategy"]
    end

    CF -->|"validate + resolve IDs"| SYNC["sync engine"]
    YT -->|"batched fetch"| SYNC
    SYNC -->|"record"| SNAP
    SYNC --> MERGE["merge"]
    SNAP -->|"previousPosition,<br/>history, movers"| MERGE
    MERGE --> V["Normalized Video<br/>(API + leaderboard)"]
```

Facts about the split:

- Editorial facts live only in the collection file. YouTube has no event
  field (verified empirically: 5 of 919 channel videos carry any event
  text), so `event`, `track`, `topics` can only be curated.
- Provider facts are never hand-edited; they refresh on every sync.
  `titleOverride`/`descriptionOverride` are the only editorial fields
  that shadow provider facts, and only when non-empty.
- Temporal facts are append-only. File mode keeps just the previous run
  (enough for movement arrows); database mode keeps the full series
  (enabling views_24h, growth, movers).

## Input: the collection file schema (v1.0)

```mermaid
erDiagram
    COLLECTION ||--|{ VIDEO_ENTRY : "videos (ordered)"
    VIDEO_ENTRY }o--o{ SPEAKER : "speakers (ordered)"
    VIDEO_ENTRY |o--o| EVENT : "event"

    COLLECTION {
        string schemaVersion "required: 1.0"
        string slug "required: lowercase-hyphen"
        string title "required"
        string description
        string author_name
        string author_url
        string source_type "e.g. curated, channel"
        string source_homepage
        string refreshInterval "Go duration: 6h"
        string defaultRanking "views likes comments engagement"
    }
    VIDEO_ENTRY {
        string youtubeUrl "required unless youtubeId; youtube.com or youtu.be only"
        string youtubeId "required unless youtubeUrl; 11 chars"
        string titleOverride "nullable; shadows provider title"
        string descriptionOverride "nullable"
        string track "single value, e.g. Keynotes"
        string-array topics "e.g. agents, evals"
        string-array organizations "e.g. OpenAI"
        bool featured "default false"
        bool published "default true; false hides from listings"
        string addedAt "RFC 3339"
        string notes "nullable, curator-only"
    }
    SPEAKER {
        string name "required"
        string slug "lowercase-hyphen; drives speaker filter"
    }
    EVENT {
        string name "canonical, see docs aie-events.md"
        int year "1990-2100"
        string city
        string venue "nullable"
    }
```

## Output: the normalized Video (API + web)

```mermaid
erDiagram
    VIDEO ||--|| CHANNEL : "channel"
    VIDEO |o--o| STATISTICS : "statistics (null until first sync)"
    VIDEO ||--|| EDITORIAL : "editorial"
    VIDEO |o--o| RANKING : "ranking (rankings responses only)"

    VIDEO {
        string id "canonical YouTube ID"
        string provider "youtube"
        string url "canonical watch URL"
        string title "provider, unless overridden"
        string description "provider, unless overridden"
        string thumbnailUrl "best available"
        datetime publishedAt
        int durationSeconds
    }
    CHANNEL {
        string id
        string name
    }
    STATISTICS {
        int64 viewCount
        int64 likeCount "0 when hidden"
        int64 commentCount "0 when disabled"
        datetime capturedAt "when fetched"
    }
    EDITORIAL {
        speakers speakers "from collection file"
        string-array topics
        string track
        event event
        string-array organizations
        bool featured
    }
    RANKING {
        int position "1-based, global across pages"
        int previousPosition "nullable: no prior recording"
        int change "nullable; positive = moved up"
        float score "raw strategy score"
        string strategy "e.g. views, engagement"
    }
```

## Database mode: where each layer lands

```mermaid
flowchart TD
    CF2["collection.json"] -->|import| C[collections]
    CF2 -->|entries| CV["collection_videos<br/>(membership + editorial:<br/>overrides, track, event,<br/>featured, published, position)"]
    CF2 -->|entities| E["speakers / topics / organizations<br/>+ join tables (global, deduped)"]
    YT2["YouTube sync"] -->|provider facts| VID["videos<br/>(one row per unique ID,<br/>shared across collections)"]
    YT2 -->|every sync| VS["video_snapshots<br/>(append-only)"]
    RANK["ranking pass"] -->|every sync| RS["rank_snapshots<br/>(append-only, per strategy)"]
    C --- CV
    CV --- VID
    VID --- VS
    VID --- RS
```

Key property: a video in N collections has one `videos` row and one
snapshot history, but N membership rows each carrying that collection's
editorial metadata. Curated collections split off from the channel pool
inherit history for free; editorial context stays per-collection.
Exception to per-collection editorial: speakers/topics/organizations are
global joins per the PRD table list (last import wins; tradeoff recorded
in db-schema.md).

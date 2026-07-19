# Discovery Engine — MVP Build PRD

Build an open-source project named Discovery Engine under github.com/denver.

## Product Summary

Discovery Engine is a Go-based API and lightweight web application for ranking a curated list of YouTube videos.

A user provides a structured list of YouTube videos and a YouTube Data API key. Discovery Engine fetches public metadata and popularity statistics, ranks the videos, and renders a browsable leaderboard.

The first real deployment will index talks from the AI Engineer World's Fair while the videos are actively being published and gaining traction.

The open-source version must run locally and remain useful without a database. PostgreSQL is an optional advanced mode that enables historical metrics, rank movement, and richer analytics over time.

## Core Use Case

A user supplies a JSON or YAML file containing a curated list of YouTube videos.

Discovery Engine should:

1. Validate and normalize the list.
2. Resolve YouTube URLs into canonical video IDs.
3. Fetch metadata from the YouTube Data API.
4. Fetch current view, like, and comment counts.
5. Rank videos by configurable criteria.
6. Expose the normalized collection through a Go REST API.
7. Render a simple responsive leaderboard.
8. Link every result back to the original YouTube video.

The system does not host or proxy video content.

## Product Boundaries

Discovery Engine is:

* A discovery and ranking engine.
* A presentation layer over curated YouTube collections.
* A reusable open-source foundation for hosted and branded collections.
* API-first and self-hostable.

Discovery Engine is not:

* A YouTube search engine.
* A video hosting platform.
* A general social network.
* A replacement for YouTube playlists.
* A recommendation system based on user behavior.

## Technical Stack

Use:

* Go
* PostgreSQL as an optional persistence layer
* A lightweight server-rendered web UI or minimal frontend
* Docker and Docker Compose
* SQL migrations when PostgreSQL is enabled
* YouTube Data API v3
* OpenAPI documentation

Prefer the Go standard library and mature, minimal dependencies.

## Operating Modes

### File Mode

File mode is the default and must not require PostgreSQL.

The user provides:

```
DISCOVERY_COLLECTION_PATH=./collections/ai-worlds-fair.json
YOUTUBE_API_KEY=...
```

The application:

* Loads the collection.
* Fetches and caches YouTube metadata in memory or a local cache file.
* Calculates the current ranking.
* Serves the API and web UI.
* Can run a manual or scheduled refresh.

File mode does not need to preserve a full historical time series.

### Database Mode

Database mode is enabled when DISCOVERY_DATABASE_URL is present.

The application:

* Imports or synchronizes the source collection into PostgreSQL.
* Stores normalized videos and collection membership.
* Stores metric snapshots over time.
* Stores calculated rank snapshots or derives them from metrics.
* Supports rank movement and historical analytics.
* Deduplicates videos shared across multiple collections.

The source file remains importable and exportable even when PostgreSQL is enabled.

## Suggested Repository Structure

```
discovery-engine/
├── cmd/
│   ├── server/
│   └── discovery/
├── internal/
│   ├── api/
│   ├── collections/
│   ├── config/
│   ├── database/
│   ├── rankings/
│   ├── scheduler/
│   ├── youtube/
│   └── web/
├── migrations/
├── collections/
│   └── example.json
├── web/
│   ├── templates/
│   └── static/
├── openapi/
├── docker-compose.yml
├── Dockerfile
├── Makefile
├── go.mod
└── README.md
```

## Collection Metadata Format

Design a versioned schema that is simple for humans to edit but rich enough to support future filtering.

Example:

```json
{
  "schemaVersion": "1.0",
  "slug": "ai-engineer-worlds-fair-2026",
  "title": "AI Engineer World's Fair 2026",
  "description": "A curated index of talks from AI Engineer World's Fair 2026.",
  "author": {
    "name": "Denver Peterson",
    "url": "https://denverpeterson.com"
  },
  "source": {
    "type": "curated",
    "homepage": "https://www.ai.engineer/"
  },
  "refreshInterval": "6h",
  "defaultRanking": "views",
  "videos": [
    {
      "youtubeUrl": "https://www.youtube.com/watch?v=VIDEO_ID",
      "titleOverride": null,
      "descriptionOverride": null,
      "speakers": [
        {
          "name": "Speaker Name",
          "slug": "speaker-name"
        }
      ],
      "event": {
        "name": "AI Engineer World's Fair",
        "year": 2026,
        "city": "San Francisco",
        "venue": null
      },
      "track": "Agents",
      "topics": [
        "agents",
        "context-engineering"
      ],
      "organizations": [
        "Example Company"
      ],
      "featured": false,
      "published": true,
      "addedAt": "2026-07-18T00:00:00Z",
      "notes": null
    }
  ]
}
```

Required collection fields:

* schemaVersion
* slug
* title
* videos

Required video field:

* youtubeUrl or youtubeId

Everything else should be optional.

The application should validate the file and return actionable errors with exact field paths.

## Normalized API Model

The API should return resolved YouTube metadata alongside editorial metadata.

Example:

```json
{
  "id": "VIDEO_ID",
  "provider": "youtube",
  "url": "https://www.youtube.com/watch?v=VIDEO_ID",
  "title": "Resolved YouTube Title",
  "description": "Resolved description",
  "thumbnailUrl": "https://...",
  "channel": {
    "id": "CHANNEL_ID",
    "name": "Channel Name"
  },
  "publishedAt": "2026-07-16T18:00:00Z",
  "durationSeconds": 1842,
  "statistics": {
    "viewCount": 82400,
    "likeCount": 4100,
    "commentCount": 218,
    "capturedAt": "2026-07-18T18:00:00Z"
  },
  "editorial": {
    "speakers": [],
    "topics": [],
    "track": "Agents",
    "event": {
      "name": "AI Engineer World's Fair",
      "year": 2026,
      "city": "San Francisco"
    }
  },
  "ranking": {
    "position": 1,
    "previousPosition": 4,
    "change": 3,
    "score": 82400,
    "strategy": "views"
  }
}
```

## Initial REST API

Implement:

```
GET  /health
GET  /api/v1/collections
GET  /api/v1/collections/{slug}
GET  /api/v1/collections/{slug}/videos
GET  /api/v1/collections/{slug}/rankings
GET  /api/v1/videos/{youtubeId}
POST /api/v1/sync
```

Support query parameters such as:

```
?sort=views
?sort=likes
?sort=comments
?topic=agents
?track=keynote
?speaker=speaker-slug
?limit=25
?offset=0
```

In database mode, add:

```
GET /api/v1/videos/{youtubeId}/history
GET /api/v1/collections/{slug}/movers
```

Do not expose the YouTube API key through any response or client-side request.

## Ranking Strategies

Implement these MVP ranking strategies:

* views
* likes
* comments
* engagement

Use a documented engagement formula, for example:

```
engagement = likes + (comments × 3)
```

In database mode, also support:

* views_24h
* views_7d
* growth_percent_24h
* rank_change_24h

Handle newly added videos and division-by-zero safely.

Keep ranking logic isolated behind a small interface so strategies can be added later.

## Refresh and Scheduling

Support:

```
discovery sync
discovery serve
discovery validate ./collection.json
discovery import ./collection.json
discovery export collection-slug
```

The server should optionally run a scheduler based on a configured refresh interval.

Also support one-shot synchronization so users can schedule refreshes externally with cron, GitHub Actions, Kubernetes CronJobs, or another scheduler.

Batch YouTube requests efficiently and avoid fetching the same video more than once per synchronization.

## Database Model

At minimum, design tables for:

```
collections
videos
collection_videos
video_snapshots
speakers
video_speakers
topics
video_topics
organizations
video_organizations
```

Important constraints:

* A video may belong to multiple collections.
* A speaker may appear in multiple videos.
* A topic may appear in multiple collections.
* YouTube metadata should be stored once per unique video.
* Metric snapshots must be append-only.
* Collection membership should preserve editorial ordering and metadata.

## Web Experience

Build a simple but polished page for each collection.

The page should include:

* Collection title and description
* Last refreshed timestamp
* Ranking controls
* Topic and track filters
* Ranked video cards
* Thumbnail
* Video title
* Speaker names
* Channel name
* Published date
* View, like, and comment counts
* Rank movement when history exists
* Direct link to watch on YouTube

The first page should feel closer to Product Hunt than YouTube:

* Rank is visually prominent.
* Movement and popularity are easy to scan.
* The page is optimized for discovery rather than playback.
* Embedded playback is optional and not required for MVP.

## AI Engineer World's Fair Example

Include a sample collection that demonstrates:

* Multiple talks
* Multiple speakers
* Topics
* Tracks
* Year
* City
* Event name
* Ranking by views
* Filtering by subject

Do not hard-code AI Engineer-specific behavior into the engine.

## Security and Reliability

* Keep the YouTube API key server-side.
* Support environment-variable configuration.
* Validate all imported data.
* Use request timeouts.
* Retry transient YouTube failures with bounded exponential backoff.
* Log failed video IDs without failing the entire synchronization.
* Rate-limit manual synchronization endpoints.
* Do not execute arbitrary expressions from collection files.
* Do not fetch arbitrary URLs beyond supported providers.

## Docker Experience

The following should work:

```
cp .env.example .env
cp collections/example.json collections/my-list.json
docker compose up
```

For file mode, PostgreSQL should not be required.

For database mode:

```
docker compose --profile postgres up
```

Document both workflows clearly.

## README Requirements

The README must explain:

1. What Discovery Engine is.
2. What it is not.
3. How to obtain a YouTube API key.
4. How to create a collection file.
5. How to run in file mode.
6. How to run with PostgreSQL.
7. How ranking works.
8. How to add a new ranking strategy.
9. How to deploy it.
10. How the hosted version can apply custom branding.

Include screenshots or placeholders for the collection leaderboard.

## MVP Acceptance Criteria

The MVP is complete when:

* A user can provide a JSON collection containing YouTube URLs.
* The application validates and normalizes the list.
* YouTube metadata and statistics are retrieved successfully.
* A Go API exposes the collection and ranked videos.
* A responsive leaderboard page renders the collection.
* The user can filter by topic and track.
* The user can rank by views, likes, comments, or engagement.
* The application works without PostgreSQL.
* PostgreSQL mode stores snapshots and calculates rank movement.
* Docker setup works.
* The repository contains documentation and an AI Engineer World's Fair example collection.

## Implementation Approach

Work in vertical slices:

1. Collection schema and validation.
2. YouTube URL normalization and provider client.
3. In-memory collection service.
4. Ranking strategies.
5. REST API.
6. Server-rendered leaderboard.
7. CLI.
8. Optional PostgreSQL persistence.
9. Snapshot metrics and rank movement.
10. Docker, documentation, and example collection.

Before implementation, produce:

* A concise architecture decision record.
* The proposed repository tree.
* The JSON Schema for collection files.
* The database schema.
* The initial OpenAPI contract.
* A milestone plan.

Then implement the MVP without expanding scope into accounts, voting, submissions, semantic search, or multi-tenant hosting.

The strongest first release is narrower than the earlier concept:

> Give Discovery Engine a curated JSON list of YouTube videos. It returns a ranked, filterable, living page.

The source JSON should contain editorial facts. YouTube supplies the provider facts. PostgreSQL records what changes with time.

-- Discovery Engine initial schema.
-- Design notes: .agent/architecture/db-schema.md

CREATE TABLE collections (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug             TEXT NOT NULL UNIQUE,
    schema_version   TEXT NOT NULL,
    title            TEXT NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    author_name      TEXT,
    author_url       TEXT,
    source_type      TEXT,
    source_homepage  TEXT,
    refresh_interval TEXT,
    default_ranking  TEXT,
    last_synced_at   TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per unique YouTube video, shared across collections.
-- Current stats are denormalized here for cheap reads; video_snapshots is
-- the historical record.
CREATE TABLE videos (
    id                BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    youtube_id        TEXT NOT NULL UNIQUE
                      CHECK (youtube_id ~ '^[A-Za-z0-9_-]{11}$'),
    title             TEXT NOT NULL DEFAULT '',
    description       TEXT NOT NULL DEFAULT '',
    thumbnail_url     TEXT NOT NULL DEFAULT '',
    channel_id        TEXT NOT NULL DEFAULT '',
    channel_name      TEXT NOT NULL DEFAULT '',
    published_at      TIMESTAMPTZ,
    duration_seconds  INT NOT NULL DEFAULT 0,
    view_count        BIGINT,
    like_count        BIGINT,
    comment_count     BIGINT,
    stats_captured_at TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Collection membership with per-collection editorial metadata.
-- position preserves the source file's editorial ordering.
CREATE TABLE collection_videos (
    collection_id        BIGINT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    video_id             BIGINT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    position             INT NOT NULL,
    title_override       TEXT,
    description_override TEXT,
    track                TEXT,
    event_name           TEXT,
    event_year           INT,
    event_city           TEXT,
    event_venue          TEXT,
    featured             BOOLEAN NOT NULL DEFAULT FALSE,
    published            BOOLEAN NOT NULL DEFAULT TRUE,
    added_at             TIMESTAMPTZ,
    notes                TEXT,
    PRIMARY KEY (collection_id, video_id),
    UNIQUE (collection_id, position) DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX collection_videos_video_idx ON collection_videos (video_id);

CREATE TABLE speakers (
    id   BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL
);

CREATE TABLE video_speakers (
    video_id   BIGINT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    speaker_id BIGINT NOT NULL REFERENCES speakers(id) ON DELETE CASCADE,
    position   INT NOT NULL DEFAULT 0,
    PRIMARY KEY (video_id, speaker_id)
);

CREATE INDEX video_speakers_speaker_idx ON video_speakers (speaker_id);

CREATE TABLE topics (
    id   BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug TEXT NOT NULL UNIQUE
);

CREATE TABLE video_topics (
    video_id BIGINT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    topic_id BIGINT NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
    PRIMARY KEY (video_id, topic_id)
);

CREATE INDEX video_topics_topic_idx ON video_topics (topic_id);

CREATE TABLE organizations (
    id   BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name TEXT NOT NULL UNIQUE
);

CREATE TABLE video_organizations (
    video_id        BIGINT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    organization_id BIGINT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    PRIMARY KEY (video_id, organization_id)
);

CREATE INDEX video_organizations_org_idx ON video_organizations (organization_id);

-- Append-only metric observations. UPDATEs are blocked by trigger;
-- DELETE remains possible for future retention policies.
CREATE TABLE video_snapshots (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    video_id      BIGINT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    view_count    BIGINT NOT NULL,
    like_count    BIGINT NOT NULL,
    comment_count BIGINT NOT NULL,
    captured_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX video_snapshots_video_time_idx
    ON video_snapshots (video_id, captured_at);

-- Computed positions per (collection, strategy) at each sync, also
-- append-only. Rank movement = compare against the closest snapshot
-- before the window boundary.
CREATE TABLE rank_snapshots (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    collection_id BIGINT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    video_id      BIGINT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    strategy      TEXT NOT NULL,
    position      INT NOT NULL,
    captured_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX rank_snapshots_lookup_idx
    ON rank_snapshots (collection_id, strategy, captured_at);

CREATE FUNCTION forbid_snapshot_update() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'snapshots are append-only';
END;
$$;

CREATE TRIGGER video_snapshots_append_only
    BEFORE UPDATE ON video_snapshots
    FOR EACH ROW EXECUTE FUNCTION forbid_snapshot_update();

CREATE TRIGGER rank_snapshots_append_only
    BEFORE UPDATE ON rank_snapshots
    FOR EACH ROW EXECUTE FUNCTION forbid_snapshot_update();

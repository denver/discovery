-- Additive: per-video ordering for topics and organizations.
--
-- video_speakers already carries a position column so speaker order from
-- the collection file survives a round trip; topics and organizations had
-- no ordering, which made ListVideos/export order nondeterministic (join
-- tables have no reliable order, and sorting by the global entity id
-- reflects first-ever-seen order across all videos, not this video's
-- editorial order). Same convention as video_speakers.position.

ALTER TABLE video_topics ADD COLUMN position INT NOT NULL DEFAULT 0;
ALTER TABLE video_organizations ADD COLUMN position INT NOT NULL DEFAULT 0;

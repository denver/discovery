# Follow-ups and Future Enhancements

Not in MVP scope. Revisit after Wave 3.

## FE-1: Resolve endpoint (curator helper)

`GET /api/v1/resolve?url=<youtube-url>` — takes a YouTube URL, returns the
normalized video JSON plus a collection-file-shaped `entry` block the curator
can paste into a collection file.

- Reuses `internal/youtube` normalization (allowed hosts only) + one
  batched fetch.
- Must share the sync endpoint's rate limiting (spends YouTube quota).
- Requires an OpenAPI contract amendment before implementation.
- Origin: Denver, 2026-07-19. Small task, Lane D shape, depends on T06.

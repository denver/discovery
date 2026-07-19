// Package webassets embeds the web UI's templates and static assets so
// the server binary is self-contained. internal/web parses templates from
// FS and serves static files from it; keeping the embed at the repo root
// preserves the PRD layout (web/templates, web/static) while remaining
// reachable from internal packages.
package webassets

import "embed"

// FS contains templates/*.html and static/*.
//
//go:embed templates static
var FS embed.FS

// Package web serves the server-rendered leaderboard UI: a collections
// index at /, a per-collection leaderboard at /c/{slug}, and embedded
// static assets under /static/.
//
// The package reads through internal/service in-process. Filtering,
// pagination, and ranking all live in the service layer; this package only
// builds view models and renders html/template pages. Sort and filter
// controls are plain links driven by ?sort=, ?track=, and ?topic= — no
// JavaScript.
package web

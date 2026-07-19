package web

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/denver/discovery/internal/collections"
	"github.com/denver/discovery/internal/rankings"
	"github.com/denver/discovery/internal/service"
	webassets "github.com/denver/discovery/web"
)

// historyNotice is rendered instead of the board when a windowed sort
// (views_24h, ...) is requested in file mode.
const historyNotice = "This sort needs database mode: historical strategies " +
	"rank by change over time, which requires snapshot history. Run with " +
	"DISCOVERY_DATABASE_URL set, or pick one of the sorts above."

// New returns the web UI handler. The server wiring mounts it at /.
func New(svc *service.Service) (http.Handler, error) {
	return newHandler(svc, time.Now)
}

// newHandler is New with an injectable clock for tests.
func newHandler(svc *service.Service, now func() time.Time) (http.Handler, error) {
	tmpl, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	h := &handler{svc: svc, now: now, tmpl: tmpl, logger: slog.Default()}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", h.index)
	mux.HandleFunc("GET /c/{slug}", h.leaderboard)
	mux.Handle("GET /static/", http.FileServerFS(webassets.FS))
	mux.HandleFunc("/", h.notFoundPage)
	return mux, nil
}

type handler struct {
	svc    *service.Service
	now    func() time.Time
	tmpl   map[string]*template.Template
	logger *slog.Logger
}

// parseTemplates builds one template set per page, each combining
// base.html with the page's title/content definitions.
func parseTemplates() (map[string]*template.Template, error) {
	pages := []string{"index", "leaderboard", "error"}
	tmpl := make(map[string]*template.Template, len(pages))
	for _, p := range pages {
		t, err := template.ParseFS(webassets.FS, "templates/base.html", "templates/"+p+".html")
		if err != nil {
			return nil, fmt.Errorf("web: parse %s templates: %w", p, err)
		}
		tmpl[p] = t
	}
	return tmpl, nil
}

// index renders the collections list at /.
func (h *handler) index(w http.ResponseWriter, r *http.Request) {
	infos, err := h.svc.Collections(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	now := h.now()
	cards := make([]collectionCard, 0, len(infos))
	for _, info := range infos {
		cards = append(cards, collectionCard{
			Slug:        info.Slug,
			Title:       info.Title,
			Description: info.Description,
			VideoCount:  info.VideoCount,
			LastSynced:  lastSyncedLabel(info.LastSyncedAt, now),
		})
	}
	h.render(w, r, http.StatusOK, "index", indexData{
		Title:       "Collections · Discovery",
		Collections: cards,
	})
}

// leaderboard renders /c/{slug} with ?sort=, ?track=, and ?topic=.
func (h *handler) leaderboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	slug := r.PathValue("slug")
	q := r.URL.Query()
	rawSort, track, topic, event := q.Get("sort"), q.Get("track"), q.Get("topic"), q.Get("event")

	limit, ok := intParam(q.Get("limit"), service.DefaultLimit)
	if !ok || limit < 1 || limit > service.MaxLimit {
		h.errorPage(w, r, http.StatusBadRequest, "Bad request",
			fmt.Sprintf("limit must be a number between 1 and %d.", service.MaxLimit))
		return
	}
	offset, ok := intParam(q.Get("offset"), 0)
	if !ok || offset < 0 {
		h.errorPage(w, r, http.StatusBadRequest, "Bad request",
			"offset must be a non-negative number.")
		return
	}

	info, err := h.svc.Collection(ctx, slug)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			h.notFoundPage(w, r)
			return
		}
		h.serverError(w, r, err)
		return
	}

	// Filter chips are built from every value present in the collection,
	// not just the currently filtered page.
	all, err := h.svc.Videos(ctx, slug, service.Filters{Limit: service.AllVideos})
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	tracks, topics, events := distinctFilterValues(all.Videos)

	data := leaderboardData{
		Title:         info.Title + " · Discovery",
		Collection:    info,
		LastRefreshed: "not synced yet",
	}
	if info.LastSyncedAt != nil {
		data.LastRefreshed = relTime(*info.LastSyncedAt, h.now())
	}

	page, resolved, err := h.svc.Rankings(ctx, slug, rawSort,
		service.Filters{Track: track, Topic: topic, Event: event, Limit: limit, Offset: offset})
	switch {
	case err == nil:
		data.Cards = make([]videoCard, 0, len(page.Videos))
		for _, v := range page.Videos {
			data.Cards = append(data.Cards, newVideoCard(v))
		}
		data.Pager = newPager(slug, rawSort, track, topic, event, limit, offset, page.Total, len(page.Videos))
	case errors.Is(err, rankings.ErrHistoryRequired),
		errors.Is(err, collections.ErrHistoryUnavailable):
		// Friendly notice, not an error page; keep controls usable.
		data.Notice = historyNotice
		resolved = rawSort
	case errors.Is(err, service.ErrBadRequest):
		h.errorPage(w, r, http.StatusBadRequest, "Bad request",
			"That sort or filter is not recognized. Try one of the sorts on the leaderboard.")
		return
	case errors.Is(err, collections.ErrNotFound):
		h.notFoundPage(w, r)
		return
	default:
		h.serverError(w, r, err)
		return
	}

	// Sort tabs always carry an explicit ?sort=; filter chips preserve
	// only what the request actually asked for (rawSort), so the default
	// sort's URLs stay clean.
	data.SortLinks = sortLinks(slug, resolved, track, topic, event, limit)
	eventLinks := filterLinks(events, event, func(v string) string {
		return leaderboardURL(slug, rawSort, track, topic, v, limit, 0)
	})
	trackLinks := filterLinks(tracks, track, func(v string) string {
		return leaderboardURL(slug, rawSort, v, topic, event, limit, 0)
	})
	topicLinks := filterLinks(topics, topic, func(v string) string {
		return leaderboardURL(slug, rawSort, track, v, event, limit, 0)
	})
	// Event is the entry dimension and stays open; track/topic open only
	// while active, keeping big collections' boards above the fold.
	for _, row := range []*filterRow{
		newFilterRow("Event", eventLinks, event, true),
		newFilterRow("Track", trackLinks, track, false),
		newFilterRow("Topic", topicLinks, topic, false),
	} {
		if row != nil {
			data.FilterRows = append(data.FilterRows, *row)
		}
	}

	h.render(w, r, http.StatusOK, "leaderboard", data)
}

// intParam parses an optional integer query parameter; empty means the
// default. ok is false on non-numeric input.
func intParam(raw string, def int) (v int, ok bool) {
	if raw == "" {
		return def, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return n, true
}

// notFoundPage renders the 404 page for unknown slugs and paths.
func (h *handler) notFoundPage(w http.ResponseWriter, r *http.Request) {
	h.errorPage(w, r, http.StatusNotFound, "Page not found",
		"That collection or page does not exist. It may have been renamed or removed.")
}

func (h *handler) serverError(w http.ResponseWriter, r *http.Request, err error) {
	h.logger.Error("web: request failed", "path", r.URL.Path, "error", err)
	h.errorPage(w, r, http.StatusInternalServerError, "Something went wrong",
		"An unexpected error occurred. Please try again.")
}

func (h *handler) errorPage(w http.ResponseWriter, r *http.Request, status int, heading, message string) {
	h.render(w, r, status, "error", errorData{
		Title:   heading + " · Discovery",
		Status:  status,
		Heading: heading,
		Message: message,
	})
}

// render executes a page into a buffer first so a template failure can
// still produce a clean 500 instead of a half-written page.
func (h *handler) render(w http.ResponseWriter, r *http.Request, status int, page string, data any) {
	var buf bytes.Buffer
	if err := h.tmpl[page].ExecuteTemplate(&buf, "base.html", data); err != nil {
		h.logger.Error("web: render failed", "page", page, "path", r.URL.Path, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

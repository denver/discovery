// Package api implements the REST API defined in openapi/openapi.yaml:
// read endpoints over the shared service layer plus POST /api/v1/sync.
// Handlers translate service and store errors into contract-shaped JSON
// errors; filtering, ranking, and pagination live in internal/service,
// never here.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	stdsync "sync"
	"time"

	"github.com/denver/discovery/internal/collections"
	"github.com/denver/discovery/internal/rankings"
	"github.com/denver/discovery/internal/service"
	syncpkg "github.com/denver/discovery/internal/sync"
)

// DefaultSyncCooldown is the minimum interval between successful manual
// syncs via POST /api/v1/sync. Scheduler runs are not subject to it.
const DefaultSyncCooldown = 60 * time.Second

// youtubeIDPattern validates {youtubeId} path values before any lookup.
var youtubeIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// Option configures the handler returned by New.
type Option func(*server)

// WithLogger sets the logger for 5xx details. Defaults to slog.Default().
func WithLogger(l *slog.Logger) Option {
	return func(s *server) { s.logger = l }
}

// WithSyncCooldown overrides the minimum interval between successful
// manual syncs. Zero or negative disables the cooldown.
func WithSyncCooldown(d time.Duration) Option {
	return func(s *server) { s.cooldown = d }
}

// WithNow overrides the clock used for the sync cooldown and the history
// ?since default. Tests inject a fixed clock; defaults to time.Now.
func WithNow(now func() time.Time) Option {
	return func(s *server) { s.now = now }
}

type server struct {
	svc      *service.Service
	engine   *syncpkg.Engine
	logger   *slog.Logger
	cooldown time.Duration
	now      func() time.Time

	mu           stdsync.Mutex
	lastManualOK time.Time
}

// New returns the API handler: GET /health plus every /api/v1 endpoint
// from the OpenAPI contract, on a stdlib ServeMux with method patterns.
// Route patterns are absolute, so the handler can be mounted on a root
// mux under "/api/v1/" and "/health" without prefix stripping.
func New(svc *service.Service, engine *syncpkg.Engine, opts ...Option) http.Handler {
	s := &server{
		svc:      svc,
		engine:   engine,
		logger:   slog.Default(),
		cooldown: DefaultSyncCooldown,
		now:      time.Now,
	}
	for _, opt := range opts {
		opt(s)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/collections", s.handleListCollections)
	mux.HandleFunc("GET /api/v1/collections/{slug}", s.handleGetCollection)
	mux.HandleFunc("GET /api/v1/collections/{slug}/videos", s.handleListVideos)
	mux.HandleFunc("GET /api/v1/collections/{slug}/rankings", s.handleRankings)
	mux.HandleFunc("GET /api/v1/collections/{slug}/movers", s.handleMovers)
	mux.HandleFunc("GET /api/v1/videos/{youtubeId}", s.handleGetVideo)
	mux.HandleFunc("GET /api/v1/videos/{youtubeId}/history", s.handleHistory)
	mux.HandleFunc("POST /api/v1/sync", s.handleSync)
	return mux
}

// writeJSON writes v with the given status. Encoding failures after the
// header is sent can only be logged.
func (s *server) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.logger.Error("api: encode response", "error", err)
	}
}

// errorBody is the contract's Error schema.
type errorBody struct {
	Error string `json:"error"`
}

// writeError maps domain errors to contract status codes. Unrecognized
// errors become a generic 500: the detail is logged, never leaked.
func (s *server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var verrs collections.ValidationErrors
	switch {
	case errors.Is(err, collections.ErrNotFound):
		s.writeJSON(w, http.StatusNotFound, errorBody{Error: err.Error()})
	case errors.Is(err, service.ErrBadRequest), errors.As(err, &verrs):
		s.writeJSON(w, http.StatusBadRequest, errorBody{Error: err.Error()})
	case errors.Is(err, rankings.ErrHistoryRequired), errors.Is(err, collections.ErrHistoryUnavailable):
		s.writeJSON(w, http.StatusNotImplemented, errorBody{Error: err.Error()})
	case errors.Is(err, syncpkg.ErrSyncInProgress):
		s.writeJSON(w, http.StatusTooManyRequests, errorBody{Error: err.Error()})
	default:
		s.logger.Error("api: internal error", "method", r.Method, "path", r.URL.Path, "error", err)
		s.writeJSON(w, http.StatusInternalServerError, errorBody{Error: "internal server error"})
	}
}

// wrapNotFound names the missing resource so 404 bodies read
// `collection "x" not found` instead of a bare "not found".
func wrapNotFound(err error, kind, key string) error {
	if errors.Is(err, collections.ErrNotFound) {
		return fmt.Errorf("%s %q %w", kind, key, collections.ErrNotFound)
	}
	return err
}

// parseFilters reads topic/track/speaker/limit/offset. Limit and offset
// must be integers within contract bounds; anything else is a 400.
func parseFilters(q url.Values) (service.Filters, error) {
	f := service.Filters{
		Topic:   q.Get("topic"),
		Track:   q.Get("track"),
		Speaker: q.Get("speaker"),
	}
	limit, err := parseIntParam(q, "limit", 1, service.MaxLimit)
	if err != nil {
		return f, err
	}
	f.Limit = limit
	offset, err := parseIntParam(q, "offset", 0, -1)
	if err != nil {
		return f, err
	}
	f.Offset = offset
	return f, nil
}

// parseIntParam strictly parses an optional integer query parameter.
// max < 0 means unbounded. Absent parameters return 0.
func parseIntParam(q url.Values, name string, min, max int) (int, error) {
	v := q.Get(name)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < min || (max >= 0 && n > max) {
		if max >= 0 {
			return 0, fmt.Errorf("%w: %s must be an integer between %d and %d", service.ErrBadRequest, name, min, max)
		}
		return 0, fmt.Errorf("%w: %s must be an integer >= %d", service.ErrBadRequest, name, min)
	}
	return n, nil
}

package api

import (
	"net/http"
	"strconv"
	"time"
)

// handleSync runs the sync engine. Concurrency is guarded by the engine
// itself (ErrSyncInProgress → 429); on top of that, successful manual
// syncs start a cooldown window during which further manual syncs get 429
// with a Retry-After header. Scheduler and CLI runs bypass the cooldown
// because they call the engine directly.
func (s *server) handleSync(w http.ResponseWriter, r *http.Request) {
	if s.cooldown > 0 {
		s.mu.Lock()
		remaining := s.cooldown - s.now().Sub(s.lastManualOK)
		last := s.lastManualOK
		s.mu.Unlock()
		if !last.IsZero() && remaining > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(ceilSeconds(remaining)))
			s.writeJSON(w, http.StatusTooManyRequests, errorBody{
				Error: "sync ran too recently; retry after the cooldown",
			})
			return
		}
	}

	result, err := s.engine.Run(r.Context())
	if err != nil {
		// Partial results accompanying a fetch error are intentionally not
		// returned: the contract's 200 means "sync completed".
		s.writeError(w, r, err)
		return
	}

	s.mu.Lock()
	s.lastManualOK = s.now()
	s.mu.Unlock()
	s.writeJSON(w, http.StatusOK, result)
}

// ceilSeconds rounds a positive duration up to whole seconds, minimum 1,
// suitable for a Retry-After header.
func ceilSeconds(d time.Duration) int {
	secs := int((d + time.Second - 1) / time.Second)
	if secs < 1 {
		secs = 1
	}
	return secs
}

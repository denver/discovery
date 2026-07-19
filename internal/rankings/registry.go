package rankings

import (
	"fmt"
	"sort"
)

// Registry maps strategy names to Rankers.
type Registry struct {
	rankers map[string]Ranker
}

// NewRegistry returns an empty registry. Most callers want
// DefaultRegistry (populated with built-in strategies in T07).
func NewRegistry() *Registry {
	return &Registry{rankers: map[string]Ranker{}}
}

// Register adds a strategy. Registering a duplicate name panics: it is a
// programming error, caught at startup.
func (r *Registry) Register(rk Ranker) {
	if _, exists := r.rankers[rk.Name()]; exists {
		panic(fmt.Sprintf("rankings: duplicate strategy %q", rk.Name()))
	}
	r.rankers[rk.Name()] = rk
}

// Get resolves a strategy by name.
func (r *Registry) Get(name string) (Ranker, error) {
	rk, ok := r.rankers[name]
	if !ok {
		return nil, fmt.Errorf("unknown ranking strategy %q (available: %v)", name, r.Names())
	}
	return rk, nil
}

// Names lists registered strategies, sorted.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.rankers))
	for n := range r.rankers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

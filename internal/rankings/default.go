package rankings

// DefaultRegistry returns a new Registry with every built-in strategy
// registered. Each call returns an independent instance; registries are
// cheap and mutating one never affects another.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	r.Register(Views{})
	r.Register(Likes{})
	r.Register(Comments{})
	r.Register(Engagement{})
	r.Register(windowed{name: "views_24h", window: day, kind: viewDelta})
	r.Register(windowed{name: "views_7d", window: 7 * day, kind: viewDelta})
	r.Register(windowed{name: "growth_percent_24h", window: day, kind: growthPercent})
	r.Register(windowed{name: "rank_change_24h", window: day, kind: rankChange})
	return r
}

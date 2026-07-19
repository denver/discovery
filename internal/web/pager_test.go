package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/denver/discovery/internal/collections"
	"github.com/denver/discovery/internal/rankings"
	"github.com/denver/discovery/internal/service"
)

// newLargeHandler seeds a collection of n synced videos with descending
// view counts (video 0 has the most views). The last video carries a
// track only it has, to prove chips scan past the first page.
func newLargeHandler(t *testing.T, n int) http.Handler {
	t.Helper()
	ctx := context.Background()
	store := collections.NewMemStore(collections.MemStoreOptions{})
	t.Cleanup(func() { store.Close() })

	entries := make([]collections.VideoEntry, n)
	provider := make([]collections.ProviderVideo, n)
	for i := range n {
		id := fmt.Sprintf("vid%08d", i)
		entries[i] = collections.VideoEntry{YouTubeID: id}
		if i == n-1 {
			entries[i].Track = "DeepCut"
		}
		provider[i] = collections.ProviderVideo{
			ID:          id,
			Title:       fmt.Sprintf("Talk %03d", i),
			ChannelName: "AI Engineer",
			PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i),
			Stats:       collections.Statistics{ViewCount: int64(10000 - i), CapturedAt: fixedNow()},
		}
	}
	coll := &collections.Collection{
		SchemaVersion: "1.0", Slug: "big", Title: "Big Collection",
		DefaultRanking: "views", Videos: entries,
	}
	if err := store.UpsertCollection(ctx, coll); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProviderData(ctx, provider); err != nil {
		t.Fatal(err)
	}
	svc := &service.Service{Store: store, Registry: rankings.DefaultRegistry(), Now: fixedNow}
	h, err := newHandler(svc, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestPagerFirstPage(t *testing.T) {
	h := newLargeHandler(t, 130)
	status, body := get(t, h, "/c/big")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if got := strings.Count(body, `<li class="card`); got != 25 {
		t.Errorf("cards = %d, want default 25", got)
	}
	if !strings.Contains(body, "1–25 of 130") {
		t.Error("missing range indicator 1–25 of 130")
	}
	if strings.Contains(body, "Prev") {
		t.Error("first page must not offer Prev")
	}
	if !strings.Contains(body, "offset=25") {
		t.Error("missing Next link to offset=25")
	}
}

func TestPagerSecondPageRanksContinue(t *testing.T) {
	h := newLargeHandler(t, 130)
	status, body := get(t, h, "/c/big?offset=25")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if !strings.Contains(body, "26–50 of 130") {
		t.Error("missing range indicator 26–50 of 130")
	}
	if !strings.Contains(body, `<span class="rank-num">26</span>`) {
		t.Error("page 2 should start at rank 26")
	}
	if strings.Contains(body, `<span class="rank-num">1</span>`) {
		t.Error("page 2 must not restart ranks at 1")
	}
	if !strings.Contains(body, "Prev") || !strings.Contains(body, "Next") {
		t.Error("middle page should offer both Prev and Next")
	}
}

func TestPagerLastPage(t *testing.T) {
	h := newLargeHandler(t, 130)
	status, body := get(t, h, "/c/big?limit=100&offset=100")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if got := strings.Count(body, `<li class="card`); got != 30 {
		t.Errorf("cards = %d, want trailing 30", got)
	}
	if !strings.Contains(body, "101–130 of 130") {
		t.Error("missing range indicator 101–130 of 130")
	}
	if strings.Contains(body, "Next") {
		t.Error("last page must not offer Next")
	}
	if !strings.Contains(body, `href="/c/big?limit=50"`) {
		t.Error("missing size-50 link with offset reset")
	}
}

func TestPagerSortLinksPreserveLimit(t *testing.T) {
	h := newLargeHandler(t, 130)
	_, body := get(t, h, "/c/big?limit=50")
	if !strings.Contains(body, `href="/c/big?limit=50&amp;sort=likes"`) {
		t.Error("sort tabs should preserve non-default limit")
	}
}

func TestPagerHiddenForSmallCollections(t *testing.T) {
	h := newLargeHandler(t, 10)
	_, body := get(t, h, "/c/big")
	if strings.Contains(body, `class="pager"`) {
		t.Error("pager should be hidden when everything fits one smallest page")
	}
}

func TestPagerBadParams(t *testing.T) {
	h := newLargeHandler(t, 30)
	for _, path := range []string{
		"/c/big?limit=abc", "/c/big?limit=0", "/c/big?limit=101",
		"/c/big?offset=-1", "/c/big?offset=x",
	} {
		if status, _ := get(t, h, path); status != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", path, status)
		}
	}
}

func TestFilterChipsScanWholeCollection(t *testing.T) {
	// 105 videos; only the 105th has a track. Chips must still show it.
	h := newLargeHandler(t, 105)
	_, body := get(t, h, "/c/big")
	if !strings.Contains(body, "DeepCut") {
		t.Error("filter chips should include values beyond the first 100 videos")
	}
}

// Built-in strategies:
//
//   - views: score = viewCount
//   - likes: score = likeCount
//   - comments: score = commentCount
//   - engagement: score = likeCount + commentCount*3 (a comment is a
//     stronger engagement signal than a like, so it weighs 3x)
//
// Windowed strategies read snapshots through History and return
// ErrHistoryRequired in file mode:
//
//   - views_24h: view-count delta over the last 24 hours
//   - views_7d: view-count delta over the last 7 days
//   - growth_percent_24h: 24h view delta as a percent of the window's
//     baseline view count (zero or missing baseline scores 0; never
//     divides by zero)
//   - rank_change_24h: position improvement over 24 hours (scoring lands
//     in T16 alongside rank snapshot queries)
//
// Windowed deltas need at least two snapshots inside the window; videos
// with fewer score 0. Videos with nil Statistics score 0 under every
// stat-based strategy and are placed after scored videos by Rank.
package rankings

package youtube

import (
	"errors"
	"testing"
)

func TestResolveID(t *testing.T) {
	const want = "dQw4w9WgXcQ"

	valid := []struct {
		name string
		in   string
		want string
	}{
		{"raw ID", "dQw4w9WgXcQ", want},
		{"raw ID with dash and underscore", "a-b_c1234Ab", "a-b_c1234Ab"},
		{"watch https www", "https://www.youtube.com/watch?v=dQw4w9WgXcQ", want},
		{"watch http bare host", "http://youtube.com/watch?v=dQw4w9WgXcQ", want},
		{"watch extra params", "https://www.youtube.com/watch?v=dQw4w9WgXcQ&feature=youtu.be&list=PL0", want},
		{"watch v not first param", "https://www.youtube.com/watch?feature=share&v=dQw4w9WgXcQ", want},
		{"mobile subdomain", "https://m.youtube.com/watch?v=dQw4w9WgXcQ&t=42s", want},
		{"music subdomain", "https://music.youtube.com/watch?v=dQw4w9WgXcQ", want},
		{"youtu.be", "https://youtu.be/dQw4w9WgXcQ", want},
		{"youtu.be with timestamp", "https://youtu.be/dQw4w9WgXcQ?t=30", want},
		{"shorts", "https://www.youtube.com/shorts/dQw4w9WgXcQ", want},
		{"shorts trailing slash", "https://www.youtube.com/shorts/dQw4w9WgXcQ/", want},
		{"embed", "https://www.youtube.com/embed/dQw4w9WgXcQ", want},
		{"live", "https://www.youtube.com/live/dQw4w9WgXcQ?feature=share", want},
		{"legacy /v/", "https://www.youtube.com/v/dQw4w9WgXcQ", want},
		{"surrounding whitespace", "  https://youtu.be/dQw4w9WgXcQ \n", want},
		{"uppercase host", "https://WWW.YouTube.COM/watch?v=dQw4w9WgXcQ", want},
	}
	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveID(tc.in)
			if err != nil {
				t.Fatalf("ResolveID(%q) error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ResolveID(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	invalid := []struct {
		name    string
		in      string
		wantErr error
	}{
		{"empty", "", ErrNotYouTube},
		{"whitespace only", "   ", ErrNotYouTube},
		{"vimeo host", "https://vimeo.com/12345678901", ErrNotYouTube},
		{"foreign host with v param", "https://evil.com/watch?v=dQw4w9WgXcQ", ErrNotYouTube},
		{"host suffix spoof", "https://youtube.com.evil.com/watch?v=dQw4w9WgXcQ", ErrNotYouTube},
		{"host prefix spoof", "https://notyoutu.be/dQw4w9WgXcQ", ErrNotYouTube},
		{"javascript scheme", "javascript:alert(1)", ErrNotYouTube},
		{"file scheme", "file:///etc/passwd", ErrNotYouTube},
		{"ftp scheme", "ftp://youtube.com/watch?v=dQw4w9WgXcQ", ErrNotYouTube},
		{"scheme-less URL", "www.youtube.com/watch?v=dQw4w9WgXcQ", ErrNotYouTube},
		{"unparseable", "https://youtube.com/%zz\x7f", ErrNotYouTube},
		{"raw ID too short", "abcdefghij", ErrNotYouTube},
		{"raw ID too long", "abcdefghijkl", ErrNotYouTube},
		{"watch without v", "https://www.youtube.com/watch", ErrNoVideoID},
		{"watch with short v", "https://www.youtube.com/watch?v=short", ErrNoVideoID},
		{"watch with injected v", "https://www.youtube.com/watch?v=<script>alert", ErrNoVideoID},
		{"youtu.be bare", "https://youtu.be/", ErrNoVideoID},
		{"shorts without ID", "https://www.youtube.com/shorts/", ErrNoVideoID},
		{"playlist URL", "https://www.youtube.com/playlist?list=PL0123456789", ErrNoVideoID},
		{"channel URL", "https://www.youtube.com/@somechannel", ErrNoVideoID},
		{"root URL", "https://www.youtube.com/", ErrNoVideoID},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveID(tc.in)
			if err == nil {
				t.Fatalf("ResolveID(%q) = %q, want error %v", tc.in, got, tc.wantErr)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ResolveID(%q) error = %v, want errors.Is %v", tc.in, err, tc.wantErr)
			}
		})
	}
}

func TestWatchURL(t *testing.T) {
	got := WatchURL("dQw4w9WgXcQ")
	want := "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
	if got != want {
		t.Fatalf("WatchURL = %q, want %q", got, want)
	}
}

func TestWatchURLRoundTrip(t *testing.T) {
	id, err := ResolveID(WatchURL("a-b_c1234Ab"))
	if err != nil {
		t.Fatalf("round trip error: %v", err)
	}
	if id != "a-b_c1234Ab" {
		t.Fatalf("round trip = %q, want %q", id, "a-b_c1234Ab")
	}
}

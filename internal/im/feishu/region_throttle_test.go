package feishu

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/im"
)

// The folded reasoning region is re-sent in full on every flush tick, which
// alone would put a three-region card on Feishu's 10 operations/second ceiling.
// The throttle must hold small increments back, release a large one at once, and
// let the final frame through unconditionally so the tail is never lost.
func TestUpdateStructuredStream_ThrottlesThinkingRegion(t *testing.T) {
	useTestHTTPClient(t)
	const cardID = "card-throttle"

	var mu sync.Mutex
	pushes := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "tenant_access_token": "test-token", "expire": 7200,
			})
			return
		}
		if r.Method == http.MethodPut {
			if i := strings.LastIndex(r.URL.Path, "/elements/"); i >= 0 {
				id := strings.TrimSuffix(r.URL.Path[i+len("/elements/"):], "/content")
				mu.Lock()
				pushes[id]++
				mu.Unlock()
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok"})
	}))
	defer srv.Close()

	adapter, _ := NewAdapter(testRegion(srv.URL), "app", "secret", "", "", "")
	feishuStreamsMu.Lock()
	feishuStreams[cardID] = &feishuStreamState{}
	feishuStreamsMu.Unlock()
	t.Cleanup(func() {
		feishuStreamsMu.Lock()
		delete(feishuStreams, cardID)
		feishuStreamsMu.Unlock()
	})

	ctx := context.Background()
	count := func(elementID string) int {
		mu.Lock()
		defer mu.Unlock()
		return pushes[elementID]
	}

	thinking := strings.Repeat("思", thinkingRegionThrottle.minGrowth)
	answer := "最终回答"
	if err := adapter.UpdateStructuredStream(ctx, nil, cardID, im.IMStreamRender{Thinking: thinking, Answer: answer}); err != nil {
		t.Fatalf("first frame: %v", err)
	}

	// Two small increments inside the same second: the thinking region must stay
	// on its previous content, while the answer region keeps its own cadence.
	thinking += strings.Repeat("考", thinkingRegionThrottle.minGrowth/4)
	for i := 0; i < 2; i++ {
		if err := adapter.UpdateStructuredStream(ctx, nil, cardID, im.IMStreamRender{Thinking: thinking, Answer: answer}); err != nil {
			t.Fatalf("throttled frame %d: %v", i, err)
		}
	}
	if got := count(streamThinkingElemID); got != 1 {
		t.Fatalf("thinking region pushed %d times within the throttle window, want 1", got)
	}

	// An increment at or above minGrowth is worth an immediate repaint.
	thinking += strings.Repeat("考", thinkingRegionThrottle.minGrowth)
	if err := adapter.UpdateStructuredStream(ctx, nil, cardID, im.IMStreamRender{Thinking: thinking, Answer: answer}); err != nil {
		t.Fatalf("growth frame: %v", err)
	}
	if got := count(streamThinkingElemID); got != 2 {
		t.Fatalf("thinking region pushed %d times after a large growth, want 2", got)
	}

	// The final frame bypasses the throttle: a withheld tail must not be lost.
	if err := adapter.FinalizeStructuredStream(ctx, nil, cardID, im.IMStreamRender{Thinking: thinking + "。", Answer: answer}); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if got := count(streamThinkingElemID); got != 3 {
		t.Fatalf("thinking region pushed %d times after finalize, want 3", got)
	}

	// The unchanged answer was only ever sent once.
	if got := count(streamAnswerElemID); got != 1 {
		t.Fatalf("answer region pushed %d times, want 1", got)
	}
}

func TestStreamRegionThrottle_Allows(t *testing.T) {
	now := time.Now()
	base := strings.Repeat("x", 100)

	cases := []struct {
		name       string
		lastPushed time.Time
		prev       string
		next       string
		want       bool
	}{
		{
			name:       "small growth inside the window is held back",
			lastPushed: now.Add(-100 * time.Millisecond),
			prev:       base,
			next:       base + strings.Repeat("y", 10),
			want:       false,
		},
		{
			name:       "large growth is released immediately",
			lastPushed: now.Add(-100 * time.Millisecond),
			prev:       base,
			next:       base + strings.Repeat("y", thinkingRegionThrottle.minGrowth),
			want:       true,
		},
		{
			name:       "small growth after the interval is released",
			lastPushed: now.Add(-thinkingRegionThrottle.minInterval),
			prev:       base,
			next:       base + "y",
			want:       true,
		},
		{
			// A retracted preamble shrinks the region; holding it back would leave
			// text on the card that the model has already withdrawn.
			name:       "retraction is never withheld",
			lastPushed: now,
			prev:       base,
			next:       base[:50],
			want:       true,
		},
	}

	for _, c := range cases {
		if got := thinkingRegionThrottle.allows(now, c.lastPushed, c.prev, c.next); got != c.want {
			t.Errorf("%s: allows() = %v, want %v", c.name, got, c.want)
		}
	}
}

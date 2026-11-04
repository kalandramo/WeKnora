package feishu

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/im"
)

// newPlaceholderTestState mirrors what StartStream records for a fresh card: the
// answer region already carries the create-time placeholder.
func newPlaceholderTestState(cardID string, placeholder string) {
	feishuStreamsMu.Lock()
	feishuStreams[cardID] = &feishuStreamState{
		regionContent: map[string]string{streamAnswerElemID: placeholder},
	}
	feishuStreamsMu.Unlock()
}

func clearPlaceholderTestState(t *testing.T, cardID string) {
	t.Helper()
	t.Cleanup(func() {
		feishuStreamsMu.Lock()
		delete(feishuStreams, cardID)
		feishuStreamsMu.Unlock()
	})
}

// The card is created with the thinking placeholder in the answer region, since
// the first tool round can take a minute and an empty body reads as a broken
// card. These transitions keep it honest: a frame without an answer leaves the
// placeholder alone, a real answer replaces it, and a retracted preamble puts it
// back instead of leaving withdrawn text parked under the summary.
func TestUpdateStructuredStream_AnswerRegionHoldsPlaceholder(t *testing.T) {
	useTestHTTPClient(t)
	const cardID = "card-answer-placeholder"

	var mu sync.Mutex
	var answerPushes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "tenant_access_token": "test-token", "expire": 7200,
			})
			return
		}
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/elements/"+streamAnswerElemID+"/content") {
			var body struct {
				Content string `json:"content"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			answerPushes = append(answerPushes, body.Content)
			mu.Unlock()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok"})
	}))
	defer srv.Close()

	adapter, _ := NewAdapter(testRegion(srv.URL), "app", "secret", "", "", "")
	placeholder := streamAnswerPlaceholder(adapter.region)
	newPlaceholderTestState(cardID, placeholder)
	clearPlaceholderTestState(t, cardID)

	ctx := context.Background()
	pushes := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), answerPushes...)
	}

	// Thinking-only frames must not repaint the body, and must never blank it.
	if err := adapter.UpdateStructuredStream(ctx, nil, cardID, im.IMStreamRender{
		Thinking: "💡 思考 · 先读资产台账",
		Summary:  "思考 1 轮 · 调用 1 次工具 · 耗时 3s",
	}); err != nil {
		t.Fatalf("thinking-only frame: %v", err)
	}
	if got := pushes(); len(got) != 0 {
		t.Fatalf("answer region repainted without an answer: %q", got)
	}

	// The real answer takes over the region.
	if err := adapter.UpdateStructuredStream(ctx, nil, cardID, im.IMStreamRender{Answer: "IP 归属 CMDB 资产。"}); err != nil {
		t.Fatalf("answer frame: %v", err)
	}
	if got := pushes(); len(got) != 1 || got[0] != "IP 归属 CMDB 资产。" {
		t.Fatalf("answer region = %q, want the answer text", got)
	}

	// A tool call retracts the answer (agent mode re-uses the body for its next
	// optimistic attempt): the placeholder must come back.
	if err := adapter.UpdateStructuredStream(ctx, nil, cardID, im.IMStreamRender{Thinking: "💡 思考 · 再查一次"}); err != nil {
		t.Fatalf("retracted frame: %v", err)
	}
	if got := pushes(); len(got) != 2 || got[1] != placeholder {
		t.Fatalf("answer region = %q, want the placeholder restored", got)
	}
	if strings.TrimSpace(placeholder) == "" {
		t.Fatal("placeholder must not be empty")
	}
}

// A stream that never produced an answer leaves the placeholder in the answer
// region. Feeding that back as the chat-list preview would pin "正在思考..." to a
// finished card.
func TestEndStreamSummarySkipsAnswerPlaceholder(t *testing.T) {
	useTestHTTPClient(t)
	const cardID = "card-placeholder-summary"

	var settingsRaw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "tenant_access_token": "test-token", "expire": 7200,
			})
			return
		}
		var body struct {
			Settings string `json:"settings"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Settings != "" {
			settingsRaw = body.Settings
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok"})
	}))
	defer srv.Close()

	adapter, _ := NewAdapter(testRegion(srv.URL), "app", "secret", "", "", "")
	newPlaceholderTestState(cardID, streamAnswerPlaceholder(adapter.region))
	clearPlaceholderTestState(t, cardID)

	if err := adapter.EndStream(context.Background(), nil, cardID); err != nil {
		t.Fatalf("EndStream: %v", err)
	}
	if settingsRaw == "" {
		t.Fatal("no card settings were delivered")
	}
	if strings.Contains(settingsRaw, adapter.region.ThinkingText) {
		t.Fatalf("chat-list summary inherited the thinking placeholder: %s", settingsRaw)
	}
}

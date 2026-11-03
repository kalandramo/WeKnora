package feishu

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/im"
)

// The structured path writes the answer into its own card element and never
// touches state.content, so EndStream must read the summary from the answer
// region instead of the legacy single-element buffer.
func TestEndStreamSummaryUsesStructuredAnswerRegion(t *testing.T) {
	useTestHTTPClient(t)
	const cardID = "card-structured-summary"

	var settingsRaw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "tenant_access_token": "test-token", "expire": 7200,
			})
			return
		}
		var body struct {
			Settings string `json:"settings"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode %s: %v", r.URL.Path, err)
		}
		if body.Settings != "" {
			settingsRaw = body.Settings
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
	// A structured stream pushes the answer into its own element only.
	if err := adapter.UpdateStructuredStream(ctx, nil, cardID, im.IMStreamRender{
		Thinking: "💭 思考过程",
		Tools:    "[完成] 网络搜索 · 1.50s",
		Answer:   "最终回答内容",
	}); err != nil {
		t.Fatalf("UpdateStructuredStream: %v", err)
	}
	if err := adapter.FinalizeStructuredStream(ctx, nil, cardID, im.IMStreamRender{
		Tools:  "共调用 1 个工具 · 全部成功 · 耗时 1.50s",
		Answer: "最终回答内容",
	}); err != nil {
		t.Fatalf("FinalizeStructuredStream: %v", err)
	}
	if err := adapter.EndStream(ctx, nil, cardID); err != nil {
		t.Fatalf("EndStream: %v", err)
	}

	if settingsRaw == "" {
		t.Fatal("no card settings were delivered")
	}
	assertStreamingClosed(t, settingsRaw, "最终回答内容")
}

// The legacy single-element path must keep working: when no structured answer
// region exists, EndStream falls back to the legacy content buffer.
func TestEndStreamSummaryFallsBackToLegacyContent(t *testing.T) {
	useTestHTTPClient(t)
	const cardID = "card-legacy-fallback"

	var settingsRaw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "tenant_access_token": "test-token", "expire": 7200,
			})
			return
		}
		var body struct {
			Settings string `json:"settings"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode %s: %v", r.URL.Path, err)
		}
		if body.Settings != "" {
			settingsRaw = body.Settings
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
	if err := adapter.UpdateStreamContent(ctx, nil, cardID, "旧路径内容"); err != nil {
		t.Fatalf("UpdateStreamContent: %v", err)
	}
	if err := adapter.EndStream(ctx, nil, cardID); err != nil {
		t.Fatalf("EndStream: %v", err)
	}

	if settingsRaw == "" {
		t.Fatal("no card settings were delivered")
	}
	assertStreamingClosed(t, settingsRaw, "旧路径内容")
}

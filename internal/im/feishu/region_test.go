package feishu

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/im"
)

// The two regions must stay distinct in every dimension that reaches the
// network or the session store: sending a Lark request to open.feishu.cn fails
// authentication, and reporting the wrong platform merges the two clouds'
// sessions together.
func TestRegions_AreDistinct(t *testing.T) {
	if RegionFeishu.OpenBaseURL == RegionLark.OpenBaseURL {
		t.Errorf("regions share OpenBaseURL %q", RegionFeishu.OpenBaseURL)
	}
	if RegionFeishu.Platform == RegionLark.Platform {
		t.Errorf("regions share Platform %q", RegionFeishu.Platform)
	}
	if RegionFeishu.Platform != im.PlatformFeishu {
		t.Errorf("RegionFeishu.Platform = %q, want %q", RegionFeishu.Platform, im.PlatformFeishu)
	}
	if RegionLark.Platform != im.PlatformLark {
		t.Errorf("RegionLark.Platform = %q, want %q", RegionLark.Platform, im.PlatformLark)
	}
}

func TestRegions_BaseURLHosts(t *testing.T) {
	cases := []struct {
		region Region
		want   string
	}{
		{RegionFeishu, "https://open.feishu.cn"},
		{RegionLark, "https://open.larksuite.com"},
	}
	for _, c := range cases {
		if c.region.OpenBaseURL != c.want {
			t.Errorf("%s OpenBaseURL = %q, want %q", c.region.Label, c.region.OpenBaseURL, c.want)
		}
		if strings.HasSuffix(c.region.OpenBaseURL, "/") {
			t.Errorf("%s OpenBaseURL has a trailing slash: %q", c.region.Label, c.region.OpenBaseURL)
		}
	}
}

// Every API call must land on the region's own cloud.
func TestAdapterAPI_UsesRegionHost(t *testing.T) {
	cases := []struct {
		region     Region
		wantPrefix string
	}{
		{RegionFeishu, "https://open.feishu.cn/open-apis/"},
		{RegionLark, "https://open.larksuite.com/open-apis/"},
	}
	for _, c := range cases {
		a, _ := NewAdapter(c.region, "cli_app", "secret", "", "", "")

		got := a.api("/open-apis/im/v1/messages/%s/reply", "om_1")
		want := c.wantPrefix + "im/v1/messages/om_1/reply"
		if got != want {
			t.Errorf("%s api() = %q, want %q", c.region.Label, got, want)
		}

		// A path with no format verbs must survive unchanged.
		if got := a.api("/open-apis/cardkit/v1/cards"); got != c.wantPrefix+"cardkit/v1/cards" {
			t.Errorf("%s api() = %q, want %q", c.region.Label, got, c.wantPrefix+"cardkit/v1/cards")
		}
	}
}

func TestAdapterPlatform_FollowsRegion(t *testing.T) {
	aFeishu, _ := NewAdapter(RegionFeishu, "a", "b", "", "", "")
	if got := aFeishu.Platform(); got != im.PlatformFeishu {
		t.Errorf("Feishu adapter Platform() = %q, want %q", got, im.PlatformFeishu)
	}
	aLark, _ := NewAdapter(RegionLark, "a", "b", "", "", "")
	if got := aLark.Platform(); got != im.PlatformLark {
		t.Errorf("Lark adapter Platform() = %q, want %q", got, im.PlatformLark)
	}
}

// A custom api_base_url overrides the region's Open Platform host, so internal
// deployments can route Feishu API calls through a reverse proxy.
func TestAdapterAPI_BaseURLOverride(t *testing.T) {
	a := &Adapter{apiBaseURL: "https://feishu-proxy.example.internal"}
	got := a.api("/open-apis/im/v1/messages/%s/reply", "om_1")
	want := "https://feishu-proxy.example.internal/open-apis/im/v1/messages/om_1/reply"
	if got != want {
		t.Errorf("override api() = %q, want %q", got, want)
	}
}

// NewAdapter falls back to the region default when api_base_url is empty, and
// the region default itself passes validation (no SSRF check on the default).
func TestNewAdapter_FallbackAndTrim(t *testing.T) {
	a, err := NewAdapter(RegionFeishu, "a", "b", "", "", "")
	if err != nil {
		t.Fatalf("NewAdapter empty api_base_url: %v", err)
	}
	if a.apiBaseURL != RegionFeishu.OpenBaseURL {
		t.Errorf("fallback apiBaseURL = %q, want %q", a.apiBaseURL, RegionFeishu.OpenBaseURL)
	}
	// The region default value also passes (treated as "no override").
	a2, err := NewAdapter(RegionFeishu, "a", "b", "", "", RegionFeishu.OpenBaseURL)
	if err != nil {
		t.Fatalf("NewAdapter default api_base_url: %v", err)
	}
	if a2.apiBaseURL != RegionFeishu.OpenBaseURL {
		t.Errorf("default apiBaseURL = %q", a2.apiBaseURL)
	}
}

// validateAPIBaseURL rejects non-http(s) schemes; empty and the region default
// are allowed without further checks.
func TestValidateAPIBaseURL(t *testing.T) {
	defaults := RegionFeishu.OpenBaseURL
	if err := validateAPIBaseURL("", defaults); err != nil {
		t.Errorf("empty should be allowed: %v", err)
	}
	if err := validateAPIBaseURL(defaults, defaults); err != nil {
		t.Errorf("default should be allowed: %v", err)
	}
	if err := validateAPIBaseURL("ftp://example.com", defaults); err == nil {
		t.Error("ftp scheme should be rejected")
	}
	if err := validateAPIBaseURL("gopher://example.com", defaults); err == nil {
		t.Error("gopher scheme should be rejected")
	}
}

// The streaming card placeholder follows the region so Lark users are not shown
// Chinese copy.
func TestBuildStreamingCardJSON_PlaceholderFollowsRegion(t *testing.T) {
	for _, region := range []Region{RegionFeishu, RegionLark} {
		raw := buildStreamingCardJSON(region)

		var card struct {
			Config struct {
				StreamingMode bool `json:"streaming_mode"`
				Summary       struct {
					Content string `json:"content"`
				} `json:"summary"`
			} `json:"config"`
			Body struct {
				Elements []struct {
					Tag      string `json:"tag"`
					Content  string `json:"content"`
					ElementID string `json:"element_id"`
					Header   struct {
						Title struct {
							Content string `json:"content"`
						} `json:"title"`
						Folded bool `json:"folded"`
					} `json:"header"`
					Elements []struct {
						Content   string `json:"content"`
						ElementID string `json:"element_id"`
					} `json:"elements"`
				} `json:"elements"`
			} `json:"body"`
		}
		if err := json.Unmarshal([]byte(raw), &card); err != nil {
			t.Fatalf("%s card is not valid JSON: %v", region.Label, err)
		}

		if !card.Config.StreamingMode {
			t.Errorf("%s card has streaming_mode disabled", region.Label)
		}
		if card.Config.Summary.Content != region.ThinkingText {
			t.Errorf("%s summary = %q, want %q", region.Label, card.Config.Summary.Content, region.ThinkingText)
		}
		// The card is split into three regions: a collapsible thinking block
		// (folded by default), a tool region, and the answer region on top.
		if len(card.Body.Elements) != 3 {
			t.Fatalf("%s card has %d elements, want 3", region.Label, len(card.Body.Elements))
		}

		thinking := card.Body.Elements[0]
		if thinking.Tag != "collapsible" {
			t.Errorf("%s thinking region tag = %q, want collapsible", region.Label, thinking.Tag)
		}
		if !thinking.Header.Folded {
			t.Errorf("%s thinking region should be folded by default", region.Label)
		}
		if len(thinking.Elements) != 1 {
			t.Fatalf("%s thinking region has %d nested elements, want 1", region.Label, len(thinking.Elements))
		}
		if thinking.Elements[0].ElementID != streamThinkingElemID {
			t.Errorf("%s thinking element_id = %q, want %q", region.Label, thinking.Elements[0].ElementID, streamThinkingElemID)
		}
		if !strings.Contains(thinking.Elements[0].Content, region.ThinkingText) {
			t.Errorf("%s thinking content %q does not contain %q", region.Label, thinking.Elements[0].Content, region.ThinkingText)
		}

		tools := card.Body.Elements[1]
		if tools.ElementID != streamToolsElemID {
			t.Errorf("%s tools element_id = %q, want %q", region.Label, tools.ElementID, streamToolsElemID)
		}
		answer := card.Body.Elements[2]
		if answer.ElementID != streamAnswerElemID {
			t.Errorf("%s answer element_id = %q, want %q", region.Label, answer.ElementID, streamAnswerElemID)
		}
		if streamingElementID != streamAnswerElemID {
			t.Errorf("streamingElementID = %q, want %q", streamingElementID, streamAnswerElemID)
		}
	}

	// Guard the point of the whole exercise: the copy actually differs.
	if RegionFeishu.ThinkingText == RegionLark.ThinkingText {
		t.Error("Feishu and Lark share ThinkingText; Lark users would see Chinese copy")
	}
}

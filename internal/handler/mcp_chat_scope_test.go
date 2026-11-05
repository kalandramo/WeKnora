package handler

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// Validation exists so a typo cannot widen exposure: a group entry without a
// chat ID matches nothing at evaluation time, so accepting it here would leave
// an operator believing a rule is in force when it is not.
func TestNormalizeScopeEntries(t *testing.T) {
	cases := []struct {
		name    string
		input   []types.MCPChatScopeEntry
		wantErr bool
	}{
		{
			name:  "group with id",
			input: []types.MCPChatScopeEntry{{Platform: " Feishu ", ChatType: " GROUP ", ChatID: " oc_ops "}},
		},
		{
			name:  "group wildcard",
			input: []types.MCPChatScopeEntry{{Platform: "feishu", ChatType: "group", ChatID: "*"}},
		},
		{
			name:  "direct without id",
			input: []types.MCPChatScopeEntry{{Platform: "feishu", ChatType: "direct"}},
		},
		{
			name:    "group without id",
			input:   []types.MCPChatScopeEntry{{Platform: "feishu", ChatType: "group"}},
			wantErr: true,
		},
		{
			name:    "direct with a group id",
			input:   []types.MCPChatScopeEntry{{Platform: "feishu", ChatType: "direct", ChatID: "oc_ops"}},
			wantErr: true,
		},
		{
			name:    "unknown chat type",
			input:   []types.MCPChatScopeEntry{{Platform: "feishu", ChatType: "channel", ChatID: "oc_ops"}},
			wantErr: true,
		},
		{
			name:    "missing platform",
			input:   []types.MCPChatScopeEntry{{ChatType: "group", ChatID: "oc_ops"}},
			wantErr: true,
		},
		{
			name:    "missing chat type",
			input:   []types.MCPChatScopeEntry{{Platform: "feishu", ChatID: "oc_ops"}},
			wantErr: true,
		},
		{
			name:  "empty list is allowed (emergency shutdown)",
			input: nil,
		},
	}

	for _, c := range cases {
		got, err := normalizeScopeEntries(c.input)
		if c.wantErr {
			if err == nil {
				t.Errorf("%s: expected an error, got %+v", c.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", c.name, err)
			continue
		}
		if len(got) != len(c.input) {
			t.Errorf("%s: got %d entries, want %d", c.name, len(got), len(c.input))
		}
	}

	// Normalization trims and lowercases so matching is not whitespace-sensitive.
	got, err := normalizeScopeEntries([]types.MCPChatScopeEntry{
		{Platform: " Feishu ", ChatType: " GROUP ", ChatID: " oc_ops ", Label: " 运维群 "},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got[0].Platform != "feishu" || got[0].ChatType != "group" ||
		got[0].ChatID != "oc_ops" || got[0].Label != "运维群" {
		t.Fatalf("entry not normalized: %+v", got[0])
	}
}

func TestMarshalSpecRoundTrips(t *testing.T) {
	allow := []types.MCPChatScopeEntry{{Platform: "feishu", ChatType: "group", ChatID: "oc_ops"}}
	raw, err := marshalSpec(allow)
	if err != nil {
		t.Fatalf("marshalSpec: %v", err)
	}
	if raw != `{"allow":[{"platform":"feishu","chat_type":"group","chat_id":"oc_ops"}]}` {
		t.Fatalf("unexpected spec encoding: %s", raw)
	}
	// An empty allowlist must still encode a list, not null: the decoder and the
	// API response both assume an array.
	empty, err := marshalSpec(nil)
	if err != nil {
		t.Fatalf("marshalSpec(nil): %v", err)
	}
	if empty != `{"allow":[]}` {
		t.Fatalf("unexpected empty spec encoding: %s", empty)
	}
}

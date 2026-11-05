package im

import "testing"

// The tool layer decides chat scoping from this identity, so the mapping has to
// preserve the two facts it relies on: the platform (an allowlist entry may name
// one) and the chat type (a direct message has no group ID to match).
func TestMCPChatIdentityOf(t *testing.T) {
	cases := []struct {
		name   string
		msg    *IncomingMessage
		wantOK bool
		want   string // platform/chatType/chatID joined for a compact assertion
	}{
		{
			name:   "group chat",
			msg:    &IncomingMessage{Platform: PlatformFeishu, ChatType: ChatTypeGroup, ChatID: " oc_ops "},
			wantOK: true,
			want:   "feishu/group/oc_ops",
		},
		{
			// Feishu leaves ChatID empty for a private chat; the chat type is the
			// only thing that distinguishes it.
			name:   "direct message",
			msg:    &IncomingMessage{Platform: PlatformFeishu, ChatType: ChatTypeDirect},
			wantOK: true,
			want:   "feishu/direct/",
		},
		{
			name:   "message without a platform is not an IM identity",
			msg:    &IncomingMessage{ChatType: ChatTypeGroup, ChatID: "oc_ops"},
			wantOK: false,
		},
		{
			name:   "nil message",
			msg:    nil,
			wantOK: false,
		},
	}

	for _, c := range cases {
		id, ok := mcpChatIdentityOf(c.msg)
		if ok != c.wantOK {
			t.Errorf("%s: ok = %v, want %v", c.name, ok, c.wantOK)
			continue
		}
		if !ok {
			continue
		}
		got := id.Platform + "/" + id.ChatType + "/" + id.ChatID
		if got != c.want {
			t.Errorf("%s: identity = %q, want %q", c.name, got, c.want)
		}
	}
}

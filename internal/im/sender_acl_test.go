package im

import (
	"testing"
)

func TestParseBotSenderWhitelist(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want map[string]bool
	}{
		{name: "empty blocks every bot", raw: "", want: nil},
		{name: "whitespace only blocks every bot", raw: "  , ,, ", want: nil},
		{name: "single id", raw: "ou_abc", want: map[string]bool{"ou_abc": true}},
		{name: "multiple ids trimmed", raw: " ou_abc , ou_def  ,", want: map[string]bool{"ou_abc": true, "ou_def": true}},
		{name: "wildcard", raw: "*", want: map[string]bool{"*": true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseBotSenderWhitelist(tt.raw)
			if tt.want == nil {
				if got != nil {
					t.Fatalf("parseBotSenderWhitelist(%q) = %v, want nil", tt.raw, got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("parseBotSenderWhitelist(%q) = %v, want %v", tt.raw, got, tt.want)
			}
			for k := range tt.want {
				if !got[k] {
					t.Fatalf("parseBotSenderWhitelist(%q) missing %q: %v", tt.raw, k, got)
				}
			}
		})
	}
}

func TestAllowBotSender(t *testing.T) {
	orig := botSenderAllowList
	defer func() { botSenderAllowList = orig }()

	t.Run("nil message passes", func(t *testing.T) {
		botSenderAllowList = nil
		if !AllowBotSender(nil) {
			t.Fatal("nil message must pass (defensive)")
		}
	})

	t.Run("human sender always passes even with empty whitelist", func(t *testing.T) {
		botSenderAllowList = nil
		msg := &IncomingMessage{UserID: "ou_human"}
		if !AllowBotSender(msg) {
			t.Fatal("missing sender_type (human) must pass")
		}
		msg.Extra = map[string]string{SenderTypeExtraKey: "user"}
		if !AllowBotSender(msg) {
			t.Fatal("explicit user sender must pass")
		}
	})

	t.Run("bot sender blocked by default", func(t *testing.T) {
		botSenderAllowList = nil
		msg := &IncomingMessage{
			UserID: "ou_bot_other",
			Extra:  map[string]string{SenderTypeExtraKey: SenderTypeApp},
		}
		if AllowBotSender(msg) {
			t.Fatal("bot sender with empty whitelist must be blocked")
		}
	})

	t.Run("whitelisted bot sender passes", func(t *testing.T) {
		botSenderAllowList = parseBotSenderWhitelist("ou_bot_ok")
		msg := &IncomingMessage{
			UserID: "ou_bot_ok",
			Extra:  map[string]string{SenderTypeExtraKey: SenderTypeApp},
		}
		if !AllowBotSender(msg) {
			t.Fatal("whitelisted bot sender must pass")
		}
		msg2 := &IncomingMessage{
			UserID: "ou_bot_other",
			Extra:  map[string]string{SenderTypeExtraKey: SenderTypeApp},
		}
		if AllowBotSender(msg2) {
			t.Fatal("non-whitelisted bot sender must be blocked")
		}
	})

	t.Run("wildcard admits every bot sender", func(t *testing.T) {
		botSenderAllowList = parseBotSenderWhitelist("*")
		msg := &IncomingMessage{
			UserID: "ou_any_bot",
			Extra:  map[string]string{SenderTypeExtraKey: SenderTypeApp},
		}
		if !AllowBotSender(msg) {
			t.Fatal("wildcard whitelist must pass any bot sender")
		}
	})
}

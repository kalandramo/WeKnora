package im

import "testing"

func TestNormalizeAtToken(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"canonical", "@_all", "@_all"},
		{"feishu text escape", "@//_all/", "@_all"},
		{"trailing slash only", "@_all/", "@_all"},
		{"uppercase", "@_ALL", "@_all"},
		{"padded", "  @//_all/  ", "@_all"},
		{"empty", "", ""},
		{"blank", "   ", ""},
		{"bot mention", "@_user_1", "@_user_1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeAtToken(tc.in); got != tc.want {
				t.Fatalf("normalizeAtToken(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsAtAllToken(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"canonical", "@_all", true},
		{"feishu text escape", "@//_all/", true},
		{"uppercase", "@_ALL", true},
		{"padded", " @//_all/ ", true},
		// A post's at element carries the clean form.
		{"post at element", "@_all", true},
		{"single user", "@_user_1", false},
		{"open id", "ou_001462438fc0ef7718e21ee2b4fdbfb1", false},
		{"bare at all", "@all", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsAtAllToken(tc.in); got != tc.want {
				t.Fatalf("IsAtAllToken(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestContainsAtAllToken(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		// Observed production sample: text messages escape the token and put a
		// leading space before it, which defeats a HasPrefix check.
		{"observed text sample", ` @//_all/ 文本所有人测试`, true},
		{"leading space canonical", ` @_all 文本所有人测试`, true},
		{"no leading space", `@//_all/ 所有人测试`, true},
		{"mention at end", `这个软件我们应该没有人在用吧？ @_all`, true},
		{"adjacent to han characters", `测试@//_all/测试`, true},
		{"uppercase", `@_ALL 注意`, true},
		{"prose about the token", `请讨论 @_all 这个语法`, true}, // plan A: matching means dropping
		{"single user mention", `@_user_1 你好`, false},
		{"longer identifier", `@_all_extra 你好`, false},
		{"bare at all", `@all 你好`, false},
		{"no mention", `这个软件我们应该没有人在用吧？`, false},
		{"empty", ``, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ContainsAtAllToken(tc.in); got != tc.want {
				t.Fatalf("ContainsAtAllToken(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestMarkAtAllAndIsAtAllMessage(t *testing.T) {
	t.Run("nil message is ignored", func(t *testing.T) {
		MarkAtAll(nil) // must not panic
		if IsAtAllMessage(nil) {
			t.Fatal("IsAtAllMessage(nil) = true, want false")
		}
	})

	t.Run("creates extra map", func(t *testing.T) {
		msg := &IncomingMessage{}
		MarkAtAll(msg)
		if msg.Extra == nil {
			t.Fatal("Extra = nil, want initialized map")
		}
		if !IsAtAllMessage(msg) {
			t.Fatal("IsAtAllMessage() = false after MarkAtAll")
		}
	})

	t.Run("keeps existing extras", func(t *testing.T) {
		msg := &IncomingMessage{Extra: map[string]string{SenderTypeExtraKey: SenderTypeUser}}
		MarkAtAll(msg)
		if msg.Extra[SenderTypeExtraKey] != SenderTypeUser {
			t.Fatalf("sender_type = %q, want %q", msg.Extra[SenderTypeExtraKey], SenderTypeUser)
		}
		if !IsAtAllMessage(msg) {
			t.Fatal("IsAtAllMessage() = false after MarkAtAll")
		}
	})

	t.Run("plain message is not at-all", func(t *testing.T) {
		msg := &IncomingMessage{Extra: map[string]string{SenderTypeExtraKey: SenderTypeUser}}
		if IsAtAllMessage(msg) {
			t.Fatal("IsAtAllMessage() = true for a plain message")
		}
	})
}

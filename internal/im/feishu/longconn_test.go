package feishu

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/im"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func stringPtr(s string) *string { return &s }

func TestConvertEventIgnoresGroupMessageWithoutBotMention(t *testing.T) {
	event := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				MessageType: stringPtr("text"),
				ChatType:    stringPtr("group"),
				ChatId:      stringPtr("oc_group"),
				MessageId:   stringPtr("om_message"),
				Content:     stringPtr(`{"text":"知识库最新文档"}`),
			},
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{OpenId: stringPtr("ou_sender")},
			},
		},
	}

	if got := convertEvent(RegionFeishu, "ou_bot", event); got != nil {
		t.Fatalf("convertEvent() = %+v, want nil for group message without bot mention", got)
	}
}

func TestConvertEventKeepsGroupMessageThatMentionsBot(t *testing.T) {
	event := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				MessageType: stringPtr("text"),
				ChatType:    stringPtr("group"),
				ChatId:      stringPtr("oc_group"),
				MessageId:   stringPtr("om_message"),
				Content:     stringPtr(`{"text":"@_user_1 亚信有什么ai安全产品"}`),
				Mentions: []*larkim.MentionEvent{{
					Key:           stringPtr("@_user_1"),
					MentionedType: stringPtr("bot"),
					Name:          stringPtr("WeKnora"),
					Id:            &larkim.UserId{OpenId: stringPtr("ou_bot")},
				}},
			},
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{OpenId: stringPtr("ou_sender")},
			},
		},
	}

	got := convertEvent(RegionFeishu, "ou_bot", event)
	if got == nil {
		t.Fatal("convertEvent() returned nil for group message that mentions the bot")
	}
	if got.Content != "亚信有什么ai安全产品" {
		t.Fatalf("Content = %q, want %q", got.Content, "亚信有什么ai安全产品")
	}
	if got.ChatType != im.ChatTypeGroup {
		t.Fatalf("ChatType = %q, want %q", got.ChatType, im.ChatTypeGroup)
	}
}

func TestConvertEventIgnoresGroupMessageThatMentionsDifferentBot(t *testing.T) {
	event := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				MessageType: stringPtr("text"),
				ChatType:    stringPtr("group"),
				ChatId:      stringPtr("oc_group"),
				MessageId:   stringPtr("om_message"),
				Content:     stringPtr(`{"text":"@_user_1 hello"}`),
				Mentions: []*larkim.MentionEvent{{
					Key:           stringPtr("@_user_1"),
					MentionedType: stringPtr("bot"),
					Name:          stringPtr("OtherBot"),
					Id:            &larkim.UserId{OpenId: stringPtr("ou_other_bot")},
				}},
			},
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{OpenId: stringPtr("ou_sender")},
			},
		},
	}

	if got := convertEvent(RegionFeishu, "ou_bot", event); got != nil {
		t.Fatalf("convertEvent() = %+v, want nil for group message mentioning a different bot", got)
	}
}

func TestConvertPostEventPreservesEmbeddedImage(t *testing.T) {
	content := `{"title":"","content":[[{"tag":"at","user_id":"ou_bot"},{"tag":"text","text":"请描述这张图"},{"tag":"img","image_key":"img_v3_abc"}]]}`
	msg := &larkim.EventMessage{Content: &content}

	got := convertPostEvent(RegionFeishu, msg, "ou_user", "oc_group", im.ChatTypeGroup, "om_message")
	if got == nil {
		t.Fatal("convertPostEvent() returned nil")
	}
	if got.MessageType != im.MessageTypeImage {
		t.Fatalf("MessageType = %q, want %q", got.MessageType, im.MessageTypeImage)
	}
	if got.Content != "请描述这张图" {
		t.Fatalf("Content = %q, want %q", got.Content, "请描述这张图")
	}
	if got.FileKey != "img_v3_abc" {
		t.Fatalf("FileKey = %q, want %q", got.FileKey, "img_v3_abc")
	}
	if got.FileName != "img_v3_abc.png" {
		t.Fatalf("FileName = %q, want %q", got.FileName, "img_v3_abc.png")
	}
}

func TestConvertPostEventPreservesImageWithoutText(t *testing.T) {
	content := `{"title":"","content":[[{"tag":"at","user_id":"ou_bot"},{"tag":"img","image_key":"img_v3_only"}]]}`
	msg := &larkim.EventMessage{Content: &content}

	got := convertPostEvent(RegionFeishu, msg, "ou_user", "oc_group", im.ChatTypeGroup, "om_message")
	if got == nil {
		t.Fatal("convertPostEvent() returned nil")
	}
	if got.MessageType != im.MessageTypeImage {
		t.Fatalf("MessageType = %q, want %q", got.MessageType, im.MessageTypeImage)
	}
	if got.Content != "" {
		t.Fatalf("Content = %q, want empty", got.Content)
	}
	if got.FileKey != "img_v3_only" {
		t.Fatalf("FileKey = %q, want %q", got.FileKey, "img_v3_only")
	}
}

func TestConvertPostEventKeepsTextOnlyPostsAsText(t *testing.T) {
	content := `{"title":"Status","content":[[{"tag":"text","text":"all systems go"}]]}`
	msg := &larkim.EventMessage{Content: &content}

	got := convertPostEvent(RegionFeishu, msg, "ou_user", "oc_group", im.ChatTypeGroup, "om_message")
	if got == nil {
		t.Fatal("convertPostEvent() returned nil")
	}
	if got.MessageType != im.MessageTypeText {
		t.Fatalf("MessageType = %q, want %q", got.MessageType, im.MessageTypeText)
	}
	if got.Content != "Status\nall systems go" {
		t.Fatalf("Content = %q, want %q", got.Content, "Status\\nall systems go")
	}
	if got.FileKey != "" {
		t.Fatalf("FileKey = %q, want empty", got.FileKey)
	}
}

// at_all flags: Feishu encodes a mention-everyone differently per message
// type, so both shapes are covered here with production samples.

// strPtr is a helper for the SDK's *string fields.
func strPtr(s string) *string { return &s }

func TestConvertTextEventMarksAtAll(t *testing.T) {
	// Production sample: the escaped token is preceded by a space, which is
	// why detection cannot rely on a prefix match.
	content := `{"text":" @//_all/ 文本所有人测试"}`
	msg := &larkim.EventMessage{
		Content:     &content,
		MessageType: strPtr("text"),
		ChatType:    strPtr("group"),
	}

	got := convertTextEvent(RegionFeishu, msg, "ou_user", "oc_group", im.ChatTypeGroup, "om_message")
	if got == nil {
		t.Fatal("convertTextEvent() returned nil")
	}
	if !im.IsAtAllMessage(got) {
		t.Fatalf("at_all flag missing; Extra = %#v", got.Extra)
	}
	// The token is intentionally left in Content: dropping it would rewrite
	// the user's text, and the message is discarded anyway.
	if !im.ContainsAtAllToken(got.Content) {
		t.Fatalf("Content = %q, want the escaped token preserved", got.Content)
	}
}

func TestConvertTextEventWithoutAtAllIsNotMarked(t *testing.T) {
	cases := []struct{ name, content string }{
		{"plain question", `{"text":"这个软件我们应该没有人在用吧？"}`},
		{"single user mention", `{"text":"@_user_1 帮我查一下"}`},
		{"bare at all", `{"text":"@all 你好"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := &larkim.EventMessage{
				Content:     &tc.content,
				MessageType: strPtr("text"),
				ChatType:    strPtr("group"),
			}
			got := convertTextEvent(RegionFeishu, msg, "ou_user", "oc_group", im.ChatTypeGroup, "om_message")
			if got == nil {
				t.Fatal("convertTextEvent() returned nil")
			}
			if im.IsAtAllMessage(got) {
				t.Fatalf("at_all flag set for %q", tc.content)
			}
		})
	}
}

func TestConvertPostEventMarksAtAll(t *testing.T) {
	// Production sample: a post carrying an image, a question and an
	// at element whose user_id is "@_all".
	content := `{"title":"","content":[[{"tag":"img","image_key":"img_v3_abc","width":1437,"height":1301}],[{"tag":"text","text":"这个软件我们应该没有人在用吧？","style":[]},{"tag":"at","user_id":"@_all","user_name":"所有人","style":[]},{"tag":"text","text":"","style":[]}]]}`
	msg := &larkim.EventMessage{
		Content:     &content,
		MessageType: strPtr("post"),
		ChatType:    strPtr("group"),
	}

	got := convertPostEvent(RegionFeishu, msg, "ou_user", "oc_group", im.ChatTypeGroup, "om_message")
	if got == nil {
		t.Fatal("convertPostEvent() returned nil")
	}
	if !im.IsAtAllMessage(got) {
		t.Fatalf("at_all flag missing; Extra = %#v", got.Extra)
	}
	// The at element must not leak into Content — the message pipeline still
	// sees the question exactly as before.
	if got.Content != "这个软件我们应该没有人在用吧？" {
		t.Fatalf("Content = %q, want %q", got.Content, "这个软件我们应该没有人在用吧？")
	}
	if got.MessageType != im.MessageTypeImage {
		t.Fatalf("MessageType = %q, want %q", got.MessageType, im.MessageTypeImage)
	}
}

func TestConvertPostEventUserMentionIsNotAtAll(t *testing.T) {
	content := `{"title":"","content":[[{"tag":"at","user_id":"ou_bot","user_name":"智能助手"},{"tag":"text","text":"帮我看看这个"}] ]}`
	msg := &larkim.EventMessage{
		Content:     &content,
		MessageType: strPtr("post"),
		ChatType:    strPtr("group"),
	}

	got := convertPostEvent(RegionFeishu, msg, "ou_user", "oc_group", im.ChatTypeGroup, "om_message")
	if got == nil {
		t.Fatal("convertPostEvent() returned nil")
	}
	if im.IsAtAllMessage(got) {
		t.Fatalf("at_all flag set for a single-user mention; Extra = %#v", got.Extra)
	}
}

func TestConvertEventKeepsAtAllAlongsideSenderType(t *testing.T) {
	// convertEvent stamps sender_type into Extra after the converter returns;
	// a pre-existing at_all flag must survive that write.
	content := `{"text":" @//_all/ 文本所有人测试"}`
	chatType, chatID, messageID, messageType := "group", "oc_group", "om_message", "text"
	openID, senderType := "ou_user", im.SenderTypeUser

	event := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender: &larkim.EventSender{
				SenderId:   &larkim.UserId{OpenId: &openID},
				SenderType: &senderType,
			},
			Message: &larkim.EventMessage{
				MessageId:   &messageID,
				ChatId:      &chatID,
				ChatType:    &chatType,
				MessageType: &messageType,
				Content:     &content,
			},
		},
	}

	got := convertEvent(RegionFeishu, event)
	if got == nil {
		t.Fatal("convertEvent() returned nil")
	}
	if !im.IsAtAllMessage(got) {
		t.Fatalf("at_all flag missing; Extra = %#v", got.Extra)
	}
	if got.Extra[im.SenderTypeExtraKey] != im.SenderTypeUser {
		t.Fatalf("sender_type = %q, want %q", got.Extra[im.SenderTypeExtraKey], im.SenderTypeUser)
	}
}

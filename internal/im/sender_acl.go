package im

import (
	"os"
	"strings"
)

// EnvIMBotSenderWhitelist names the environment variable holding the
// comma-separated list of bot/app senders allowed to reach the QA pipeline.
//
// Feishu (and Lark) stamp every incoming message with sender.sender_type,
// which is "user" for human members and "app" for anything sent by a bot
// application. Messages from other apps arrive on the same event stream as
// human messages and are indistinguishable without that field, so two
// connected bots can end up answering each other in a loop. The whitelist
// closes that hole: an "app" sender passes only when its open_id is listed,
// and an empty list (the default) blocks every bot sender.
//
// The single entry "*" whitelists all bot senders — useful to restore the
// pre-ACL behaviour for a deployment that deliberately wants bot-to-bot
// conversations.
//
// The value is read once at process start; changing it requires a restart.
// Human senders are never affected: a missing or "user" sender_type always
// passes, so deployments that never set the variable keep today's behaviour
// for people and only gain the default-deny for other applications.
const EnvIMBotSenderWhitelist = "IM_BOT_SENDER_WHITELIST"

// SenderTypeExtraKey is the IncomingMessage.Extra key adapters use to report
// the platform's sender classification (e.g. Feishu's sender.sender_type).
const SenderTypeExtraKey = "sender_type"

// SenderTypeApp marks a message sent by a bot application rather than a
// human member.
const SenderTypeApp = "app"

// wildcardAll Senders whitelists every bot sender when listed instead of an
// open_id.
const wildcardAllSenders = "*"

// botSenderAllowList is the parsed whitelist. Package-level so HandleMessage
// takes no extra wiring; tests reassign it via parseBotSenderWhitelist.
var botSenderAllowList = parseBotSenderWhitelist(os.Getenv(EnvIMBotSenderWhitelist))

// parseBotSenderWhitelist splits the raw env value on commas, trims
// whitespace and drops empty entries. The returned set is nil for an empty
// value so callers can distinguish "no whitelist configured".
func parseBotSenderWhitelist(raw string) map[string]bool {
	entries := strings.Split(raw, ",")
	set := make(map[string]bool, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		set[entry] = true
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

// AllowBotSender reports whether an incoming message may proceed to the QA
// pipeline under the bot-sender whitelist.
//
// Human senders (no sender_type, or anything other than "app") always pass —
// the ACL only narrows what other applications can do. An "app" sender passes
// only when its open_id (IncomingMessage.UserID) is whitelisted, or when the
// list contains the "*" wildcard.
func AllowBotSender(msg *IncomingMessage) bool {
	if msg == nil || msg.Extra[SenderTypeExtraKey] != SenderTypeApp {
		return true
	}
	if botSenderAllowList == nil {
		return false
	}
	return botSenderAllowList[msg.UserID] || botSenderAllowList[wildcardAllSenders]
}

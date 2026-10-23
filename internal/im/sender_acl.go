package im

import (
	"os"
	"strings"
)

// EnvIMBotSenderWhitelist names the environment variable holding the
// comma-separated list of automated senders allowed to reach the QA pipeline.
//
// Feishu (and Lark) stamp every incoming message with sender.sender_type,
// which classifies the originator: "user" for a human member, "bot" for a
// message posted by a bot identity and "app" for one posted through an app
// identity. Messages from other bots arrive on the same event stream as human
// messages and are indistinguishable without that field, so two connected
// bots can end up answering each other in a loop. The whitelist closes that
// hole: an automated sender passes only when its open_id is listed, and an
// empty list (the default) blocks every automated sender.
//
// The single entry "*" whitelists all automated senders — useful to restore
// the pre-ACL behaviour for a deployment that deliberately wants bot-to-bot
// conversations.
//
// The value is read once at process start; changing it requires a restart.
// Human senders are never affected: a missing or "user" sender_type always
// passes, so deployments that never set the variable keep today's behaviour
// for people and only gain the default-deny for automation.
const EnvIMBotSenderWhitelist = "IM_BOT_SENDER_WHITELIST"

// SenderTypeExtraKey is the IncomingMessage.Extra key adapters use to report
// the platform's sender classification (e.g. Feishu's sender.sender_type).
const SenderTypeExtraKey = "sender_type"

// SenderTypeUser marks a message sent by a human member.
const SenderTypeUser = "user"

// SenderTypeApp marks a message sent through an app (application) identity.
const SenderTypeApp = "app"

// SenderTypeBot marks a message sent through a bot identity. Feishu uses this
// value (not "app") for messages posted by a bot, as observed on
// im.message.receive_v1 — gating only "app" would let every bot through.
const SenderTypeBot = "bot"

// wildcardAllSenders whitelists every automated sender when listed instead of
// an open_id.
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

// isAutomatedSender reports whether a sender_type denotes a non-human sender.
//
// An empty value means the adapter did not classify the sender — another
// platform, or an event that omits the field — and keeps the pre-ACL
// behaviour. Everything else that is not a human ("user") is treated as
// automated, so a classification added by the platform later cannot silently
// bypass the whitelist.
func isAutomatedSender(senderType string) bool {
	senderType = strings.TrimSpace(senderType)
	return senderType != "" && senderType != SenderTypeUser
}

// AllowBotSender reports whether an incoming message may proceed to the QA
// pipeline under the automated-sender whitelist.
//
// Human senders always pass — the ACL only narrows what automation can do. An
// automated sender ("bot", "app", or any other non-"user" classification)
// passes only when its open_id (IncomingMessage.UserID) is whitelisted, or
// when the list contains the "*" wildcard. With no list configured, every
// automated sender is dropped.
func AllowBotSender(msg *IncomingMessage) bool {
	if msg == nil || !isAutomatedSender(msg.Extra[SenderTypeExtraKey]) {
		return true
	}
	if botSenderAllowList == nil {
		return false
	}
	return botSenderAllowList[msg.UserID] || botSenderAllowList[wildcardAllSenders]
}

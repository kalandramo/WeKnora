package im

import "strings"

// AtAllExtraKey is the IncomingMessage.Extra key adapters use to report that a
// message mentions everyone in the chat ("@all").
//
// The flag is stamped by the adapter because the mention is not reliably
// recoverable from IncomingMessage.Content: Feishu encodes it as the escaped
// token "@//_all/" in text messages, but as a structured {"tag":"at"} element
// in rich-text (post) messages — and convertPostEvent drops those elements
// while building the plain-text content. Folding both shapes into one Extra
// key lets the service gate on a single, platform-neutral condition.
const AtAllExtraKey = "at_all"

// atAllExtraValue is the value stored under AtAllExtraKey for a match. Any
// non-empty marker is treated as "mentioned everyone".
const atAllExtraValue = "1"

// AtAllToken is the canonical spelling of the "mention everyone" token.
//
// Feishu escapes the token differently per message type — "@//_all/" inside a
// text message, "@_all" in a post's at element's user_id — so comparisons
// normalize first (see normalizeAtToken) instead of matching a literal.
const AtAllToken = "@_all"

// normalizeAtToken lowercases a token and strips the slashes Feishu inserts
// when escaping a mention inside text messages ("@//_all/" -> "@_all"), so
// every observed spelling collapses to the canonical form.
func normalizeAtToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	token = strings.ReplaceAll(token, "/", "")
	return strings.ToLower(token)
}

// IsAtAllToken reports whether a single token (or an at element's user_id)
// denotes "everyone".
func IsAtAllToken(token string) bool {
	normalized := normalizeAtToken(token)
	return normalized != "" && normalized == AtAllToken
}

// isTokenByte reports whether b can continue an identifier-like token, used to
// keep "@_all" from matching inside a longer word such as "@_all_extra".
func isTokenByte(b byte) bool {
	return b == '_' ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9')
}

// ContainsAtAllToken reports whether free-form text mentions everyone.
//
// The token is looked up after stripping slashes and lowercasing, so the
// escaped form "@//_all/" matches just like "@_all". Matching is
// boundary-aware rather than prefix-based: Feishu puts a leading space before
// an escaped mention (e.g. " @//_all/ ..."), so a HasPrefix check on the raw
// content would silently miss every real message.
func ContainsAtAllToken(text string) bool {
	if text == "" || !strings.Contains(text, "@") {
		return false
	}
	normalized := strings.ReplaceAll(strings.ToLower(text), "/", "")
	for offset := 0; offset < len(normalized); {
		pos := strings.Index(normalized[offset:], AtAllToken)
		if pos < 0 {
			return false
		}
		end := offset + pos + len(AtAllToken)
		// The token must not continue into a longer identifier.
		if end >= len(normalized) || !isTokenByte(normalized[end]) {
			return true
		}
		offset += pos + 1
	}
	return false
}

// MarkAtAll records on msg that it mentions everyone. Adapters call this after
// building the IncomingMessage; the Extra map is created on demand so adapters
// stay free of nil checks.
func MarkAtAll(msg *IncomingMessage) {
	if msg == nil {
		return
	}
	if msg.Extra == nil {
		msg.Extra = make(map[string]string, 2)
	}
	msg.Extra[AtAllExtraKey] = atAllExtraValue
}

// IsAtAllMessage reports whether msg mentions everyone.
//
// Such a message is a broadcast rather than a question directed at the
// assistant, so HandleMessage drops it: replying would post an answer into a
// group thread that nobody asked into, and the reply would go to every member
// regardless of whether they are entitled to the retrieved content.
func IsAtAllMessage(msg *IncomingMessage) bool {
	if msg == nil {
		return false
	}
	return msg.Extra[AtAllExtraKey] == atAllExtraValue
}

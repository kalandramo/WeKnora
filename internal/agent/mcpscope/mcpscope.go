// Package mcpscope restricts selected MCP tools to a set of IM conversations.
//
// It is a decorator on approval.Checker rather than a change inside the MCP
// catalog: every exposure and execution decision (visibleTools, checkEnabled,
// MCPTool.Execute, list_servers) already funnels through the Gate, so wrapping
// the gate's checker covers all of them without touching upstream files.
//
// The mechanism can only subtract exposure. A tool without a configured scope
// is untouched, and a run that did not start from an IM message (Web, API,
// scheduled work) keeps the upstream policy chain.
package mcpscope

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Store reads the allowlists of one MCP service. It returns every scoped tool
// of the service in one call, so a directory of twenty tools costs one query
// rather than twenty.
type Store interface {
	ListByService(ctx context.Context, tenantID uint64, serviceID string) (map[string]types.MCPChatScope, error)
}

// Decorator applies chat scoping on top of the upstream MCP policy checker.
type Decorator struct {
	inner approval.Checker
	store Store
}

var (
	_ approval.Checker            = (*Decorator)(nil)
	_ approval.BulkEnabledChecker = (*Decorator)(nil)
	_ approval.RiskChecker        = (*Decorator)(nil)
)

// NewDecorator wraps a checker. A nil store or nil inner checker degrades to
// pass-through, so wiring mistakes disable the feature instead of taking every
// MCP tool offline.
func NewDecorator(inner approval.Checker, store Store) *Decorator {
	return &Decorator{inner: inner, store: store}
}

// IsRequired delegates the approval requirement: chat scoping decides where a
// tool may run, never whether a run needs human approval.
func (d *Decorator) IsRequired(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error) {
	if d == nil || d.inner == nil {
		return false, nil
	}
	return d.inner.IsRequired(ctx, tenantID, serviceID, toolName)
}

// IsEnabled is the single-tool gate: the upstream decision first, then the chat
// allowlist.
func (d *Decorator) IsEnabled(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error) {
	if d == nil || d.inner == nil {
		return true, nil
	}
	enabled, err := d.inner.IsEnabled(ctx, tenantID, serviceID, toolName)
	if err != nil || !enabled {
		return enabled, err
	}
	return d.allowed(ctx, tenantID, serviceID, toolName)
}

// EnabledTools is the batch gate used when enumerating a service directory.
// Implementing it keeps the scope read at one query per service; without it the
// generic fallback would call IsEnabled once per tool and pay that query each
// time.
func (d *Decorator) EnabledTools(
	ctx context.Context, tenantID uint64, serviceID string, names []string,
) (map[string]bool, error) {
	if d == nil || d.inner == nil {
		enabled := make(map[string]bool, len(names))
		for _, name := range names {
			enabled[name] = true
		}
		return enabled, nil
	}

	var (
		upstream map[string]bool
		err      error
	)
	if bulk, ok := d.inner.(approval.BulkEnabledChecker); ok {
		upstream, err = bulk.EnabledTools(ctx, tenantID, serviceID, names)
	} else {
		upstream, err = approval.EnabledTools(ctx, d.inner, tenantID, serviceID, names)
	}
	if err != nil {
		return nil, err
	}

	// A run that did not start from an IM message is governed entirely by the
	// upstream chain: skip the scope read, so Web, API and scheduled work pay
	// neither the query nor a new failure mode.
	if _, fromIM := types.MCPChatIdentityFromContext(ctx); !fromIM {
		return upstream, nil
	}

	scopes, err := d.scopes(ctx, tenantID, serviceID)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		if !upstream[name] {
			continue
		}
		ok, err := d.decide(ctx, scopes, serviceID, name)
		if err != nil {
			return nil, err
		}
		upstream[name] = ok
	}
	return upstream, nil
}

// Policy passes the stored per-tool policy through. The risk assessment reads it
// through a type assertion on the gate's checker; a decorator that dropped this
// would silently strip risk grading from every MCP tool.
func (d *Decorator) Policy(
	ctx context.Context, tenantID uint64, serviceID, toolName string,
) (*types.MCPToolApproval, error) {
	risk, ok := d.inner.(approval.RiskChecker)
	if !ok {
		return nil, nil
	}
	return risk.Policy(ctx, tenantID, serviceID, toolName)
}

// ObserveRisk passes a declared risk through to the policy store.
func (d *Decorator) ObserveRisk(ctx context.Context, tenantID uint64, serviceID, toolName, risk string) error {
	recorder, ok := d.inner.(approval.RiskChecker)
	if !ok {
		return nil
	}
	return recorder.ObserveRisk(ctx, tenantID, serviceID, toolName, risk)
}

func (d *Decorator) allowed(ctx context.Context, tenantID uint64, serviceID, toolName string) (bool, error) {
	// Without an IM conversation there is nothing to scope against. Returning
	// early keeps the upstream answer final and avoids both the query and the
	// fail-closed path for callers this mechanism does not govern.
	if _, fromIM := types.MCPChatIdentityFromContext(ctx); !fromIM {
		return true, nil
	}
	scopes, err := d.scopes(ctx, tenantID, serviceID)
	if err != nil {
		return false, err
	}
	return d.decide(ctx, scopes, serviceID, toolName)
}

func (d *Decorator) scopes(
	ctx context.Context, tenantID uint64, serviceID string,
) (map[string]types.MCPChatScope, error) {
	if d.store == nil {
		return nil, nil
	}
	scopes, err := d.store.ListByService(ctx, tenantID, serviceID)
	if err != nil {
		// Fail closed: the caller turns this into "permissions are temporarily
		// unavailable" instead of silently running unscoped tools.
		return nil, fmt.Errorf("read MCP chat scope: %w", err)
	}
	return scopes, nil
}

// decide applies one tool's allowlist to the run's conversation.
func (d *Decorator) decide(
	ctx context.Context, scopes map[string]types.MCPChatScope, serviceID, toolName string,
) (bool, error) {
	row, scoped := scopes[toolName]
	if !scoped {
		return true, nil
	}
	identity, fromIM := types.MCPChatIdentityFromContext(ctx)
	if !fromIM {
		return true, nil
	}
	spec, err := ParseSpec(row.Spec)
	if err != nil {
		// A hand-edited or half-written row must not open the tool: an
		// unreadable allowlist is treated as an empty one.
		logger.Warnf(ctx, "[MCP-SCOPE] unreadable spec, denying tool=%s service=%s err=%v",
			toolName, serviceID, err)
		return false, nil
	}
	if Allows(spec, identity) {
		return true, nil
	}
	logger.Infof(ctx, "[MCP-SCOPE] denied tool=%s service=%s platform=%s chat_type=%s chat=%s",
		toolName, serviceID, identity.Platform, identity.ChatType, identity.ChatID)
	return false, nil
}

// ParseSpec decodes a stored allowlist. Empty content is a valid, empty
// allowlist (deny in every IM conversation), not an error.
func ParseSpec(raw string) (types.MCPChatScopeSpec, error) {
	var spec types.MCPChatScopeSpec
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return spec, nil
	}
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return types.MCPChatScopeSpec{}, err
	}
	return spec, nil
}

// Allows reports whether any allowlist entry admits the conversation.
func Allows(spec types.MCPChatScopeSpec, identity types.MCPChatIdentity) bool {
	for _, entry := range spec.Allow {
		if entryMatches(entry, identity) {
			return true
		}
	}
	return false
}

func entryMatches(entry types.MCPChatScopeEntry, identity types.MCPChatIdentity) bool {
	if platform := strings.TrimSpace(entry.Platform); platform != "" &&
		!strings.EqualFold(platform, identity.Platform) {
		return false
	}
	entryType := strings.ToLower(strings.TrimSpace(entry.ChatType))
	if entryType != "" && entryType != strings.ToLower(identity.ChatType) {
		return false
	}

	chatID := strings.TrimSpace(entry.ChatID)
	switch {
	case chatID == "*":
		return true
	case chatID == "":
		// Only a direct chat is addressable without an ID. A group entry with no
		// ID matches nothing: validation rejects it, and this keeps a hand-edited
		// row from quietly opening every group in the tenant.
		return entryType == "direct"
	default:
		return chatID == strings.TrimSpace(identity.ChatID)
	}
}

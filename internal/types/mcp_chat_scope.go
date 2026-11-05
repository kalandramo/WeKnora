package types

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// MCPChatScope restricts one MCP tool to a set of IM conversations. It is a
// separate table rather than a column on mcp_tool_approvals: this mechanism is
// a customization layered on top of the upstream policy chain, so keeping it in
// its own table means an upstream migration of the approvals table never
// touches it, and removing this feature is a drop table rather than a column
// rewind.
//
// A row exists only for tools an operator explicitly scoped. Tools without a
// row keep the existing chain untouched — the mechanism can only subtract
// exposure, never grant it.
type MCPChatScope struct {
	ID        string `json:"id"         gorm:"type:varchar(36);primaryKey"`
	TenantID  uint64 `json:"tenant_id"  gorm:"not null;uniqueIndex:idx_mcp_chat_scopes_tenant_svc_tool"`
	ServiceID string `json:"service_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_mcp_chat_scopes_tenant_svc_tool;index"`
	ToolName  string `json:"tool_name"  gorm:"type:varchar(512);not null;uniqueIndex:idx_mcp_chat_scopes_tenant_svc_tool"`
	// Spec is the JSON encoding of MCPChatScopeSpec. It is stored as text rather
	// than JSONB so the same schema works on PostgreSQL and SQLite.
	Spec      string    `json:"spec"       gorm:"type:text;not null;default:''"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// BeforeCreate sets ID for MCPChatScope.
func (m *MCPChatScope) BeforeCreate(tx *gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	return nil
}

// TableName pins the table name so a rename of the Go type cannot silently
// orphan the migration.
func (m *MCPChatScope) TableName() string {
	return "mcp_chat_scopes"
}

// MCPChatScopeSpec is the stored allowlist of one tool.
type MCPChatScopeSpec struct {
	// Allow lists the conversations where the tool may be used. An empty list
	// means "unavailable in every IM conversation", which is the emergency
	// shutdown switch: the row stays, the tool goes dark.
	Allow []MCPChatScopeEntry `json:"allow"`
}

// MCPChatScopeEntry names one allowed conversation.
type MCPChatScopeEntry struct {
	// Platform is the im.Platform value (feishu, lark, wecom, dingtalk, …).
	// Required: the API rejects an entry without it, and matching is exact, so a
	// blank value matches nothing rather than everything.
	Platform string `json:"platform,omitempty"`
	// ChatType is "group" or "direct" (im.ChatType). Required, matched exactly.
	ChatType string `json:"chat_type,omitempty"`
	// ChatID is the group/channel ID. Direct messages have no group ID, so an
	// entry that allows a direct chat leaves this empty and relies on ChatType.
	// "*" matches any conversation of the given ChatType.
	ChatID string `json:"chat_id,omitempty"`
	// Label is free-form operator documentation. It never participates in
	// matching; it exists so a reviewer can tell two oc_ IDs apart.
	Label string `json:"label,omitempty"`
}

// MCPChatIdentity is the IM conversation a run belongs to. It is stamped into
// the context by the IM service and read by the tool layer; runs that did not
// start from an IM message (Web, API, scheduled work) carry no identity and are
// therefore untouched by chat scoping.
type MCPChatIdentity struct {
	Platform string
	ChatType string
	ChatID   string
}

type mcpChatIdentityKey struct{}

// WithMCPChatIdentity stamps the originating IM conversation on ctx.
func WithMCPChatIdentity(ctx context.Context, id MCPChatIdentity) context.Context {
	if ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, mcpChatIdentityKey{}, id)
}

// MCPChatIdentityFromContext reports the IM conversation a run started from.
// The second result is false for Web/API/scheduled runs, which must keep the
// upstream policy chain unchanged.
func MCPChatIdentityFromContext(ctx context.Context) (MCPChatIdentity, bool) {
	if ctx == nil {
		return MCPChatIdentity{}, false
	}
	id, ok := ctx.Value(mcpChatIdentityKey{}).(MCPChatIdentity)
	if !ok {
		return MCPChatIdentity{}, false
	}
	// A run stamped with nothing usable is treated as non-IM: requiring a
	// platform here would turn a bookkeeping gap into a silent tool outage.
	if id.Platform == "" {
		return MCPChatIdentity{}, false
	}
	return id, true
}

package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MCPChatScopeRepository stores IM-conversation allowlists for MCP tools.
//
// The table is intentionally small and read-mostly: the tool layer queries it on
// every MCP tool enumeration and call, the operator writes it rarely.
type MCPChatScopeRepository struct {
	db *gorm.DB
}

// NewMCPChatScopeRepository constructs the repository.
func NewMCPChatScopeRepository(db *gorm.DB) *MCPChatScopeRepository {
	return &MCPChatScopeRepository{db: db}
}

// Upsert writes the allowlist of one tool, replacing any previous spec. It
// updates only the spec and updated_at so a concurrent write of the same tool
// cannot resurrect stale field values.
func (r *MCPChatScopeRepository) Upsert(
	ctx context.Context, tenantID uint64, serviceID, toolName, spec string,
) error {
	if serviceID == "" || toolName == "" {
		return errors.New("service_id and tool_name are required")
	}
	now := time.Now()
	row := map[string]interface{}{
		"id":         uuid.New().String(),
		"tenant_id":  tenantID,
		"service_id": serviceID,
		"tool_name":  toolName,
		"spec":       spec,
		"created_at": now,
		"updated_at": now,
	}
	return r.db.WithContext(ctx).Model(&types.MCPChatScope{}).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "tenant_id"},
			{Name: "service_id"},
			{Name: "tool_name"},
		},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"spec":       spec,
			"updated_at": now,
		}),
	}).Create(row).Error
}

// Delete removes the allowlist of one tool, returning the tool to the upstream
// policy chain. Deleting a row that does not exist is not an error: the caller
// asked for "unrestricted", which is the state they get either way.
func (r *MCPChatScopeRepository) Delete(ctx context.Context, tenantID uint64, serviceID, toolName string) error {
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND service_id = ? AND tool_name = ?", tenantID, serviceID, toolName).
		Delete(&types.MCPChatScope{}).Error
}

// ListByService returns every scoped tool of one service, keyed by tool name.
// This is the read used by the policy decorator: one query per service covers
// both the batch enumeration and the per-call check.
func (r *MCPChatScopeRepository) ListByService(
	ctx context.Context, tenantID uint64, serviceID string,
) (map[string]types.MCPChatScope, error) {
	if tenantID == 0 || serviceID == "" {
		return nil, nil
	}
	var rows []types.MCPChatScope
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND service_id = ?", tenantID, serviceID).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	scopes := make(map[string]types.MCPChatScope, len(rows))
	for _, row := range rows {
		scopes[row.ToolName] = row
	}
	return scopes, nil
}

// List returns every scoped tool of a tenant, for the admin listing endpoint.
func (r *MCPChatScopeRepository) List(ctx context.Context, tenantID uint64) ([]types.MCPChatScope, error) {
	var rows []types.MCPChatScope
	err := r.db.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("service_id, tool_name").
		Find(&rows).Error
	return rows, err
}

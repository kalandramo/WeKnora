package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/mcpscope"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// MCPChatScopeHandler exposes the IM-conversation allowlists of MCP tools.
//
// There is no UI for this yet by design: the operator entry point is this API,
// which keeps the feature out of the upstream MCP settings screens and out of
// the frontend release cycle. A caller configures a tool once and the change
// takes effect on the next tool enumeration — no restart, no redeploy.
type MCPChatScopeHandler struct {
	scopes     *repository.MCPChatScopeRepository
	mcpService interfaces.MCPServiceService
}

// NewMCPChatScopeHandler constructs the handler.
func NewMCPChatScopeHandler(
	scopes *repository.MCPChatScopeRepository, mcpService interfaces.MCPServiceService,
) *MCPChatScopeHandler {
	return &MCPChatScopeHandler{scopes: scopes, mcpService: mcpService}
}

// mcpChatScopeView is the API representation of one scoped tool. The stored
// spec is decoded so callers never have to parse a JSON string inside JSON.
type mcpChatScopeView struct {
	ServiceID string                    `json:"service_id"`
	ToolName  string                    `json:"tool_name"`
	Allow     []types.MCPChatScopeEntry `json:"allow"`
	UpdatedAt time.Time                 `json:"updated_at"`
}

type mcpChatScopeUpsertRequest struct {
	// ServiceID takes precedence; Service (the display name, unique per tenant)
	// is a convenience for operators who should not have to look up a UUID.
	ServiceID string                    `json:"service_id"`
	Service   string                    `json:"service"`
	ToolName  string                    `json:"tool_name"`
	Allow     []types.MCPChatScopeEntry `json:"allow"`
}

// List returns every scoped tool of the workspace, or of one service when
// service_id is supplied.
//
// List godoc
// @Summary      查询 MCP 工具的群聊白名单
// @Tags         MCP服务
// @Produce      json
// @Param        service_id  query     string  false  "只看某个 MCP 服务"
// @Success      200         {object}  map[string]interface{}
// @Router       /mcp-chat-scopes [get]
func (h *MCPChatScopeHandler) List(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.Error(errors.NewBadRequestError("Workspace ID cannot be empty"))
		return
	}

	serviceID := strings.TrimSpace(c.Query("service_id"))
	views := make([]mcpChatScopeView, 0)
	if serviceID != "" {
		rows, err := h.scopes.ListByService(ctx, tenantID, serviceID)
		if err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{"service_id": secutils.SanitizeForLog(serviceID)})
			c.Error(errors.NewInternalServerError("Failed to list MCP chat scopes"))
			return
		}
		for _, row := range rows {
			views = append(views, toScopeView(ctx, row))
		}
	} else {
		rows, err := h.scopes.List(ctx, tenantID)
		if err != nil {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to list MCP chat scopes"))
			return
		}
		for _, row := range rows {
			views = append(views, toScopeView(ctx, row))
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": views})
}

// Upsert creates or replaces the allowlist of one tool.
//
// Upsert godoc
// @Summary      设置 MCP 工具的群聊白名单
// @Description  allow 为空表示该工具在所有 IM 会话中不可用；未配置过的工具保持原有策略链
// @Tags         MCP服务
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Router       /mcp-chat-scopes [put]
func (h *MCPChatScopeHandler) Upsert(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.Error(errors.NewBadRequestError("Workspace ID cannot be empty"))
		return
	}

	var req mcpChatScopeUpsertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError("Invalid request body: " + err.Error()))
		return
	}

	serviceID, ok := h.resolveService(c, tenantID, strings.TrimSpace(req.ServiceID), strings.TrimSpace(req.Service))
	if !ok {
		return
	}
	toolName := strings.TrimSpace(req.ToolName)
	if toolName == "" {
		c.Error(errors.NewBadRequestError("tool_name is required"))
		return
	}

	allow, err := normalizeScopeEntries(req.Allow)
	if err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	spec, err := marshalSpec(allow)
	if err != nil {
		c.Error(errors.NewInternalServerError("Failed to encode MCP chat scope"))
		return
	}
	if err := h.scopes.Upsert(ctx, tenantID, serviceID, toolName, spec); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"service_id": secutils.SanitizeForLog(serviceID),
			"tool_name":  secutils.SanitizeForLog(toolName),
		})
		c.Error(errors.NewInternalServerError("Failed to save MCP chat scope"))
		return
	}

	logger.Infof(ctx, "[MCP-SCOPE] saved service=%s tool=%s entries=%d",
		secutils.SanitizeForLog(serviceID), secutils.SanitizeForLog(toolName), len(allow))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": mcpChatScopeView{
			ServiceID: serviceID,
			ToolName:  toolName,
			Allow:     allow,
			UpdatedAt: time.Now(),
		},
	})
}

// Delete removes the allowlist, returning the tool to the upstream policy
// chain.
//
// Delete godoc
// @Summary      删除 MCP 工具的群聊白名单
// @Tags         MCP服务
// @Produce      json
// @Param        service_id  query     string  true  "MCP 服务 ID"
// @Param        tool_name   query     string  true  "工具名"
// @Success      200         {object}  map[string]interface{}
// @Router       /mcp-chat-scopes [delete]
func (h *MCPChatScopeHandler) Delete(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.Error(errors.NewBadRequestError("Workspace ID cannot be empty"))
		return
	}

	serviceID := strings.TrimSpace(c.Query("service_id"))
	toolName := strings.TrimSpace(c.Query("tool_name"))
	if serviceID == "" || toolName == "" {
		c.Error(errors.NewBadRequestError("service_id and tool_name are required"))
		return
	}
	if err := h.scopes.Delete(ctx, tenantID, serviceID, toolName); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"service_id": secutils.SanitizeForLog(serviceID),
			"tool_name":  secutils.SanitizeForLog(toolName),
		})
		c.Error(errors.NewInternalServerError("Failed to delete MCP chat scope"))
		return
	}
	logger.Infof(ctx, "[MCP-SCOPE] removed service=%s tool=%s",
		secutils.SanitizeForLog(serviceID), secutils.SanitizeForLog(toolName))
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// resolveService accepts either an ID or a display name and returns the ID. The
// name path lists the tenant's services, which is cheap and keeps the operator
// from having to query a UUID before locking a tool.
func (h *MCPChatScopeHandler) resolveService(
	c *gin.Context, tenantID uint64, serviceID, serviceName string,
) (string, bool) {
	ctx := c.Request.Context()
	if serviceID != "" {
		if _, err := h.mcpService.GetMCPServiceByID(ctx, tenantID, serviceID); err != nil {
			c.Error(errors.NewBadRequestError("MCP service not found: " + serviceID))
			return "", false
		}
		return serviceID, true
	}
	if serviceName == "" {
		c.Error(errors.NewBadRequestError("service_id or service is required"))
		return "", false
	}
	services, err := h.mcpService.ListMCPServices(ctx, tenantID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError("Failed to list MCP services"))
		return "", false
	}
	for _, svc := range services {
		if svc != nil && strings.EqualFold(strings.TrimSpace(svc.Name), serviceName) {
			return svc.ID, true
		}
	}
	c.Error(errors.NewBadRequestError("MCP service not found: " + serviceName))
	return "", false
}

func toScopeView(ctx context.Context, row types.MCPChatScope) mcpChatScopeView {
	spec, err := mcpscope.ParseSpec(row.Spec)
	if err != nil {
		// Surface the broken row instead of hiding it: an operator who sees an
		// empty list would not know the tool is currently denied everywhere.
		logger.Warnf(ctx, "[MCP-SCOPE] unreadable spec service=%s tool=%s err=%v",
			row.ServiceID, row.ToolName, err)
	}
	allow := spec.Allow
	if allow == nil {
		allow = []types.MCPChatScopeEntry{}
	}
	return mcpChatScopeView{
		ServiceID: row.ServiceID,
		ToolName:  row.ToolName,
		Allow:     allow,
		UpdatedAt: row.UpdatedAt,
	}
}

// normalizeScopeEntries validates and normalizes the submitted allowlist.
//
// The rules exist so a typo cannot widen exposure: an entry with no chat ID is
// only meaningful for a direct chat, and a group entry without one matches
// nothing (see mcpscope.Allows). Rejecting it here beats leaving a rule that
// silently does not apply.
func normalizeScopeEntries(entries []types.MCPChatScopeEntry) ([]types.MCPChatScopeEntry, error) {
	normalized := make([]types.MCPChatScopeEntry, 0, len(entries))
	for _, entry := range entries {
		entry.Platform = strings.ToLower(strings.TrimSpace(entry.Platform))
		entry.ChatType = strings.ToLower(strings.TrimSpace(entry.ChatType))
		entry.ChatID = strings.TrimSpace(entry.ChatID)
		entry.Label = strings.TrimSpace(entry.Label)

		switch entry.ChatType {
		case "group", "direct", "":
		default:
			return nil, errors.NewBadRequestError("chat_type must be group or direct")
		}
		if entry.ChatType == "" {
			return nil, errors.NewBadRequestError("chat_type is required on every entry")
		}
		if entry.ChatType == "group" && entry.ChatID == "" {
			return nil, errors.NewBadRequestError("chat_id is required for group entries (use * for any group)")
		}
		if entry.ChatType == "direct" && entry.ChatID != "" && entry.ChatID != "*" {
			// Direct messages carry no group ID; a value here means the operator
			// pasted a group ID into a private-chat rule.
			return nil, errors.NewBadRequestError("chat_id must be empty or * for direct entries")
		}
		normalized = append(normalized, entry)
	}
	return normalized, nil
}

func marshalSpec(allow []types.MCPChatScopeEntry) (string, error) {
	spec := types.MCPChatScopeSpec{Allow: allow}
	if spec.Allow == nil {
		spec.Allow = []types.MCPChatScopeEntry{}
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

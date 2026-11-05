package router

import (
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/gin-gonic/gin"
)

// RegisterMCPChatScopeRoutes mounts the IM-conversation allowlist API.
//
// It is a sibling of the MCP service routes rather than a subresource, so the
// upstream /mcp-services group definition stays untouched: this feature is a
// customization and should not add merge surface to a file that changes often.
func RegisterMCPChatScopeRoutes(
	r *gin.RouterGroup,
	scopeHandler *handler.MCPChatScopeHandler,
	g *rbacGuards,
) {
	if scopeHandler == nil {
		return
	}
	scopes := g.apiKeyGroup(r.Group("/mcp-chat-scopes"), apiKeyManageMCPServices(apiKeyFullAccess()))
	{
		// Reading the allowlist is part of running the workspace, but it is
		// admin-only anyway: the rows name internal groups.
		scopes.GET("", g.Admin(), scopeHandler.List)
		scopes.PUT("", g.Admin(), scopeHandler.Upsert)
		scopes.DELETE("", g.Admin(), scopeHandler.Delete)
	}
}

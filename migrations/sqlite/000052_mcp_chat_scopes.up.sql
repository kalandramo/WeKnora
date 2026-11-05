-- IM-conversation allowlists for MCP tools (see versioned/000135).
-- A row exists only for tools an operator explicitly scoped; tools without a row
-- keep the upstream policy chain untouched.
CREATE TABLE IF NOT EXISTS mcp_chat_scopes (
    id         VARCHAR(36)  PRIMARY KEY,
    tenant_id  INTEGER      NOT NULL,
    service_id VARCHAR(36)  NOT NULL,
    tool_name  VARCHAR(512) NOT NULL,
    spec       TEXT         NOT NULL DEFAULT '',
    created_at DATETIME,
    updated_at DATETIME
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_chat_scopes_tenant_svc_tool
    ON mcp_chat_scopes (tenant_id, service_id, tool_name);

CREATE INDEX IF NOT EXISTS idx_mcp_chat_scopes_service_id
    ON mcp_chat_scopes (service_id);

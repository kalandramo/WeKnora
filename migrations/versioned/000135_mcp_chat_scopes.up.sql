DO $$ BEGIN RAISE NOTICE '[Migration 000135] Creating mcp_chat_scopes...'; END $$;

-- IM-conversation allowlists for MCP tools.
--
-- Deliberately a separate table rather than a column on mcp_tool_approvals:
-- this is a customization layered on the upstream policy chain, so an upstream
-- migration of the approvals table must never collide with it, and removing the
-- feature is a drop table instead of a column rewind.
--
-- A row exists only for tools an operator explicitly scoped. Tools without a row
-- keep the existing chain untouched: the mechanism can only subtract exposure.
CREATE TABLE IF NOT EXISTS mcp_chat_scopes (
    id         VARCHAR(36)  PRIMARY KEY,
    tenant_id  BIGINT       NOT NULL,
    service_id VARCHAR(36)  NOT NULL,
    tool_name  VARCHAR(512) NOT NULL,
    -- JSON encoding of {allow: [{platform, chat_type, chat_id, label}]}. Stored as
    -- text rather than jsonb so the same schema ships to PostgreSQL and SQLite.
    spec       TEXT         NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ
);

-- Upserts target (tenant, service, tool); the unique index is what makes the
-- ON CONFLICT clause work on both PostgreSQL and SQLite.
CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_chat_scopes_tenant_svc_tool
    ON mcp_chat_scopes (tenant_id, service_id, tool_name);

-- Every tool call reads the scoped tools of one service.
CREATE INDEX IF NOT EXISTS idx_mcp_chat_scopes_service_id
    ON mcp_chat_scopes (service_id);

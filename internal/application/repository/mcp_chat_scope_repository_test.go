package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newMCPChatScopeTestRepo(t *testing.T) *MCPChatScopeRepository {
	t.Helper()
	dsn := "file:" + uuid.NewString() + "?mode=memory&cache=shared&_busy_timeout=5000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&types.MCPChatScope{}))
	return NewMCPChatScopeRepository(db)
}

// Upsert must replace the spec of an existing (tenant, service, tool) instead of
// adding a second row: the unique index is what the tool layer's map read
// assumes, and two rows would make the effective allowlist depend on read order.
func TestMCPChatScopeUpsertReplacesSpec(t *testing.T) {
	repo := newMCPChatScopeTestRepo(t)
	ctx := context.Background()

	require.NoError(t, repo.Upsert(ctx, 1, "svc", "tool", `{"allow":[]}`))
	require.NoError(t, repo.Upsert(ctx, 1, "svc", "tool", `{"allow":[{"chat_type":"group","chat_id":"oc_ops"}]}`))

	var rows []types.MCPChatScope
	require.NoError(t, repo.db.Where("tenant_id = ?", 1).Find(&rows).Error)
	require.Len(t, rows, 1)
	require.JSONEq(t, `{"allow":[{"chat_type":"group","chat_id":"oc_ops"}]}`, rows[0].Spec)

	scopes, err := repo.ListByService(ctx, 1, "svc")
	require.NoError(t, err)
	require.Len(t, scopes, 1)
	require.Contains(t, scopes["tool"].Spec, "oc_ops")
}

func TestMCPChatScopeDelete(t *testing.T) {
	repo := newMCPChatScopeTestRepo(t)
	ctx := context.Background()

	require.NoError(t, repo.Upsert(ctx, 1, "svc", "tool", `{"allow":[]}`))
	require.NoError(t, repo.Delete(ctx, 1, "svc", "tool"))

	scopes, err := repo.ListByService(ctx, 1, "svc")
	require.NoError(t, err)
	require.Empty(t, scopes)

	// Deleting again is a no-op: the caller asked for "unrestricted" and that is
	// the state they get.
	require.NoError(t, repo.Delete(ctx, 1, "svc", "tool"))
}

// A scope written in one workspace must never be visible to another: the tool
// layer passes the run's tenant and trusts the repository to bound the read.
func TestMCPChatScopeTenantIsolation(t *testing.T) {
	repo := newMCPChatScopeTestRepo(t)
	ctx := context.Background()

	require.NoError(t, repo.Upsert(ctx, 1, "svc", "tool", `{"allow":[]}`))

	other, err := repo.ListByService(ctx, 2, "svc")
	require.NoError(t, err)
	require.Empty(t, other)

	list, err := repo.List(ctx, 2)
	require.NoError(t, err)
	require.Empty(t, list)

	mine, err := repo.List(ctx, 1)
	require.NoError(t, err)
	require.Len(t, mine, 1)
}

func TestMCPChatScopeListByServiceScopesToOneService(t *testing.T) {
	repo := newMCPChatScopeTestRepo(t)
	ctx := context.Background()

	require.NoError(t, repo.Upsert(ctx, 1, "svc-a", "tool-a", `{"allow":[]}`))
	require.NoError(t, repo.Upsert(ctx, 1, "svc-b", "tool-b", `{"allow":[]}`))

	a, err := repo.ListByService(ctx, 1, "svc-a")
	require.NoError(t, err)
	require.Len(t, a, 1)
	require.Contains(t, a, "tool-a")
	require.NotContains(t, a, "tool-b")
}

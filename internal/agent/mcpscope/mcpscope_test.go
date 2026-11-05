package mcpscope

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/types"
)

type fakeStore struct {
	scopes map[string]types.MCPChatScope
	err    error
	calls  int
}

func (f *fakeStore) ListByService(
	_ context.Context, _ uint64, _ string,
) (map[string]types.MCPChatScope, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.scopes, nil
}

type fakeChecker struct {
	enabled  bool
	err      error
	required bool
}

func (f *fakeChecker) IsRequired(
	_ context.Context, _ uint64, _, _ string,
) (bool, error) {
	return f.required, f.err
}

func (f *fakeChecker) IsEnabled(
	_ context.Context, _ uint64, _, _ string,
) (bool, error) {
	return f.enabled, f.err
}

func scopedRow(t *testing.T, tool string, entries ...types.MCPChatScopeEntry) types.MCPChatScope {
	t.Helper()
	spec, err := marshalTestSpec(entries)
	if err != nil {
		t.Fatalf("encode spec: %v", err)
	}
	return types.MCPChatScope{ToolName: tool, ServiceID: "svc", Spec: spec}
}

func marshalTestSpec(entries []types.MCPChatScopeEntry) (string, error) {
	raw, err := json.Marshal(types.MCPChatScopeSpec{Allow: entries})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func groupIdentity(chatID string) types.MCPChatIdentity {
	return types.MCPChatIdentity{Platform: "feishu", ChatType: "group", ChatID: chatID}
}

func ctxWith(id types.MCPChatIdentity) context.Context {
	return types.WithMCPChatIdentity(context.Background(), id)
}

// A tool nobody scoped must behave exactly as before, with or without an IM
// conversation: the mechanism can only subtract exposure.
func TestIsEnabledUnscopedToolKeepsUpstreamDecision(t *testing.T) {
	d := NewDecorator(&fakeChecker{enabled: true}, &fakeStore{})
	for _, ctx := range []context.Context{
		context.Background(),
		ctxWith(groupIdentity("oc_ops")),
	} {
		ok, err := d.IsEnabled(ctx, 1, "svc", "cmdb_asset_update")
		if err != nil || !ok {
			t.Fatalf("unscoped tool should stay enabled, got ok=%v err=%v", ok, err)
		}
	}
}

// A run that did not start from an IM message must not consult the scope store
// at all: that keeps its behaviour byte-for-byte upstream, and keeps the scope
// table out of its failure domain. The store here would fail if it were read.
func TestNonIMRunSkipsScopeStore(t *testing.T) {
	store := &fakeStore{err: errors.New("db down")}
	d := NewDecorator(&fakeChecker{enabled: true}, store)

	ok, err := d.IsEnabled(context.Background(), 1, "svc", "write_tool")
	if err != nil || !ok {
		t.Fatalf("non-IM run must keep the upstream answer, got ok=%v err=%v", ok, err)
	}
	if store.calls != 0 {
		t.Fatalf("non-IM run queried the scope store %d times, want 0", store.calls)
	}

	batch, err := d.EnabledTools(context.Background(), 1, "svc", []string{"read_tool", "write_tool"})
	if err != nil {
		t.Fatalf("non-IM enumeration must not fail on the scope store: %v", err)
	}
	if !batch["read_tool"] || !batch["write_tool"] {
		t.Errorf("non-IM tools must all keep the upstream answer, got %v", batch)
	}
	if store.calls != 0 {
		t.Fatalf("non-IM enumeration queried the scope store %d times, want 0", store.calls)
	}
}

func TestIsEnabledEnforcesAllowlist(t *testing.T) {
	store := &fakeStore{scopes: map[string]types.MCPChatScope{
		"cmdb_asset_update": scopedRow(t, "cmdb_asset_update",
			types.MCPChatScopeEntry{Platform: "feishu", ChatType: "group", ChatID: "oc_ops"}),
	}}

	cases := []struct {
		name string
		ctx  context.Context
		want bool
	}{
		{
			name: "allowlisted group",
			ctx:  ctxWith(groupIdentity("oc_ops")),
			want: true,
		},
		{
			name: "other group",
			ctx:  ctxWith(groupIdentity("oc_other")),
			want: false,
		},
		{
			// The strict scope applies to every IM conversation, so a user who
			// moves the same question into a private chat does not bypass it.
			name: "direct message is not in the allowlist",
			ctx:  ctxWith(types.MCPChatIdentity{Platform: "feishu", ChatType: "direct"}),
			want: false,
		},
		{
			name: "other platform",
			ctx:  ctxWith(types.MCPChatIdentity{Platform: "wecom", ChatType: "group", ChatID: "oc_ops"}),
			want: false,
		},
		{
			// Web, API and scheduled runs carry no IM identity: they keep the
			// upstream chain (the workspace already gates them by permission).
			name: "non-IM run inherits the upstream chain",
			ctx:  context.Background(),
			want: true,
		},
	}

	d := NewDecorator(&fakeChecker{enabled: true}, store)
	for _, c := range cases {
		ok, err := d.IsEnabled(c.ctx, 1, "svc", "cmdb_asset_update")
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", c.name, err)
		}
		if ok != c.want {
			t.Errorf("%s: IsEnabled = %v, want %v", c.name, ok, c.want)
		}
	}
}

func TestIsEnabledDirectEntryAllowsPrivateChat(t *testing.T) {
	store := &fakeStore{scopes: map[string]types.MCPChatScope{
		"tool": scopedRow(t, "tool",
			types.MCPChatScopeEntry{Platform: "feishu", ChatType: "direct"}),
	}}
	d := NewDecorator(&fakeChecker{enabled: true}, store)

	ok, err := d.IsEnabled(ctxWith(types.MCPChatIdentity{Platform: "feishu", ChatType: "direct"}), 1, "svc", "tool")
	if err != nil || !ok {
		t.Fatalf("explicit direct entry should allow a private chat, got ok=%v err=%v", ok, err)
	}
	ok, err = d.IsEnabled(ctxWith(groupIdentity("oc_ops")), 1, "svc", "tool")
	if err != nil || ok {
		t.Fatalf("direct-only entry must not allow a group, got ok=%v err=%v", ok, err)
	}
}

func TestIsEnabledWildcards(t *testing.T) {
	store := &fakeStore{scopes: map[string]types.MCPChatScope{
		"any_group": scopedRow(t, "any_group",
			types.MCPChatScopeEntry{Platform: "feishu", ChatType: "group", ChatID: "*"}),
		"any_platform": scopedRow(t, "any_platform",
			types.MCPChatScopeEntry{ChatType: "group", ChatID: "oc_ops"}),
	}}
	d := NewDecorator(&fakeChecker{enabled: true}, store)

	for _, chat := range []string{"oc_a", "oc_b"} {
		ok, err := d.IsEnabled(ctxWith(groupIdentity(chat)), 1, "svc", "any_group")
		if err != nil || !ok {
			t.Fatalf("chat_id * should allow %s, got ok=%v err=%v", chat, ok, err)
		}
	}
	ok, err := d.IsEnabled(
		ctxWith(types.MCPChatIdentity{Platform: "wecom", ChatType: "group", ChatID: "oc_ops"}),
		1, "svc", "any_platform")
	if err != nil || !ok {
		t.Fatalf("an entry without platform should match any platform, got ok=%v err=%v", ok, err)
	}
}

// An empty allowlist is the emergency switch: the row stays, the tool goes dark
// in every IM conversation.
func TestIsEnabledEmptyAllowlistDeniesEveryIMChat(t *testing.T) {
	store := &fakeStore{scopes: map[string]types.MCPChatScope{
		"tool": scopedRow(t, "tool"),
	}}
	d := NewDecorator(&fakeChecker{enabled: true}, store)

	for _, id := range []types.MCPChatIdentity{
		groupIdentity("oc_ops"),
		{Platform: "feishu", ChatType: "direct"},
	} {
		ok, err := d.IsEnabled(ctxWith(id), 1, "svc", "tool")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ok {
			t.Errorf("%s: empty allowlist must deny, got allowed", id.ChatType)
		}
	}
	if ok, _ := d.IsEnabled(context.Background(), 1, "svc", "tool"); !ok {
		t.Error("non-IM run must still inherit the upstream chain")
	}
}

// A hand-edited or half-written spec must not open the tool.
func TestIsEnabledUnreadableSpecDenies(t *testing.T) {
	store := &fakeStore{scopes: map[string]types.MCPChatScope{
		"tool": {ToolName: "tool", Spec: "{not json"},
	}}
	d := NewDecorator(&fakeChecker{enabled: true}, store)
	ok, err := d.IsEnabled(ctxWith(groupIdentity("oc_ops")), 1, "svc", "tool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("an unreadable spec must deny, not allow")
	}
}

// Scoping is a subtraction: it can never re-enable a tool the workspace turned
// off.
func TestIsEnabledRespectsUpstreamDisable(t *testing.T) {
	store := &fakeStore{scopes: map[string]types.MCPChatScope{
		"tool": scopedRow(t, "tool",
			types.MCPChatScopeEntry{Platform: "feishu", ChatType: "group", ChatID: "oc_ops"}),
	}}
	d := NewDecorator(&fakeChecker{enabled: false}, store)
	ok, err := d.IsEnabled(ctxWith(groupIdentity("oc_ops")), 1, "svc", "tool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("an allowlisted chat must not override a workspace disable")
	}
}

func TestIsEnabledPropagatesStoreFailure(t *testing.T) {
	store := &fakeStore{err: errors.New("db down")}
	d := NewDecorator(&fakeChecker{enabled: true}, store)
	if _, err := d.IsEnabled(ctxWith(groupIdentity("oc_ops")), 1, "svc", "tool"); err == nil {
		t.Fatal("a store failure must surface, so the caller can fail closed")
	}
}

// Enumeration must read the allowlist once per service, not once per tool.
func TestEnabledToolsBatchesOneScopeRead(t *testing.T) {
	store := &fakeStore{scopes: map[string]types.MCPChatScope{
		"write_tool": scopedRow(t, "write_tool",
			types.MCPChatScopeEntry{Platform: "feishu", ChatType: "group", ChatID: "oc_ops"}),
	}}
	d := NewDecorator(&fakeChecker{enabled: true}, store)

	names := []string{"read_tool", "write_tool", "other_tool"}
	got, err := d.EnabledTools(ctxWith(groupIdentity("oc_denied")), 1, "svc", names)
	if err != nil {
		t.Fatalf("EnabledTools: %v", err)
	}
	if store.calls != 1 {
		t.Fatalf("store queried %d times, want 1", store.calls)
	}
	if !got["read_tool"] || !got["other_tool"] {
		t.Errorf("unscoped tools must stay enabled, got %v", got)
	}
	if got["write_tool"] {
		t.Errorf("scoped tool outside its allowlist must be disabled, got %v", got)
	}

	store.calls = 0
	allowed, err := d.EnabledTools(ctxWith(groupIdentity("oc_ops")), 1, "svc", names)
	if err != nil {
		t.Fatalf("EnabledTools: %v", err)
	}
	if !allowed["write_tool"] {
		t.Errorf("scoped tool inside its allowlist must be enabled, got %v", allowed)
	}
}

func TestNilDependenciesDegradeToPassThrough(t *testing.T) {
	var d *Decorator
	if ok, err := d.IsEnabled(context.Background(), 1, "svc", "tool"); err != nil || !ok {
		t.Fatalf("nil decorator must pass through, got ok=%v err=%v", ok, err)
	}

	noStore := NewDecorator(&fakeChecker{enabled: true}, nil)
	if ok, err := noStore.IsEnabled(ctxWith(groupIdentity("oc_ops")), 1, "svc", "tool"); err != nil || !ok {
		t.Fatalf("nil store must pass through, got ok=%v err=%v", ok, err)
	}
}

func TestParseSpec(t *testing.T) {
	spec, err := ParseSpec(`{"allow":[{"platform":"feishu","chat_type":"group","chat_id":"oc_1"}]}`)
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	if len(spec.Allow) != 1 || spec.Allow[0].ChatID != "oc_1" {
		t.Fatalf("unexpected spec: %+v", spec)
	}

	empty, err := ParseSpec("  ")
	if err != nil {
		t.Fatalf("empty spec must be valid: %v", err)
	}
	if len(empty.Allow) != 0 {
		t.Fatalf("empty spec must have no entries, got %+v", empty)
	}
}

// The decorator is wired into the gate's checker, so the gate's own batch and
// single-tool entry points must reach it. This asserts the interface the gate
// type-asserts on (BulkEnabledChecker) is actually satisfied — a plain Decorator
// that only implemented IsEnabled would compile and then silently fall back to
// one scope query per tool.
func TestGateRoutesThroughDecorator(t *testing.T) {
	store := &fakeStore{scopes: map[string]types.MCPChatScope{
		"write_tool": scopedRow(t, "write_tool",
			types.MCPChatScopeEntry{Platform: "feishu", ChatType: "group", ChatID: "oc_ops"}),
	}}
	gate := approval.NewGate(nil, NewDecorator(&fakeChecker{enabled: true}, store), nil)

	ctx := ctxWith(groupIdentity("oc_denied"))
	batch, err := gate.EnabledTools(ctx, 1, "svc", []string{"read_tool", "write_tool"})
	if err != nil {
		t.Fatalf("gate.EnabledTools: %v", err)
	}
	if store.calls != 1 {
		t.Fatalf("gate batch enumeration queried the store %d times, want 1", store.calls)
	}
	if !batch["read_tool"] || batch["write_tool"] {
		t.Fatalf("unexpected batch verdict: %v", batch)
	}

	enabled, err := gate.IsEnabled(ctx, 1, "svc", "write_tool")
	if err != nil {
		t.Fatalf("gate.IsEnabled: %v", err)
	}
	if enabled {
		t.Fatal("gate.IsEnabled must inherit the chat allowlist")
	}
}

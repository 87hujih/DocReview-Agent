package identity_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"

	"agent_project/apps/server/internal/agent/identity"
)

const testIngressSecret = "0123456789abcdef0123456789abcdef"

// TestTrustedIngressAdapterReturnsBoundPrincipalAndWorkspaceScope 验证对应场景下的正常路径与失败路径。
func TestTrustedIngressAdapterReturnsBoundPrincipalAndWorkspaceScope(t *testing.T) {
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	request := signedRequest(t, now, "workspace-11111111-1111-4111-8111-111111111111")
	adapter := mustAdapter(t, now)

	scope, err := adapter.Authenticate(context.Background(), request, "11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatalf("authenticate trusted ingress: %v", err)
	}
	if !scope.Trusted || scope.TrustSource != "edge-hmac-v1" {
		t.Fatalf("expected trusted edge scope, got %#v", scope)
	}
	if scope.WorkspaceID != "11111111-1111-4111-8111-111111111111" || scope.Principal.ID != "22222222-2222-4222-8222-222222222222" {
		t.Fatalf("unexpected principal/workspace binding: %#v", scope)
	}
}

// TestTrustedIngressAdapterRejectsTamperedSignatureBeforeScopeUse 验证对应场景下的正常路径与失败路径。
func TestTrustedIngressAdapterRejectsTamperedSignatureBeforeScopeUse(t *testing.T) {
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	request := signedRequest(t, now, "workspace-11111111-1111-4111-8111-111111111111")
	request.Header.Set(identity.HeaderPrincipalID, "33333333-3333-4333-8333-333333333333")

	_, err := mustAdapter(t, now).Authenticate(context.Background(), request, "11111111-1111-4111-8111-111111111111")
	if err == nil || !strings.Contains(err.Error(), "签名") {
		t.Fatalf("应拒绝被篡改的签名，实际错误：%v", err)
	}
}

// TestTrustedIngressAdapterRejectsExpiredAttestation 验证对应场景下的正常路径与失败路径。
func TestTrustedIngressAdapterRejectsExpiredAttestation(t *testing.T) {
	issuedAt := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	request := signedRequest(t, issuedAt, "workspace-11111111-1111-4111-8111-111111111111")

	_, err := mustAdapter(t, issuedAt.Add(6*time.Minute)).Authenticate(context.Background(), request, "11111111-1111-4111-8111-111111111111")
	if err == nil || !strings.Contains(err.Error(), "已过期") {
		t.Fatalf("应拒绝已过期的签名证明，实际错误：%v", err)
	}
}

// TestTrustedIngressAdapterRejectsCrossWorkspaceClaim 验证对应场景下的正常路径与失败路径。
func TestTrustedIngressAdapterRejectsCrossWorkspaceClaim(t *testing.T) {
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	request := signedRequest(t, now, "workspace-11111111-1111-4111-8111-111111111111")

	_, err := mustAdapter(t, now).Authenticate(context.Background(), request, "44444444-4444-4444-8444-444444444444")
	if err == nil || !strings.Contains(err.Error(), "工作区") {
		t.Fatalf("应拒绝跨工作区声明，实际错误：%v", err)
	}
}

// mustAdapter 执行该函数负责的核心处理逻辑。
func mustAdapter(t *testing.T, now time.Time) *identity.TrustedIngressAdapter {
	t.Helper()
	adapter, err := identity.NewTrustedIngressAdapter(identity.TrustedIngressConfig{
		Secret: testIngressSecret, TrustSource: "edge-hmac-v1", MaxAge: 5 * time.Minute,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new trusted ingress adapter: %v", err)
	}
	return adapter
}

// signedRequest 执行该函数负责的核心处理逻辑。
func signedRequest(t *testing.T, issuedAt time.Time, workspaceHeader string) identity.Request {
	t.Helper()
	request := identity.Request{
		Method: "POST", Path: "/api/assistant/sessions/session-1/messages", RequestID: "request-1",
		Header: make(http.Header),
	}
	request.Header.Set(identity.HeaderPrincipalType, "user")
	request.Header.Set(identity.HeaderPrincipalID, "22222222-2222-4222-8222-222222222222")
	request.Header.Set(identity.HeaderOrganizationID, "55555555-5555-4555-8555-555555555555")
	request.Header.Set(identity.HeaderWorkspaceID, strings.TrimPrefix(workspaceHeader, "workspace-"))
	request.Header.Set(identity.HeaderIssuedAt, issuedAt.Format(time.RFC3339Nano))
	request.Header.Set(identity.HeaderRoles, "editor")
	canonical := strings.Join([]string{
		"v1", request.RequestID, request.Method, request.Path, "user",
		"22222222-2222-4222-8222-222222222222",
		"55555555-5555-4555-8555-555555555555",
		"11111111-1111-4111-8111-111111111111",
		issuedAt.Format(time.RFC3339Nano), "editor",
	}, "\n")
	mac := hmac.New(sha256.New, []byte(testIngressSecret))
	_, _ = mac.Write([]byte(canonical))
	request.Header.Set(identity.HeaderSignature, hex.EncodeToString(mac.Sum(nil)))
	return request
}

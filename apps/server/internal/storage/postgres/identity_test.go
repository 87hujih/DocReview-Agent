package postgres

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// TestMembershipRoleValidation 验证对应场景下的正常路径与失败路径。
func TestMembershipRoleValidation(t *testing.T) {
	for _, role := range []MembershipRole{
		MembershipRoleOwner,
		MembershipRoleAdmin,
		MembershipRoleEditor,
		MembershipRoleViewer,
	} {
		if !role.Valid() {
			t.Fatalf("expected role %q to be valid", role)
		}
	}
	if MembershipRole("superuser").Valid() {
		t.Fatal("expected unknown role to be invalid")
	}
}

// TestCreateUserRejectsPartialExternalIdentityBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestCreateUserRejectsPartialExternalIdentityBeforeDatabaseAccess(t *testing.T) {
	issuer := "https://issuer.example"
	repo := NewIdentityRepo(nil)

	_, err := repo.CreateUser(context.Background(), CreateUserParams{ExternalIssuer: &issuer})
	if err == nil || !strings.Contains(err.Error(), "supplied together") {
		t.Fatalf("expected paired external identity validation, got %v", err)
	}
}

// TestRecordPrincipalAuditEventRejectsInvalidJSONBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestRecordPrincipalAuditEventRejectsInvalidJSONBeforeDatabaseAccess(t *testing.T) {
	repo := NewIdentityRepo(nil)

	_, err := repo.RecordPrincipalAuditEvent(context.Background(), PrincipalAuditEventParams{Metadata: json.RawMessage(`{`)})
	if err == nil || !strings.Contains(err.Error(), "valid JSON") {
		t.Fatalf("expected invalid metadata error, got %v", err)
	}
}

// TestIdentityTenancyMigrationIsExpandOnly 验证对应场景下的正常路径与失败路径。
func TestIdentityTenancyMigrationIsExpandOnly(t *testing.T) {
	contents, err := migrationsFS.ReadFile("migrations/016_identity_tenancy_expand.sql")
	if err != nil {
		t.Fatalf("read identity migration: %v", err)
	}
	sql := string(contents)

	for _, table := range []string{"users", "organizations", "workspaces", "memberships", "principal_audit_events"} {
		if !strings.Contains(sql, "CREATE TABLE IF NOT EXISTS "+table) {
			t.Fatalf("expected migration to create %s", table)
		}
	}
	for _, table := range []string{"resources", "tasks", "approvals", "execution_jobs", "assistant_sessions", "uploaded_files"} {
		if !strings.Contains(sql, "ALTER TABLE "+table) {
			t.Fatalf("expected nullable scope expansion for %s", table)
		}
		alterBlock := regexp.MustCompile(`(?is)ALTER\s+TABLE\s+` + regexp.QuoteMeta(table) + `\b.*?;`).FindString(sql)
		if strings.Contains(strings.ToUpper(alterBlock), "NOT NULL") {
			t.Fatalf("existing table %s must receive nullable columns only", table)
		}
	}

	destructive := regexp.MustCompile(`(?mi)^\s*(UPDATE\s|DELETE\s+FROM\s|DROP\s|TRUNCATE\s|ALTER\s+TABLE[^;]*SET\s+NOT\s+NULL)`)
	if match := destructive.FindString(sql); match != "" {
		t.Fatalf("identity migration must remain expand-only; found %q", strings.TrimSpace(match))
	}
}

// TestIdentityRepoFoundationRoundTrip 验证对应场景下的正常路径与失败路径。
func TestIdentityRepoFoundationRoundTrip(t *testing.T) {
	pool := newTestPool(t)
	ctx := testContext(t)
	repo := NewIdentityRepo(pool)
	suffix := uniqueSuffix()

	user, err := repo.CreateUser(ctx, CreateUserParams{DisplayName: "Test User"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	organization, err := repo.CreateOrganization(ctx, "org-"+suffix, "Test Organization")
	if err != nil {
		t.Fatalf("create organization: %v", err)
	}
	workspace, err := repo.CreateWorkspace(ctx, organization.ID, "workspace-"+suffix, "Test Workspace")
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	membership, err := repo.CreateMembership(ctx, workspace.ID, user.ID, MembershipRoleOwner)
	if err != nil {
		t.Fatalf("create membership: %v", err)
	}

	stored, err := repo.GetMembership(ctx, workspace.ID, user.ID)
	if err != nil {
		t.Fatalf("get membership: %v", err)
	}
	if stored == nil || stored.ID != membership.ID || stored.Role != MembershipRoleOwner {
		t.Fatalf("unexpected membership: %#v", stored)
	}

	auditID, err := repo.RecordPrincipalAuditEvent(ctx, PrincipalAuditEventParams{
		WorkspaceID:   &workspace.ID,
		PrincipalType: "user",
		PrincipalID:   user.ID,
		Action:        "identity.foundation.test",
		Decision:      "observe",
	})
	if err != nil {
		t.Fatalf("record principal audit event: %v", err)
	}
	if auditID == "" {
		t.Fatal("expected audit event id")
	}

}

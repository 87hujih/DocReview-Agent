package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MembershipRole 为 the persisted 工作区 role contract used 由 later policy work.
type MembershipRole string

const (
	MembershipRoleOwner  MembershipRole = "owner"
	MembershipRoleAdmin  MembershipRole = "admin"
	MembershipRoleEditor MembershipRole = "editor"
	MembershipRoleViewer MembershipRole = "viewer"
)

// User 为 一个 application 标识. External 标识 linkage remains 提供方-neutral.
type User struct {
	ID              string
	ExternalIssuer  *string
	ExternalSubject *string
	Email           *string
	DisplayName     string
	Status          string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type Organization struct {
	ID        string
	Slug      string
	Name      string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Workspace struct {
	ID             string
	OrganizationID string
	Slug           string
	Name           string
	Status         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Membership struct {
	ID          string
	WorkspaceID string
	UserID      string
	Role        MembershipRole
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type CreateUserParams struct {
	ExternalIssuer  *string
	ExternalSubject *string
	Email           *string
	DisplayName     string
}

type PrincipalAuditEventParams struct {
	WorkspaceID   *string
	PrincipalType string
	PrincipalID   string
	Action        string
	ResourceType  *string
	ResourceID    *string
	Decision      string
	ReasonCode    *string
	RequestID     *string
	Metadata      json.RawMessage
}

// IdentityRepo owns only persistence contracts; it 为 not wired into 公开的 请求 handling yet.
type IdentityRepo struct {
	pool *pgxpool.Pool
}

// NewIdentityRepo 校验依赖并创建对应实例。
func NewIdentityRepo(pool *pgxpool.Pool) *IdentityRepo {
	return &IdentityRepo{pool: pool}
}

// CreateUser 按领域约束持久化数据。
func (r *IdentityRepo) CreateUser(ctx context.Context, params CreateUserParams) (*User, error) {
	issuer := trimOptional(params.ExternalIssuer)
	subject := trimOptional(params.ExternalSubject)
	if (issuer == nil) != (subject == nil) {
		return nil, fmt.Errorf("external issuer 和 subject 必须为 supplied together")
	}

	user, err := scanUser(r.pool.QueryRow(ctx, `
		INSERT INTO users (external_issuer, external_subject, email, display_name)
		VALUES ($1, $2, $3, $4)
		RETURNING id, external_issuer, external_subject, email, display_name, status, created_at, updated_at
	`, issuer, subject, trimOptional(params.Email), strings.TrimSpace(params.DisplayName)))
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// CreateOrganization 按领域约束持久化数据。
func (r *IdentityRepo) CreateOrganization(ctx context.Context, slug string, name string) (*Organization, error) {
	organization, err := scanOrganization(r.pool.QueryRow(ctx, `
		INSERT INTO organizations (slug, name)
		VALUES ($1, $2)
		RETURNING id, slug, name, status, created_at, updated_at
	`, strings.TrimSpace(slug), strings.TrimSpace(name)))
	if err != nil {
		return nil, err
	}
	return &organization, nil
}

// CreateWorkspace 按领域约束持久化数据。
func (r *IdentityRepo) CreateWorkspace(ctx context.Context, organizationID string, slug string, name string) (*Workspace, error) {
	workspace, err := scanWorkspace(r.pool.QueryRow(ctx, `
		INSERT INTO workspaces (organization_id, slug, name)
		VALUES ($1, $2, $3)
		RETURNING id, organization_id, slug, name, status, created_at, updated_at
	`, organizationID, strings.TrimSpace(slug), strings.TrimSpace(name)))
	if err != nil {
		return nil, err
	}
	return &workspace, nil
}

// CreateMembership 按领域约束持久化数据。
func (r *IdentityRepo) CreateMembership(ctx context.Context, workspaceID string, userID string, role MembershipRole) (*Membership, error) {
	if !role.Valid() {
		return nil, fmt.Errorf("不支持的 membership role %q", role)
	}

	membership, err := scanMembership(r.pool.QueryRow(ctx, `
		INSERT INTO memberships (workspace_id, user_id, role)
		VALUES ($1, $2, $3)
		RETURNING id, workspace_id, user_id, role, status, created_at, updated_at
	`, workspaceID, userID, role))
	if err != nil {
		return nil, err
	}
	return &membership, nil
}

// GetMembership 按作用域读取并返回所需数据。
func (r *IdentityRepo) GetMembership(ctx context.Context, workspaceID string, userID string) (*Membership, error) {
	membership, err := scanMembership(r.pool.QueryRow(ctx, `
		SELECT id, workspace_id, user_id, role, status, created_at, updated_at
		FROM memberships
		WHERE workspace_id = $1 AND user_id = $2
	`, workspaceID, userID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &membership, nil
}

// RecordPrincipalAuditEvent 按领域约束持久化数据。
func (r *IdentityRepo) RecordPrincipalAuditEvent(ctx context.Context, params PrincipalAuditEventParams) (string, error) {
	metadata := params.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	if !json.Valid(metadata) {
		return "", fmt.Errorf("principal audit metadata must be valid JSON")
	}

	var id string
	err := r.pool.QueryRow(ctx, `
		INSERT INTO principal_audit_events (
			workspace_id, principal_type, principal_id, action,
			resource_type, resource_id, decision, reason_code, request_id, metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id
	`, params.WorkspaceID, params.PrincipalType, params.PrincipalID, params.Action,
		params.ResourceType, params.ResourceID, params.Decision, params.ReasonCode, params.RequestID, metadata).Scan(&id)
	return id, err
}

// 有效的 执行该函数负责的核心处理逻辑。
func (role MembershipRole) Valid() bool {
	// 根据当前状态或类型选择对应的处理分支。
	switch role {
	case MembershipRoleOwner, MembershipRoleAdmin, MembershipRoleEditor, MembershipRoleViewer:
		return true
	default:
		return false
	}
}

// trimOptional 执行该函数负责的核心处理逻辑。
func trimOptional(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// scanUser 执行该函数负责的核心处理逻辑。
func scanUser(row pgx.Row) (User, error) {
	var value User
	err := row.Scan(&value.ID, &value.ExternalIssuer, &value.ExternalSubject, &value.Email, &value.DisplayName, &value.Status, &value.CreatedAt, &value.UpdatedAt)
	return value, err
}

// scanOrganization 执行该函数负责的核心处理逻辑。
func scanOrganization(row pgx.Row) (Organization, error) {
	var value Organization
	err := row.Scan(&value.ID, &value.Slug, &value.Name, &value.Status, &value.CreatedAt, &value.UpdatedAt)
	return value, err
}

// scanWorkspace 执行该函数负责的核心处理逻辑。
func scanWorkspace(row pgx.Row) (Workspace, error) {
	var value Workspace
	err := row.Scan(&value.ID, &value.OrganizationID, &value.Slug, &value.Name, &value.Status, &value.CreatedAt, &value.UpdatedAt)
	return value, err
}

// scanMembership 执行该函数负责的核心处理逻辑。
func scanMembership(row pgx.Row) (Membership, error) {
	var value Membership
	err := row.Scan(&value.ID, &value.WorkspaceID, &value.UserID, &value.Role, &value.Status, &value.CreatedAt, &value.UpdatedAt)
	return value, err
}

// Package 标识 owns the authenticated 主体和 WorkspaceScope 信任
// boundary used 由 the 持久化的 Agent 运行时. 模型和请求负载 fields
// 为 never accepted as 标识 facts.
package identity

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	HeaderPrincipalType  = "X-DocReview-Principal-Type"
	HeaderPrincipalID    = "X-DocReview-Principal-ID"
	HeaderOrganizationID = "X-DocReview-Organization-ID"
	HeaderWorkspaceID    = "X-DocReview-Workspace-ID"
	HeaderIssuedAt       = "X-DocReview-Identity-Issued-At"
	HeaderRoles          = "X-DocReview-Roles"
	HeaderSignature      = "X-DocReview-Identity-Signature"
)

var ErrUntrustedIdentity = errors.New("不可信的持久化的标识")

type Principal struct {
	Type           string
	ID             string
	OrganizationID string
	Roles          []string
}

type WorkspaceScope struct {
	Principal   Principal
	WorkspaceID string
	TrustSource string
	Trusted     bool
	IssuedAt    time.Time
}

type Request struct {
	Method    string
	Path      string
	RequestID string
	Header    http.Header
}

type Adapter interface {
	Authenticate(ctx context.Context, request Request, requestedWorkspaceID string) (WorkspaceScope, error)
}

type TrustedIngressConfig struct {
	Secret      string
	TrustSource string
	MaxAge      time.Duration
	Now         func() time.Time
}

type TrustedIngressAdapter struct {
	secret      []byte
	trustSource string
	maxAge      time.Duration
	now         func() time.Time
}

// NewTrustedIngressAdapter 校验依赖并创建对应实例。
func NewTrustedIngressAdapter(cfg TrustedIngressConfig) (*TrustedIngressAdapter, error) {
	if len(cfg.Secret) < 32 || strings.TrimSpace(cfg.TrustSource) == "" || cfg.MaxAge <= 0 {
		return nil, fmt.Errorf("可信的入口需要一个 32-byte 密钥、来源、和正数最大有效期")
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	return &TrustedIngressAdapter{
		secret: []byte(cfg.Secret), trustSource: strings.TrimSpace(cfg.TrustSource),
		maxAge: cfg.MaxAge, now: cfg.Now,
	}, nil
}

// Authenticate 执行该函数负责的核心处理逻辑。
func (adapter *TrustedIngressAdapter) Authenticate(_ context.Context, request Request, requestedWorkspaceID string) (WorkspaceScope, error) {
	if adapter == nil || len(adapter.secret) == 0 {
		return WorkspaceScope{}, fmt.Errorf("%w：可信的入口适配器不可用", ErrUntrustedIdentity)
	}
	request.Method = strings.ToUpper(strings.TrimSpace(request.Method))
	request.Path = strings.TrimSpace(request.Path)
	request.RequestID = strings.TrimSpace(request.RequestID)
	requestedWorkspaceID = strings.TrimSpace(requestedWorkspaceID)
	if request.Header == nil || request.Method == "" || request.Path == "" || request.RequestID == "" {
		return WorkspaceScope{}, fmt.Errorf("%w：请求绑定信息为不完整", ErrUntrustedIdentity)
	}

	principalType := strings.ToLower(strings.TrimSpace(request.Header.Get(HeaderPrincipalType)))
	principalID := strings.TrimSpace(request.Header.Get(HeaderPrincipalID))
	organizationID := strings.TrimSpace(request.Header.Get(HeaderOrganizationID))
	workspaceID := strings.TrimSpace(request.Header.Get(HeaderWorkspaceID))
	issuedAtRaw := strings.TrimSpace(request.Header.Get(HeaderIssuedAt))
	rolesRaw := strings.TrimSpace(request.Header.Get(HeaderRoles))
	if principalType != "user" && principalType != "service" {
		return WorkspaceScope{}, fmt.Errorf("%w：主体类型无效", ErrUntrustedIdentity)
	}
	for name, value := range map[string]string{
		"principal": principalID, "organization": organizationID, "workspace": workspaceID,
	} {
		if _, err := uuid.Parse(value); err != nil {
			return WorkspaceScope{}, fmt.Errorf("%w：%s 标识无效", ErrUntrustedIdentity, name)
		}
	}
	issuedAt, err := time.Parse(time.RFC3339Nano, issuedAtRaw)
	if err != nil {
		return WorkspaceScope{}, fmt.Errorf("%w：标识时间戳无效", ErrUntrustedIdentity)
	}
	now := adapter.now().UTC()
	if issuedAt.After(now.Add(30*time.Second)) || now.Sub(issuedAt) > adapter.maxAge {
		return WorkspaceScope{}, fmt.Errorf("%w：标识签名证明已过期", ErrUntrustedIdentity)
	}

	providedSignature, err := hex.DecodeString(strings.TrimSpace(request.Header.Get(HeaderSignature)))
	if err != nil || len(providedSignature) != sha256.Size {
		return WorkspaceScope{}, fmt.Errorf("%w：标识签名无效", ErrUntrustedIdentity)
	}
	canonical := strings.Join([]string{
		"v1", request.RequestID, request.Method, request.Path, principalType, principalID,
		organizationID, workspaceID, issuedAtRaw, rolesRaw,
	}, "\n")
	mac := hmac.New(sha256.New, adapter.secret)
	_, _ = mac.Write([]byte(canonical))
	if !hmac.Equal(providedSignature, mac.Sum(nil)) {
		return WorkspaceScope{}, fmt.Errorf("%w：标识签名不匹配", ErrUntrustedIdentity)
	}
	if requestedWorkspaceID == "" || workspaceID != requestedWorkspaceID {
		return WorkspaceScope{}, fmt.Errorf("%w：请求的工作区不匹配可信的作用域", ErrUntrustedIdentity)
	}

	return WorkspaceScope{
		Principal:   Principal{Type: principalType, ID: principalID, OrganizationID: organizationID, Roles: normalizeRoles(rolesRaw)},
		WorkspaceID: workspaceID, TrustSource: adapter.trustSource, Trusted: true, IssuedAt: issuedAt.UTC(),
	}, nil
}

// normalizeRoles 执行该函数负责的核心处理逻辑。
func normalizeRoles(raw string) []string {
	seen := make(map[string]struct{})
	for _, value := range strings.Split(raw, ",") {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	roles := make([]string, 0, len(seen))
	for role := range seen {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	return roles
}

var _ Adapter = (*TrustedIngressAdapter)(nil)

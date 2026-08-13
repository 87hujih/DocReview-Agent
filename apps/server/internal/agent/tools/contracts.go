// Package tools defines the only 执行 protocol 用于 Agent tools. 工具
// implementations do not 鉴权 themselves 和 不能 写入 audit records
// outside 运行时.
package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type RiskLevel string

const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

type IdempotencyMode string

const (
	IdempotencyNone     IdempotencyMode = "none"
	IdempotencyOptional IdempotencyMode = "optional"
	IdempotencyRequired IdempotencyMode = "required"
)

type DataClassification string

const (
	DataPublic       DataClassification = "public"
	DataInternal     DataClassification = "internal"
	DataConfidential DataClassification = "confidential"
	DataRestricted   DataClassification = "restricted"
)

type ErrorCategory string

const (
	ErrorInvalidInput      ErrorCategory = "invalid_input"
	ErrorPermissionDenied  ErrorCategory = "permission_denied"
	ErrorNotFound          ErrorCategory = "not_found"
	ErrorConflict          ErrorCategory = "conflict"
	ErrorRateLimited       ErrorCategory = "rate_limited"
	ErrorTimeout           ErrorCategory = "timeout"
	ErrorRetryableUpstream ErrorCategory = "retryable_upstream"
	ErrorTerminalUpstream  ErrorCategory = "terminal_upstream"
	ErrorPolicyBlocked     ErrorCategory = "policy_blocked"
	ErrorCancelled         ErrorCategory = "cancelled"
)

type RetryPolicy struct {
	MaxAttempts int
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
}

type Descriptor struct {
	Name                string
	Version             string
	Description         string
	InputSchema         json.RawMessage
	OutputSchema        json.RawMessage
	RequiredPermissions []string
	ResourceSelectors   []ResourceSelector
	RiskLevel           RiskLevel
	Timeout             time.Duration
	RetryPolicy         RetryPolicy
	IdempotencyMode     IdempotencyMode
	MaxResultTokens     int
	DataClassification  DataClassification
}

type ResourceSelector struct {
	Type       string
	InputField string
	Access     ResourceAccess
}

type SecurityContext struct {
	PrincipalType string
	PrincipalID   string
	WorkspaceID   string
	ResourceID    string
	RequestID     string
	RunID         string
	StepID        string
}

type Call struct {
	ID             string
	RunID          string
	StepID         string
	ToolName       string
	ToolVersion    string
	Input          json.RawMessage
	IdempotencyKey string
	ApprovalID     string
	TraceID        string
	Security       SecurityContext
}

type ResourceAccess string

const (
	AccessRead  ResourceAccess = "read"
	AccessWrite ResourceAccess = "write"
)

type ResourceRef struct {
	Type   string         `json:"type"`
	ID     string         `json:"id"`
	Access ResourceAccess `json:"access"`
}

type AuthorizationRequest struct {
	Descriptor Descriptor
	Call       Call
	Resources  []ResourceRef
}

type PolicyOutcome string

const (
	PolicyAllow           PolicyOutcome = "allow"
	PolicyDeny            PolicyOutcome = "deny"
	PolicyRequireApproval PolicyOutcome = "require_approval"
)

type PolicyDecision struct {
	Outcome    PolicyOutcome `json:"outcome"`
	ReasonCode string        `json:"reason_code"`
}

type ApprovalCheck struct {
	ApprovalID     string
	Principal      SecurityContext
	RunID          string
	StepID         string
	ToolName       string
	ToolVersion    string
	IdempotencyKey string
	Resources      []ResourceRef
}

// HashResources 执行该函数负责的核心处理逻辑。
func HashResources(resources []ResourceRef) string {
	canonical := append([]ResourceRef(nil), resources...)
	sort.Slice(canonical, func(i, j int) bool {
		if canonical[i].Type != canonical[j].Type {
			return canonical[i].Type < canonical[j].Type
		}
		if canonical[i].ID != canonical[j].ID {
			return canonical[i].ID < canonical[j].ID
		}
		return canonical[i].Access < canonical[j].Access
	})
	encoded, _ := json.Marshal(canonical)
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", digest[:])
}

type Authorizer interface {
	Authorize(ctx context.Context, request AuthorizationRequest) (PolicyDecision, error)
}

type Provenance struct {
	SourceType  string `json:"source_type"`
	SourceID    string `json:"source_id"`
	ResourceID  string `json:"resource_id,omitempty"`
	VersionID   string `json:"version_id,omitempty"`
	ContentHash string `json:"content_hash,omitempty"`
	TrustLevel  string `json:"trust_level"`
	Provider    string `json:"provider,omitempty"`
}

type ArtifactReference struct {
	ID          string `json:"id"`
	URI         string `json:"uri"`
	ContentHash string `json:"content_hash"`
	TokenCount  int    `json:"token_count"`
}

type Result struct {
	Output          json.RawMessage    `json:"output"`
	Provenance      []Provenance       `json:"provenance"`
	Artifact        *ArtifactReference `json:"artifact,omitempty"`
	OversizeSummary json.RawMessage    `json:"-"`
}

type Tool interface {
	Descriptor() Descriptor
	Execute(ctx context.Context, call Call) (Result, error)
}

type ToolError struct {
	Category ErrorCategory   `json:"category"`
	Message  string          `json:"message"`
	Details  json.RawMessage `json:"details,omitempty"`
	Cause    error           `json:"-"`
}

// 错误 执行该函数负责的核心处理逻辑。
func (e *ToolError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("tool %s: %s", e.Category, e.Message)
}

// Unwrap 执行该函数负责的核心处理逻辑。
func (e *ToolError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Retryable 执行该函数负责的核心处理逻辑。
func (category ErrorCategory) Retryable() bool {
	// 根据当前状态或类型选择对应的处理分支。
	switch category {
	case ErrorRateLimited, ErrorTimeout, ErrorRetryableUpstream:
		return true
	default:
		return false
	}
}

// 有效的 执行该函数负责的核心处理逻辑。
func (category ErrorCategory) Valid() bool {
	// 根据当前状态或类型选择对应的处理分支。
	switch category {
	case ErrorInvalidInput, ErrorPermissionDenied, ErrorNotFound, ErrorConflict,
		ErrorRateLimited, ErrorTimeout, ErrorRetryableUpstream, ErrorTerminalUpstream,
		ErrorPolicyBlocked, ErrorCancelled:
		return true
	default:
		return false
	}
}

// validate 校验输入及领域约束。
func (descriptor Descriptor) validate() error {
	descriptor.Name = strings.TrimSpace(descriptor.Name)
	descriptor.Version = strings.TrimSpace(descriptor.Version)
	descriptor.Description = strings.TrimSpace(descriptor.Description)
	if descriptor.Name == "" || descriptor.Version == "" || descriptor.Description == "" {
		return fmt.Errorf("工具 name、版本、和 description 不能为空")
	}
	for name, schema := range map[string]json.RawMessage{"input": descriptor.InputSchema, "output": descriptor.OutputSchema} {
		compiled, err := compileSchema(schema)
		if err != nil {
			return fmt.Errorf("工具 %s schema：%w", name, err)
		}
		if compiled.typeName != "object" {
			return fmt.Errorf("工具 %s schema root 类型必须为对象", name)
		}
	}
	if !descriptor.RiskLevel.valid() || !descriptor.IdempotencyMode.valid() || !descriptor.DataClassification.valid() ||
		descriptor.Timeout <= 0 || descriptor.Timeout > 10*time.Minute || descriptor.RetryPolicy.MaxAttempts < 1 ||
		descriptor.RetryPolicy.MaxAttempts > 10 || descriptor.MaxResultTokens <= 0 {
		return fmt.Errorf("工具执行 policy 无效")
	}
	if descriptor.RetryPolicy.MaxAttempts > 1 && (descriptor.RetryPolicy.BaseBackoff <= 0 || descriptor.RetryPolicy.MaxBackoff < descriptor.RetryPolicy.BaseBackoff) {
		return fmt.Errorf("工具 retry backoff policy 无效")
	}
	if descriptor.RetryPolicy.MaxBackoff > time.Minute {
		return fmt.Errorf("工具 retry backoff 超过一个 minute")
	}
	for _, permission := range descriptor.RequiredPermissions {
		if strings.TrimSpace(permission) == "" {
			return fmt.Errorf("工具权限s 不能为空")
		}
	}
	for _, selector := range descriptor.ResourceSelectors {
		if strings.TrimSpace(selector.Type) == "" || strings.TrimSpace(selector.InputField) == "" ||
			(selector.Access != AccessRead && selector.Access != AccessWrite) {
			return fmt.Errorf("工具资源 selectors 无效")
		}
		if selector.Access == AccessWrite && descriptor.IdempotencyMode != IdempotencyRequired {
			return fmt.Errorf("写入工具必须需要 idempotency")
		}
	}
	return nil
}

// 有效的 执行该函数负责的核心处理逻辑。
func (risk RiskLevel) valid() bool {
	return risk == RiskLow || risk == RiskMedium || risk == RiskHigh || risk == RiskCritical
}

// 有效的 执行该函数负责的核心处理逻辑。
func (mode IdempotencyMode) valid() bool {
	return mode == IdempotencyNone || mode == IdempotencyOptional || mode == IdempotencyRequired
}

// 有效的 执行该函数负责的核心处理逻辑。
func (classification DataClassification) valid() bool {
	return classification == DataPublic || classification == DataInternal || classification == DataConfidential || classification == DataRestricted
}

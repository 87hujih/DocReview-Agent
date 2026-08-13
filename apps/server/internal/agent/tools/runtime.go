package tools

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type AuditStatus string

const (
	AuditPending   AuditStatus = "pending"
	AuditRunning   AuditStatus = "running"
	AuditSucceeded AuditStatus = "succeeded"
	AuditFailed    AuditStatus = "failed"
	AuditCancelled AuditStatus = "cancelled"
)

type AuditStart struct {
	Call       Call
	Descriptor Descriptor
	StartedAt  time.Time
}

type AuditRecord struct {
	ID              string
	Acquired        bool
	Status          AuditStatus
	ClaimedBy       string
	LeaseGeneration int64
	Result          *Result
	Error           *ToolError
}

type AuditFinish struct {
	ID              string
	Status          AuditStatus
	ClaimedBy       string
	LeaseGeneration int64
	Result          *Result
	Error           *ToolError
	Attempts        int
	LatencyMS       int64
	CompletedAt     time.Time
}

type AuditStore interface {
	Begin(ctx context.Context, start AuditStart) (AuditRecord, error)
	Finish(ctx context.Context, finish AuditFinish) error
}

type Execution struct {
	CallID    string
	Status    AuditStatus
	Result    *Result
	Error     *ToolError
	Attempts  int
	LatencyMS int64
	Replayed  bool
}

type RuntimeConfig struct {
	Registry   *Registry
	Authorizer Authorizer
	Audit      AuditStore
	Limiter    RateLimiter
	Counter    TokenCounter
	Artifacts  ArtifactStore
	Sleeper    Sleeper
}

type TokenCounter interface {
	CountJSON(value json.RawMessage) int
}

type ArtifactWrite struct {
	WorkspaceID        string
	RunID              string
	StepID             string
	IdempotencyKey     string
	ToolName           string
	ToolVersion        string
	DataClassification DataClassification
	Content            json.RawMessage
	TokenCount         int
	Provenance         []Provenance
}

type ArtifactStore interface {
	Write(ctx context.Context, input ArtifactWrite) (ArtifactReference, error)
}

type RateLimitRequest struct {
	ToolName    string
	ToolVersion string
	RiskLevel   RiskLevel
	Security    SecurityContext
}

type RateLimitDecision struct {
	Allowed    bool
	RetryAfter time.Duration
}

type RateLimiter interface {
	Allow(ctx context.Context, request RateLimitRequest) (RateLimitDecision, error)
}

type Sleeper interface {
	Sleep(ctx context.Context, delay time.Duration) error
}

type timerSleeper struct{}

// Sleep 执行该函数负责的核心处理逻辑。
func (timerSleeper) Sleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	// 等待并发事件、取消信号或超时结果。
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type Runtime struct {
	registry   *Registry
	authorizer Authorizer
	audit      AuditStore
	limiter    RateLimiter
	counter    TokenCounter
	artifacts  ArtifactStore
	sleeper    Sleeper
}

// NewRuntime 校验依赖并创建对应实例。
func NewRuntime(cfg RuntimeConfig) (*Runtime, error) {
	if cfg.Registry == nil || cfg.Authorizer == nil || cfg.Audit == nil || cfg.Limiter == nil || cfg.Counter == nil || cfg.Artifacts == nil {
		return nil, fmt.Errorf("工具 registry、authorizer、audit、rate limiter、计数器、和制品存储不能为空")
	}
	if cfg.Sleeper == nil {
		cfg.Sleeper = timerSleeper{}
	}
	return &Runtime{
		registry: cfg.Registry, authorizer: cfg.Authorizer, audit: cfg.Audit,
		limiter: cfg.Limiter, counter: cfg.Counter, artifacts: cfg.Artifacts, sleeper: cfg.Sleeper,
	}, nil
}

// Execute 执行该函数负责的核心处理逻辑。
func (r *Runtime) Execute(ctx context.Context, call Call) (Execution, error) {
	call.RunID = strings.TrimSpace(call.RunID)
	call.StepID = strings.TrimSpace(call.StepID)
	call.ToolName = strings.TrimSpace(call.ToolName)
	call.ToolVersion = strings.TrimSpace(call.ToolVersion)
	if call.RunID == "" || call.StepID == "" || call.ToolName == "" || call.ToolVersion == "" {
		return Execution{}, fmt.Errorf("run_id、step_id、工具_name、和工具_version 不能为空")
	}
	registered, err := r.registry.resolveRegistered(call.ToolName, call.ToolVersion)
	if err != nil {
		return Execution{}, err
	}
	descriptor := registered.descriptor
	startedAt := time.Now().UTC()
	// 开启事务，确保后续状态变更以原子方式提交。
	audit, err := r.audit.Begin(ctx, AuditStart{Call: call, Descriptor: descriptor, StartedAt: startedAt})
	if err != nil {
		return Execution{}, fmt.Errorf("begin 工具 audit：%w", err)
	}
	if !audit.Acquired {
		return replayAudit(audit), nil
	}
	if descriptor.IdempotencyMode == IdempotencyRequired && strings.TrimSpace(call.IdempotencyKey) == "" {
		failure := &ToolError{Category: ErrorInvalidInput, Message: "工具调用必须提供幂等键", Details: json.RawMessage(`{"stage":"idempotency"}`)}
		return r.finish(ctx, audit, startedAt, nil, failure, 0)
	}
	if err := registered.input.validateJSON(call.Input); err != nil {
		failure := &ToolError{Category: ErrorInvalidInput, Message: err.Error(), Details: json.RawMessage(`{"stage":"input_schema"}`)}
		return r.finish(ctx, audit, startedAt, nil, failure, 0)
	}
	resources, err := extractResources(descriptor, call.Input)
	if err != nil {
		failure := &ToolError{Category: ErrorInvalidInput, Message: err.Error(), Details: json.RawMessage(`{"stage":"resource_selector"}`)}
		return r.finish(ctx, audit, startedAt, nil, failure, 0)
	}

	decision, err := r.authorizer.Authorize(ctx, AuthorizationRequest{Descriptor: descriptor, Call: call, Resources: resources})
	if err != nil {
		return Execution{}, fmt.Errorf("鉴权工具调用：%w", err)
	}
	if decision.Outcome != PolicyAllow {
		category := ErrorPolicyBlocked
		if decision.Outcome == PolicyDeny && decision.ReasonCode == "permission_denied" {
			category = ErrorPermissionDenied
		}
		failure := &ToolError{Category: category, Message: decision.ReasonCode}
		return r.finish(ctx, audit, startedAt, nil, failure, 0)
	}
	limit, err := r.limiter.Allow(ctx, RateLimitRequest{
		ToolName: descriptor.Name, ToolVersion: descriptor.Version,
		RiskLevel: descriptor.RiskLevel, Security: call.Security,
	})
	if err != nil {
		failure := &ToolError{Category: ErrorPolicyBlocked, Message: "速率限制检查失败", Cause: err}
		return r.finish(ctx, audit, startedAt, nil, failure, 0)
	}
	if !limit.Allowed {
		details, _ := json.Marshal(map[string]int64{"retry_after_ms": limit.RetryAfter.Milliseconds()})
		failure := &ToolError{Category: ErrorRateLimited, Message: "工具调用超过速率限制", Details: details}
		return r.finish(ctx, audit, startedAt, nil, failure, 0)
	}

	for attempt := 1; attempt <= descriptor.RetryPolicy.MaxAttempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, descriptor.Timeout)
		result, executeErr := registered.tool.Execute(attemptCtx, call)
		attemptContextErr := attemptCtx.Err()
		cancel()
		failure := classifyExecutionError(ctx, attemptContextErr, executeErr)
		if failure == nil {
			if err := registered.output.validateJSON(result.Output); err != nil {
				failure = &ToolError{Category: ErrorTerminalUpstream, Message: "工具输出违反模式约束：" + err.Error()}
			} else if err := validateProvenance(result); err != nil {
				failure = &ToolError{Category: ErrorTerminalUpstream, Message: err.Error()}
			} else {
				bounded, err := r.boundResult(ctx, call, descriptor, result)
				if err != nil {
					failure = &ToolError{Category: ErrorTerminalUpstream, Message: "存储超大工具结果失败：" + err.Error(), Cause: err}
				} else {
					return r.finish(ctx, audit, startedAt, &bounded, nil, attempt)
				}
			}
		}
		if !failure.Category.Retryable() || attempt == descriptor.RetryPolicy.MaxAttempts {
			finishCtx := ctx
			if failure.Category == ErrorCancelled {
				finishCtx = context.WithoutCancel(ctx)
			}
			return r.finish(finishCtx, audit, startedAt, nil, failure, attempt)
		}
		if err := r.sleeper.Sleep(ctx, retryDelay(descriptor.RetryPolicy, attempt)); err != nil {
			cancelled := &ToolError{Category: ErrorCancelled, Message: "工具重试已取消", Cause: err}
			return r.finish(context.WithoutCancel(ctx), audit, startedAt, nil, cancelled, attempt)
		}
	}
	panic("工具重试循环进入不可达分支")
}

// boundResult 执行该函数负责的核心处理逻辑。
func (r *Runtime) boundResult(ctx context.Context, call Call, descriptor Descriptor, result Result) (Result, error) {
	tokens := r.counter.CountJSON(result.Output)
	if tokens < 0 {
		return Result{}, fmt.Errorf("计数器返回了一个负数结果")
	}
	if tokens <= descriptor.MaxResultTokens {
		return result, nil
	}
	artifact, err := r.artifacts.Write(ctx, ArtifactWrite{
		WorkspaceID: call.Security.WorkspaceID, RunID: call.RunID, StepID: call.StepID,
		IdempotencyKey: artifactIdempotencyKey(call, result.Output),
		ToolName:       descriptor.Name, ToolVersion: descriptor.Version,
		DataClassification: descriptor.DataClassification, Content: append(json.RawMessage(nil), result.Output...),
		TokenCount: tokens, Provenance: append([]Provenance(nil), result.Provenance...),
	})
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(artifact.ID) == "" || strings.TrimSpace(artifact.URI) == "" || strings.TrimSpace(artifact.ContentHash) == "" {
		return Result{}, fmt.Errorf("制品存储返回了一个无效的引用")
	}
	modelSummary := any("tool result stored as artifact")
	if len(result.OversizeSummary) > 0 {
		if len(result.OversizeSummary) > 64*1024 {
			return Result{}, fmt.Errorf("工具 oversize summary 超过 64 KiB")
		}
		var summaryObject map[string]any
		if err := json.Unmarshal(result.OversizeSummary, &summaryObject); err != nil || summaryObject == nil {
			return Result{}, fmt.Errorf("工具 oversize summary 必须为一个 JSON 对象")
		}
		modelSummary = summaryObject
	}
	summary, err := json.Marshal(map[string]any{
		"truncated": true, "summary": modelSummary,
		"artifact_uri": artifact.URI, "artifact_id": artifact.ID, "full_result_tokens": tokens,
	})
	if err != nil {
		return Result{}, err
	}
	result.Output = summary
	result.Artifact = &artifact
	result.OversizeSummary = nil
	return result, nil
}

// artifactIdempotencyKey 执行该函数负责的核心处理逻辑。
func artifactIdempotencyKey(call Call, content json.RawMessage) string {
	if strings.TrimSpace(call.IdempotencyKey) != "" {
		return "tool-result:" + strings.TrimSpace(call.IdempotencyKey)
	}
	digest := sha256.Sum256(content)
	return fmt.Sprintf("tool-result:%s:%s:%x", call.StepID, call.ToolName, digest[:])
}

// classifyExecutionError 执行该函数负责的核心处理逻辑。
func classifyExecutionError(parent context.Context, attemptContextErr, executeErr error) *ToolError {
	if parent.Err() != nil || errors.Is(executeErr, context.Canceled) {
		return &ToolError{Category: ErrorCancelled, Message: "工具执行已取消", Cause: executeErr}
	}
	if errors.Is(attemptContextErr, context.DeadlineExceeded) || errors.Is(executeErr, context.DeadlineExceeded) {
		return &ToolError{Category: ErrorTimeout, Message: "工具单次执行已超时", Cause: executeErr}
	}
	if executeErr == nil {
		return nil
	}
	var failure *ToolError
	if errors.As(executeErr, &failure) {
		if failure.Category.Valid() {
			return failure
		}
	}
	return &ToolError{Category: ErrorTerminalUpstream, Message: executeErr.Error(), Cause: executeErr}
}

// retryDelay 执行该函数负责的核心处理逻辑。
func retryDelay(policy RetryPolicy, failedAttempt int) time.Duration {
	delay := policy.BaseBackoff
	for index := 1; index < failedAttempt; index++ {
		if delay >= policy.MaxBackoff/2 {
			return policy.MaxBackoff
		}
		delay *= 2
	}
	if delay > policy.MaxBackoff {
		return policy.MaxBackoff
	}
	return delay
}

// validateProvenance 校验输入及领域约束。
func validateProvenance(result Result) error {
	if len(result.Provenance) == 0 {
		return fmt.Errorf("工具结果溯源信息不能为空")
	}
	for _, source := range result.Provenance {
		if strings.TrimSpace(source.SourceType) == "" || strings.TrimSpace(source.SourceID) == "" ||
			(source.TrustLevel != "trusted" && source.TrustLevel != "untrusted") {
			return fmt.Errorf("工具结果溯源信息来源和信任 level 无效")
		}
	}
	if result.Artifact != nil && (strings.TrimSpace(result.Artifact.ID) == "" || strings.TrimSpace(result.Artifact.URI) == "" ||
		strings.TrimSpace(result.Artifact.ContentHash) == "" || result.Artifact.TokenCount < 0) {
		return fmt.Errorf("工具结果制品引用无效")
	}
	return nil
}

// extractResources 执行该函数负责的核心处理逻辑。
func extractResources(descriptor Descriptor, input json.RawMessage) ([]ResourceRef, error) {
	if len(descriptor.ResourceSelectors) == 0 {
		return nil, nil
	}
	var object map[string]any
	if err := json.Unmarshal(input, &object); err != nil {
		return nil, fmt.Errorf("解析资源 selectors：%w", err)
	}
	resources := make([]ResourceRef, 0, len(descriptor.ResourceSelectors))
	for _, selector := range descriptor.ResourceSelectors {
		value, exists := object[selector.InputField]
		resourceID, ok := value.(string)
		resourceID = strings.TrimSpace(resourceID)
		if !exists || !ok || resourceID == "" {
			return nil, fmt.Errorf("资源选择器 %q 必须解析用于一个 non-空 string", selector.InputField)
		}
		resources = append(resources, ResourceRef{Type: selector.Type, ID: resourceID, Access: selector.Access})
	}
	return resources, nil
}

// finish 执行该函数负责的核心处理逻辑。
func (r *Runtime) finish(ctx context.Context, audit AuditRecord, startedAt time.Time, result *Result, failure *ToolError, attempts int) (Execution, error) {
	completedAt := time.Now().UTC()
	status := AuditSucceeded
	if failure != nil {
		status = AuditFailed
		if failure.Category == ErrorCancelled {
			status = AuditCancelled
		}
	}
	latency := completedAt.Sub(startedAt).Milliseconds()
	finish := AuditFinish{
		ID: audit.ID, Status: status, ClaimedBy: audit.ClaimedBy, LeaseGeneration: audit.LeaseGeneration,
		Result: result, Error: failure,
		Attempts: attempts, LatencyMS: latency, CompletedAt: completedAt,
	}
	if err := r.audit.Finish(ctx, finish); err != nil {
		return Execution{}, fmt.Errorf("finish 工具 audit：%w", err)
	}
	return Execution{CallID: audit.ID, Status: status, Result: result, Error: failure, Attempts: attempts, LatencyMS: latency}, nil
}

// replayAudit 执行该函数负责的核心处理逻辑。
func replayAudit(record AuditRecord) Execution {
	return Execution{
		CallID: record.ID, Status: record.Status, Result: record.Result,
		Error: record.Error, Replayed: true,
	}
}

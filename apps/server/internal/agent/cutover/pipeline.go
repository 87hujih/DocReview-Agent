// Package 切换 owns the reversible 旧版/影子/持久化的请求路由器和
// the single transport-neutral 轮次流水线 seam.
package cutover

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"agent_project/apps/server/internal/agent/identity"
)

type Mode string

const (
	ModeLegacy  Mode = "legacy"
	ModeShadow  Mode = "shadow"
	ModeDurable Mode = "durable"
)

var ErrUntrustedDurableScope = errors.New("持久化的 Agent 运行时需要一个可信的主体和工作区作用域")

// 有效的执行该函数负责的核心处理逻辑。
func (mode Mode) Valid() bool {
	return mode == ModeLegacy || mode == ModeShadow || mode == ModeDurable
}

type RouterConfig struct {
	Mode         Mode
	WorkspaceIDs []string
	ResourceIDs  []string
}

type Router struct {
	mu           sync.RWMutex
	mode         Mode
	workspaceIDs map[string]struct{}
	resourceIDs  map[string]struct{}
}

type Decision struct {
	Mode      Mode
	Cohort    bool
	DecidedAt time.Time
}

// NewRouter 校验依赖并创建对应实例。
func NewRouter(cfg RouterConfig) (*Router, error) {
	if !cfg.Mode.Valid() {
		return nil, fmt.Errorf("运行时模式必须为旧版、影子、或持久化的")
	}
	router := &Router{mode: cfg.Mode, workspaceIDs: set(cfg.WorkspaceIDs), resourceIDs: set(cfg.ResourceIDs)}
	if cfg.Mode != ModeLegacy && (len(router.workspaceIDs) == 0 || len(router.resourceIDs) == 0) {
		return nil, fmt.Errorf("影子/持久化的模式需要明确的工作区和资源分组")
	}
	return router, nil
}

// SetMode 执行该函数负责的核心处理逻辑。
func (router *Router) SetMode(mode Mode) bool {
	if router == nil || !mode.Valid() {
		return false
	}
	router.mu.Lock()
	router.mode = mode
	router.mu.Unlock()
	return true
}

// 模式执行该函数负责的核心处理逻辑。
func (router *Router) Mode() Mode {
	if router == nil {
		return ModeLegacy
	}
	router.mu.RLock()
	defer router.mu.RUnlock()
	return router.mode
}

// Route 执行该函数负责的核心处理逻辑。
func (router *Router) Route(request Request) (Decision, error) {
	mode := router.Mode()
	decision := Decision{Mode: ModeLegacy, DecidedAt: time.Now().UTC()}
	if mode == ModeLegacy || router == nil {
		return decision, nil
	}
	workspaceID := strings.TrimSpace(request.WorkspaceID)
	resourceID := strings.TrimSpace(request.ResourceID)
	_, workspaceAllowed := router.workspaceIDs[workspaceID]
	_, resourceAllowed := router.resourceIDs[resourceID]
	if mode == ModeDurable && resourceAllowed && !workspaceAllowed {
		return Decision{}, ErrUntrustedDurableScope
	}
	if !workspaceAllowed || !resourceAllowed {
		return decision, nil
	}
	decision.Cohort = true
	if !trustedFor(request.Scope, workspaceID) {
		if mode == ModeDurable {
			return Decision{}, ErrUntrustedDurableScope
		}
		// 影子评估为 omitted when its 可信的作用域不可用;
		// the existing 旧版请求 remains authoritative.
		return decision, nil
	}
	decision.Mode = mode
	return decision, nil
}

// trustedFor 执行该函数负责的核心处理逻辑。
func trustedFor(scope identity.WorkspaceScope, workspaceID string) bool {
	return scope.Trusted && strings.TrimSpace(scope.TrustSource) != "" &&
		strings.TrimSpace(scope.WorkspaceID) == workspaceID &&
		strings.TrimSpace(scope.Principal.Type) != "" && strings.TrimSpace(scope.Principal.ID) != ""
}

// set 执行该函数负责的核心处理逻辑。
func set(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result[value] = struct{}{}
		}
	}
	return result
}

type Request struct {
	RequestID     string
	TraceID       string
	SessionID     string
	Message       string
	WorkspaceID   string
	ResourceID    string
	AfterSequence int
	Scope         identity.WorkspaceScope
}

type Event struct {
	Sequence int             `json:"sequence"`
	Type     string          `json:"type"`
	Payload  json.RawMessage `json:"payload"`
}

type Observer func(Event) error

type Result struct {
	Mode                Mode
	DTO                 json.RawMessage
	Events              []Event
	Reconciliation      *Comparison
	ReconciliationError string
}

type Runner interface {
	Execute(ctx context.Context, request Request, observe Observer) (Result, error)
}

type ShadowRequest struct {
	Request     Request
	AllowWrites bool
}

type ShadowEvaluator interface {
	Evaluate(ctx context.Context, request ShadowRequest) (Result, error)
}

type ComparisonStatus string

const (
	ComparisonMatched     ComparisonStatus = "matched"
	ComparisonDiverged    ComparisonStatus = "diverged"
	ComparisonUnavailable ComparisonStatus = "unavailable"
)

type Comparison struct {
	RequestID        string
	WorkspaceID      string
	ResourceID       string
	Status           ComparisonStatus
	LegacyResultHash string
	TypedResultHash  string
	LegacyEventHash  string
	TypedEventHash   string
	LegacyDTOHash    string
	TypedDTOHash     string
	Details          json.RawMessage
}

type ComparisonRecorder interface {
	Record(ctx context.Context, comparison Comparison) error
}

type Pipeline struct {
	router   *Router
	legacy   Runner
	durable  Runner
	shadow   ShadowEvaluator
	recorder ComparisonRecorder
}

// DurableOnlyPipeline is the production cutover seam after the global durable
// switch. It intentionally has no legacy runner, shadow evaluator, cohort
// router, or fallback behavior.
type DurableOnlyPipeline struct {
	durable Runner
}

func NewDurableOnlyPipeline(durable Runner) (*DurableOnlyPipeline, error) {
	if durable == nil {
		return nil, fmt.Errorf("持久化运行器不能为空")
	}
	return &DurableOnlyPipeline{durable: durable}, nil
}

func (pipeline *DurableOnlyPipeline) Execute(ctx context.Context, request Request, observe Observer) (Result, error) {
	if pipeline == nil || pipeline.durable == nil {
		return Result{}, fmt.Errorf("持久化流水线不可用")
	}
	result, err := pipeline.durable.Execute(ctx, request, observe)
	result.Mode = ModeDurable
	return result, err
}

var _ Runner = (*DurableOnlyPipeline)(nil)

// NewPipeline 校验依赖并创建对应实例。
func NewPipeline(router *Router, legacy Runner, durable Runner, shadow ShadowEvaluator, recorder ComparisonRecorder) (*Pipeline, error) {
	if router == nil || legacy == nil || durable == nil {
		return nil, fmt.Errorf("切换路由器、旧版运行器、和持久化的运行器不能为空")
	}
	if router.Mode() == ModeShadow && (shadow == nil || recorder == nil) {
		return nil, fmt.Errorf("影子评估器和比较结果记录器不能为空位于影子模式")
	}
	return &Pipeline{router: router, legacy: legacy, durable: durable, shadow: shadow, recorder: recorder}, nil
}

// Execute 为 the 一个轮次流水线 entry point 用于 streaming 和 non-streaming
// 处理失败： transports. The observer changes 投影 only; it never selects 一个 second
// 处理失败： orchestration path.
func (pipeline *Pipeline) Execute(ctx context.Context, request Request, observe Observer) (Result, error) {
	if pipeline == nil || pipeline.router == nil {
		return Result{}, fmt.Errorf("切换流水线不可用")
	}
	decision, err := pipeline.router.Route(request)
	if err != nil {
		return Result{}, err
	}
	// 根据当前状态或类型选择对应的处理分支。
	switch decision.Mode {
	case ModeDurable:
		result, err := pipeline.durable.Execute(ctx, request, observe)
		result.Mode = ModeDurable
		return result, err
	case ModeShadow:
		return pipeline.executeShadow(ctx, request, observe)
	default:
		result, err := pipeline.legacy.Execute(ctx, request, observe)
		result.Mode = ModeLegacy
		return result, err
	}
}

// executeShadow 执行该函数负责的核心处理逻辑。
func (pipeline *Pipeline) executeShadow(ctx context.Context, request Request, observe Observer) (Result, error) {
	legacyResult, err := pipeline.legacy.Execute(ctx, request, observe)
	if err != nil {
		return legacyResult, err
	}
	legacyResult.Mode = ModeShadow
	typedResult, typedErr := pipeline.shadow.Evaluate(ctx, ShadowRequest{Request: request, AllowWrites: false})
	comparison, compareErr := compare(request, legacyResult, typedResult, typedErr)
	if compareErr != nil {
		legacyResult.ReconciliationError = compareErr.Error()
		return legacyResult, nil
	}
	legacyResult.Reconciliation = &comparison
	if err := pipeline.recorder.Record(ctx, comparison); err != nil {
		legacyResult.ReconciliationError = "record shadow comparison: " + err.Error()
	}
	return legacyResult, nil
}

// compare 执行该函数负责的核心处理逻辑。
func compare(request Request, legacy Result, typed Result, typedErr error) (Comparison, error) {
	comparison := Comparison{RequestID: request.RequestID, WorkspaceID: request.WorkspaceID, ResourceID: request.ResourceID}
	legacyDTO, err := canonicalHash(legacy.DTO)
	if err != nil {
		return Comparison{}, fmt.Errorf("规范化旧版 DTO：%w", err)
	}
	legacyEvents, err := canonicalHash(mustJSON(legacy.Events))
	if err != nil {
		return Comparison{}, fmt.Errorf("规范化旧版事件：%w", err)
	}
	comparison.LegacyDTOHash = legacyDTO
	comparison.LegacyEventHash = legacyEvents
	comparison.LegacyResultHash = combinedHash(legacyDTO, legacyEvents)
	if typedErr != nil {
		comparison.Status = ComparisonUnavailable
		comparison.Details = mustJSON(map[string]string{"typed_error": typedErr.Error()})
		return comparison, nil
	}
	typedDTO, err := canonicalHash(typed.DTO)
	if err != nil {
		return Comparison{}, fmt.Errorf("规范化类型化的 DTO：%w", err)
	}
	typedEvents, err := canonicalHash(mustJSON(typed.Events))
	if err != nil {
		return Comparison{}, fmt.Errorf("规范化类型化的事件：%w", err)
	}
	comparison.TypedDTOHash = typedDTO
	comparison.TypedEventHash = typedEvents
	comparison.TypedResultHash = combinedHash(typedDTO, typedEvents)
	comparison.Status = ComparisonDiverged
	if comparison.LegacyDTOHash == comparison.TypedDTOHash && comparison.LegacyEventHash == comparison.TypedEventHash {
		comparison.Status = ComparisonMatched
	}
	comparison.Details = mustJSON(map[string]any{
		"dto_match":   comparison.LegacyDTOHash == comparison.TypedDTOHash,
		"event_match": comparison.LegacyEventHash == comparison.TypedEventHash,
	})
	return comparison, nil
}

// canonicalHash 执行该函数负责的核心处理逻辑。
func canonicalHash(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return fmt.Sprintf("sha256:%x", digest), nil
}

// combinedHash 执行该函数负责的核心处理逻辑。
func combinedHash(values ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(values, "\n")))
	return fmt.Sprintf("sha256:%x", digest)
}

// mustJSON 执行该函数负责的核心处理逻辑。
func mustJSON(value any) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}

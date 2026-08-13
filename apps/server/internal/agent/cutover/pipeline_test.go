package cutover_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"agent_project/apps/server/internal/agent/cutover"
	"agent_project/apps/server/internal/agent/identity"
)

const (
	workspaceID = "11111111-1111-4111-8111-111111111111"
	resourceID  = "22222222-2222-4222-8222-222222222222"
)

// TestDurableRequiresExplicitWorkspaceAndResourceCohortAndTrustedScope 验证对应场景下的正常路径与失败路径。
func TestDurableRequiresExplicitWorkspaceAndResourceCohortAndTrustedScope(t *testing.T) {
	router := mustRouter(t, cutover.ModeDurable)

	decision, err := router.Route(cutover.Request{WorkspaceID: workspaceID, ResourceID: resourceID, Scope: trustedScope()})
	if err != nil || decision.Mode != cutover.ModeDurable {
		t.Fatalf("expected durable cohort, got decision=%#v err=%v", decision, err)
	}
	decision, err = router.Route(cutover.Request{WorkspaceID: workspaceID, ResourceID: "33333333-3333-4333-8333-333333333333", Scope: trustedScope()})
	if err != nil || decision.Mode != cutover.ModeLegacy {
		t.Fatalf("expected non-cohort request to stay legacy, got decision=%#v err=%v", decision, err)
	}
	_, err = router.Route(cutover.Request{WorkspaceID: workspaceID, ResourceID: resourceID})
	if !errors.Is(err, cutover.ErrUntrustedDurableScope) {
		t.Fatalf("expected durable fail-closed error, got %v", err)
	}
}

// TestDurableAllowlistedResourceCannotBypassTrustByOmittingWorkspace 验证对应场景下的正常路径与失败路径。
func TestDurableAllowlistedResourceCannotBypassTrustByOmittingWorkspace(t *testing.T) {
	router := mustRouter(t, cutover.ModeDurable)
	_, err := router.Route(cutover.Request{ResourceID: resourceID})
	if !errors.Is(err, cutover.ErrUntrustedDurableScope) {
		t.Fatalf("allowlisted durable resource bypassed trust with omitted workspace: %v", err)
	}
}

// TestSwitchToLegacyAffectsNewRequestsWithoutChangingStartedDurableLease 验证对应场景下的正常路径与失败路径。
func TestSwitchToLegacyAffectsNewRequestsWithoutChangingStartedDurableLease(t *testing.T) {
	router := mustRouter(t, cutover.ModeDurable)
	started, err := router.Route(cutover.Request{WorkspaceID: workspaceID, ResourceID: resourceID, Scope: trustedScope()})
	if err != nil {
		t.Fatalf("route durable request: %v", err)
	}
	router.SetMode(cutover.ModeLegacy)
	newDecision, err := router.Route(cutover.Request{WorkspaceID: workspaceID, ResourceID: resourceID, Scope: trustedScope()})
	if err != nil {
		t.Fatalf("route after rollback: %v", err)
	}
	if started.Mode != cutover.ModeDurable || newDecision.Mode != cutover.ModeLegacy {
		t.Fatalf("expected immutable started route and legacy new route, got started=%s new=%s", started.Mode, newDecision.Mode)
	}
}

// TestStreamingAndNonStreamingUseTheSameDurableTurnPipeline 验证对应场景下的正常路径与失败路径。
func TestStreamingAndNonStreamingUseTheSameDurableTurnPipeline(t *testing.T) {
	runner := &fakeRunner{result: cutover.Result{DTO: json.RawMessage(`{"status":"accepted"}`), Events: []cutover.Event{{Sequence: 1, Type: "turn.accepted", Payload: json.RawMessage(`{}`)}}}}
	pipeline := mustPipeline(t, cutover.ModeDurable, &fakeRunner{}, runner, nil, nil)
	request := validRequest()

	if _, err := pipeline.Execute(context.Background(), request, nil); err != nil {
		t.Fatalf("execute non-stream turn: %v", err)
	}
	var observed int
	if _, err := pipeline.Execute(context.Background(), request, func(cutover.Event) error { observed++; return nil }); err != nil {
		t.Fatalf("execute stream turn: %v", err)
	}
	if runner.calls != 2 || observed != 1 {
		t.Fatalf("expected one shared durable seam for both transports, calls=%d observed=%d", runner.calls, observed)
	}
}

// TestDurableOnlyPipelineDelegatesEveryRequestToDurable verifies the production
// cutover seam has no legacy fallback or cohort branch.
func TestDurableOnlyPipelineDelegatesEveryRequestToDurable(t *testing.T) {
	runner := &fakeRunner{result: cutover.Result{DTO: json.RawMessage(`{"status":"accepted"}`)}}
	pipeline, err := cutover.NewDurableOnlyPipeline(runner)
	if err != nil {
		t.Fatalf("new durable-only pipeline: %v", err)
	}

	request := cutover.Request{RequestID: "request-1", Message: "review this document"}
	result, err := pipeline.Execute(context.Background(), request, nil)
	if err != nil {
		t.Fatalf("execute durable-only request: %v", err)
	}
	if runner.calls != 1 || result.Mode != cutover.ModeDurable {
		t.Fatalf("expected exactly one durable call, calls=%d mode=%s", runner.calls, result.Mode)
	}
}

func TestDurableOnlyPipelineRejectsMissingRunner(t *testing.T) {
	if _, err := cutover.NewDurableOnlyPipeline(nil); err == nil {
		t.Fatal("expected missing durable runner to be rejected")
	}
}

// TestShadowReturnsLegacyResultAndEvaluatesTypedPathReadOnly 验证对应场景下的正常路径与失败路径。
func TestShadowReturnsLegacyResultAndEvaluatesTypedPathReadOnly(t *testing.T) {
	legacy := &fakeRunner{result: cutover.Result{DTO: json.RawMessage(`{"message":"legacy"}`), Events: []cutover.Event{{Sequence: 1, Type: "message_completed", Payload: json.RawMessage(`{"message":"legacy"}`)}}}}
	shadow := &fakeShadow{result: cutover.Result{DTO: json.RawMessage(`{"message":"typed"}`), Events: []cutover.Event{{Sequence: 1, Type: "message_completed", Payload: json.RawMessage(`{"message":"typed"}`)}}}}
	recorder := &fakeRecorder{}
	pipeline := mustPipeline(t, cutover.ModeShadow, legacy, &fakeRunner{}, shadow, recorder)

	result, err := pipeline.Execute(context.Background(), validRequest(), nil)
	if err != nil {
		t.Fatalf("execute shadow turn: %v", err)
	}
	if string(result.DTO) != `{"message":"legacy"}` || legacy.calls != 1 || shadow.calls != 1 {
		t.Fatalf("shadow must preserve the one legacy write result, result=%s legacy=%d shadow=%d", result.DTO, legacy.calls, shadow.calls)
	}
	if shadow.last.AllowWrites {
		t.Fatal("shadow evaluation must never allow writes")
	}
	if recorder.calls != 1 || recorder.last.Status != cutover.ComparisonDiverged || recorder.last.LegacyDTOHash == recorder.last.TypedDTOHash {
		t.Fatalf("expected persisted DTO/event reconciliation, got %#v", recorder.last)
	}
}

// mustRouter 执行该函数负责的核心处理逻辑。
func mustRouter(t *testing.T, mode cutover.Mode) *cutover.Router {
	t.Helper()
	router, err := cutover.NewRouter(cutover.RouterConfig{Mode: mode, WorkspaceIDs: []string{workspaceID}, ResourceIDs: []string{resourceID}})
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	return router
}

// mustPipeline 执行该函数负责的核心处理逻辑。
func mustPipeline(t *testing.T, mode cutover.Mode, legacy cutover.Runner, durable cutover.Runner, shadow cutover.ShadowEvaluator, recorder cutover.ComparisonRecorder) *cutover.Pipeline {
	t.Helper()
	pipeline, err := cutover.NewPipeline(mustRouter(t, mode), legacy, durable, shadow, recorder)
	if err != nil {
		t.Fatalf("new pipeline: %v", err)
	}
	return pipeline
}

// validRequest 执行该函数负责的核心处理逻辑。
func validRequest() cutover.Request {
	return cutover.Request{
		RequestID: "request-1", SessionID: "session-1", Message: "revise the document",
		WorkspaceID: workspaceID, ResourceID: resourceID, Scope: trustedScope(),
	}
}

// trustedScope 执行该函数负责的核心处理逻辑。
func trustedScope() identity.WorkspaceScope {
	return identity.WorkspaceScope{
		Principal:   identity.Principal{Type: "user", ID: "44444444-4444-4444-8444-444444444444", OrganizationID: "55555555-5555-4555-8555-555555555555"},
		WorkspaceID: workspaceID, TrustSource: "edge-hmac-v1", Trusted: true, IssuedAt: time.Now().UTC(),
	}
}

type fakeRunner struct {
	calls  int
	result cutover.Result
	err    error
}

// Execute 执行该函数负责的核心处理逻辑。
func (runner *fakeRunner) Execute(_ context.Context, _ cutover.Request, observe cutover.Observer) (cutover.Result, error) {
	runner.calls++
	if runner.err != nil {
		return cutover.Result{}, runner.err
	}
	for _, event := range runner.result.Events {
		if observe != nil {
			if err := observe(event); err != nil {
				return cutover.Result{}, err
			}
		}
	}
	return runner.result, nil
}

type fakeShadow struct {
	calls  int
	last   cutover.ShadowRequest
	result cutover.Result
}

// 评估执行该函数负责的核心处理逻辑。
func (shadow *fakeShadow) Evaluate(_ context.Context, request cutover.ShadowRequest) (cutover.Result, error) {
	shadow.calls++
	shadow.last = request
	return shadow.result, nil
}

type fakeRecorder struct {
	calls int
	last  cutover.Comparison
}

// 记录按领域约束持久化数据。
func (recorder *fakeRecorder) Record(_ context.Context, comparison cutover.Comparison) error {
	recorder.calls++
	recorder.last = comparison
	return nil
}

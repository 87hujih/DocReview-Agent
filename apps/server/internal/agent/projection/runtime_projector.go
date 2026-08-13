// Package 投影 turns 持久化的运行 facts into rebuildable 轮次/公开的
// facts. It never executes models、工具、approvals、或 commits.
package projection

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	agentturn "agent_project/apps/server/internal/agent/turn"
	"agent_project/apps/server/internal/storage/postgres/outbox"
)

const runtimeProjectionName = "agent-turn-runtime-v1"

var ErrUnsupportedProjectionEvent = errors.New("不支持的运行时投影事件")

type RuntimeSnapshot struct {
	TurnID     string
	RunID      string
	RunStatus  string
	StepType   string
	OutputJSON json.RawMessage
	ErrorJSON  json.RawMessage
}

type RuntimeSnapshotReader interface {
	Load(ctx context.Context, event outbox.Event) (RuntimeSnapshot, error)
}

type OutcomeCommitter interface {
	CommitOutcome(ctx context.Context, outcome agentturn.Outcome) (agentturn.OutcomeResult, error)
}

type ReceiptStore interface {
	Exists(ctx context.Context, eventID, projectionName string) (bool, error)
	Record(ctx context.Context, eventID, projectionName, payloadHash string) error
}

type RuntimeProjector struct {
	reader    RuntimeSnapshotReader
	committer OutcomeCommitter
	receipts  ReceiptStore
}

// NewRuntimeProjector 校验依赖并创建对应实例。
func NewRuntimeProjector(reader RuntimeSnapshotReader, committer OutcomeCommitter, receipts ReceiptStore) (*RuntimeProjector, error) {
	if reader == nil || committer == nil || receipts == nil {
		return nil, fmt.Errorf("运行时投影读取器、结果提交器、和回执存储不能为空")
	}
	return &RuntimeProjector{reader: reader, committer: committer, receipts: receipts}, nil
}

// 投影执行该函数负责的核心处理逻辑。
func (projector *RuntimeProjector) Project(ctx context.Context, event outbox.Event) error {
	if event.EventType != "agent.step.outcome_committed" && event.EventType != "agent.tool_approval.rejected" {
		return fmt.Errorf("%w：%s", ErrUnsupportedProjectionEvent, event.EventType)
	}
	seen, err := projector.receipts.Exists(ctx, event.ID, runtimeProjectionName)
	if err != nil {
		return fmt.Errorf("查询运行时投影回执：%w", err)
	}
	if seen {
		return nil
	}
	snapshot, err := projector.reader.Load(ctx, event)
	if err != nil {
		return fmt.Errorf("加载运行时投影快照：%w", err)
	}
	outcome, err := outcomeFromSnapshot(event, snapshot)
	if err != nil {
		return err
	}
	if _, err := projector.committer.CommitOutcome(ctx, outcome); err != nil {
		return fmt.Errorf("commit 已投影的轮次结果：%w", err)
	}
	hash, err := eventHash(event)
	if err != nil {
		return err
	}
	if err := projector.receipts.Record(ctx, event.ID, runtimeProjectionName, hash); err != nil {
		return fmt.Errorf("记录运行时投影回执：%w", err)
	}
	return nil
}

// outcomeFromSnapshot 执行该函数负责的核心处理逻辑。
func outcomeFromSnapshot(event outbox.Event, snapshot RuntimeSnapshot) (agentturn.Outcome, error) {
	snapshot.TurnID = strings.TrimSpace(snapshot.TurnID)
	snapshot.RunID = strings.TrimSpace(snapshot.RunID)
	snapshot.StepType = strings.TrimSpace(snapshot.StepType)
	if snapshot.TurnID == "" || snapshot.RunID == "" {
		return agentturn.Outcome{}, fmt.Errorf("运行时投影快照需要轮次和运行标识列表")
	}
	status, err := turnStatus(snapshot.RunStatus)
	if err != nil {
		return agentturn.Outcome{}, err
	}
	output := snapshot.OutputJSON
	if len(output) == 0 {
		output = json.RawMessage(`{}`)
	}
	outcome := agentturn.Outcome{
		TurnID: snapshot.TurnID, IdempotencyKey: "outbox-projection:" + event.ID,
		Status: status, OutputJSON: output, ErrorJSON: snapshot.ErrorJSON,
	}
	if status == agentturn.StatusSucceeded && snapshot.StepType == "RenderOutcome" {
		var rendered struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(output, &rendered); err != nil || strings.TrimSpace(rendered.Message) == "" {
			return agentturn.Outcome{}, fmt.Errorf("RenderOutcome 投影需要一个类型化的消息")
		}
		payload, _ := json.Marshal(map[string]string{"content": strings.TrimSpace(rendered.Message)})
		outcome.Messages = []agentturn.Message{{Role: "assistant", Kind: "text", Payload: payload}}
	}
	return outcome, nil
}

// turnStatus 执行该函数负责的核心处理逻辑。
func turnStatus(runStatus string) (agentturn.Status, error) {
	// 根据当前状态或类型选择对应的处理分支。
	switch strings.TrimSpace(runStatus) {
	case "queued", "running":
		return agentturn.StatusRunning, nil
	case "waiting_input":
		return agentturn.StatusWaitingInput, nil
	case "waiting_approval":
		return agentturn.StatusWaitingApproval, nil
	case "succeeded":
		return agentturn.StatusSucceeded, nil
	case "failed":
		return agentturn.StatusFailed, nil
	case "cancelled":
		return agentturn.StatusCancelled, nil
	default:
		return "", fmt.Errorf("运行时投影具有无效的运行状态 %q", runStatus)
	}
}

// eventHash 执行该函数负责的核心处理逻辑。
func eventHash(event outbox.Event) (string, error) {
	var payload any
	if err := json.Unmarshal(event.PayloadJSON, &payload); err != nil {
		return "", fmt.Errorf("投影事件负载无效：%w", err)
	}
	canonical, err := json.Marshal(struct {
		ID            string `json:"id"`
		AggregateType string `json:"aggregate_type"`
		AggregateID   string `json:"aggregate_id"`
		EventType     string `json:"event_type"`
		Payload       any    `json:"payload"`
	}{event.ID, event.AggregateType, event.AggregateID, event.EventType, payload})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return fmt.Sprintf("sha256:%x", digest), nil
}

var _ outbox.Projector = (*RuntimeProjector)(nil)

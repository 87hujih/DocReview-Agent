package cutover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	agentturn "agent_project/apps/server/internal/agent/turn"
)

var ErrTurnNotDeterministic = errors.New("持久化的轮次未进入可确定恢复的状态")

type DurableRunnerConfig struct {
	PollInterval time.Duration
	MaxWait      time.Duration
}

type TurnCoordinator interface {
	Submit(ctx context.Context, request agentturn.Request) (agentturn.Result, error)
	Stream(ctx context.Context, request agentturn.Request, afterSequence int, observe func(agentturn.Event) error) error
}

type PublicProjector interface {
	Project(ctx context.Context, turn agentturn.Turn, status agentturn.Status, events []agentturn.Event) (json.RawMessage, error)
}

type DurableRunner struct {
	cfg         DurableRunnerConfig
	coordinator TurnCoordinator
	projector   PublicProjector
}

// NewDurableRunner 校验依赖并创建对应实例。
func NewDurableRunner(cfg DurableRunnerConfig, coordinator TurnCoordinator, projector PublicProjector) (*DurableRunner, error) {
	if cfg.PollInterval <= 0 || cfg.MaxWait <= 0 || coordinator == nil || projector == nil {
		return nil, fmt.Errorf("持久化的运行器必须为正数轮询边界、协调器、和投影器")
	}
	return &DurableRunner{cfg: cfg, coordinator: coordinator, projector: projector}, nil
}

// Execute 执行该函数负责的核心处理逻辑。
func (runner *DurableRunner) Execute(ctx context.Context, request Request, observe Observer) (Result, error) {
	if runner == nil || runner.coordinator == nil || !trustedFor(request.Scope, strings.TrimSpace(request.WorkspaceID)) {
		return Result{}, ErrUntrustedDurableScope
	}
	turnRequest := agentturn.Request{
		RequestID: request.RequestID, TraceID: request.TraceID,
		OrganizationID: request.Scope.Principal.OrganizationID, WorkspaceID: request.WorkspaceID,
		ResourceID: request.ResourceID, SessionID: request.SessionID, Message: request.Message,
		PrincipalType: request.Scope.Principal.Type, PrincipalID: request.Scope.Principal.ID,
		TrustSource: request.Scope.TrustSource, RuntimeMode: string(ModeDurable),
	}
	accepted, err := runner.coordinator.Submit(ctx, turnRequest)
	if err != nil {
		return Result{}, err
	}
	events := indexEvents(accepted.Events)
	cursor := maxSequence(events)
	if err := observeAfter(events, request.AfterSequence, observe); err != nil {
		return Result{}, err
	}
	if status, ok := deterministicStatus(events); ok {
		return runner.project(ctx, accepted.Turn, status, events)
	}

	deadline := time.NewTimer(runner.cfg.MaxWait)
	defer deadline.Stop()
	for {
		var observed []agentturn.Event
		err := runner.coordinator.Stream(ctx, turnRequest, cursor, func(event agentturn.Event) error {
			observed = append(observed, event)
			if event.Sequence > request.AfterSequence && observe != nil {
				return observe(toEvent(event))
			}
			return nil
		})
		if err != nil {
			return Result{}, err
		}
		for _, event := range observed {
			events[event.Sequence] = event
			if event.Sequence > cursor {
				cursor = event.Sequence
			}
		}
		if status, ok := deterministicStatus(events); ok {
			return runner.project(ctx, accepted.Turn, status, events)
		}

		poll := time.NewTimer(runner.cfg.PollInterval)
		// 等待并发事件、取消信号或超时结果。
		select {
		case <-ctx.Done():
			if !poll.Stop() {
				<-poll.C
			}
			return Result{}, ctx.Err()
		case <-deadline.C:
			if !poll.Stop() {
				<-poll.C
			}
			return Result{}, ErrTurnNotDeterministic
		case <-poll.C:
		}
	}
}

// 投影执行该函数负责的核心处理逻辑。
func (runner *DurableRunner) project(ctx context.Context, turn agentturn.Turn, status agentturn.Status, indexed map[int]agentturn.Event) (Result, error) {
	events := orderedEvents(indexed)
	dto, err := runner.projector.Project(ctx, turn, status, events)
	if err != nil {
		return Result{}, fmt.Errorf("投影持久化的公开的 DTO：%w", err)
	}
	publicEvents := make([]Event, 0, len(events))
	for _, event := range events {
		publicEvents = append(publicEvents, toEvent(event))
	}
	return Result{Mode: ModeDurable, DTO: dto, Events: publicEvents}, nil
}

// indexEvents 执行该函数负责的核心处理逻辑。
func indexEvents(events []agentturn.Event) map[int]agentturn.Event {
	indexed := make(map[int]agentturn.Event, len(events))
	for _, event := range events {
		if event.Sequence > 0 {
			indexed[event.Sequence] = event
		}
	}
	return indexed
}

// orderedEvents 执行该函数负责的核心处理逻辑。
func orderedEvents(indexed map[int]agentturn.Event) []agentturn.Event {
	sequences := make([]int, 0, len(indexed))
	for sequence := range indexed {
		sequences = append(sequences, sequence)
	}
	sort.Ints(sequences)
	result := make([]agentturn.Event, 0, len(sequences))
	for _, sequence := range sequences {
		result = append(result, indexed[sequence])
	}
	return result
}

// maxSequence 执行该函数负责的核心处理逻辑。
func maxSequence(indexed map[int]agentturn.Event) int {
	maximum := 0
	for sequence := range indexed {
		if sequence > maximum {
			maximum = sequence
		}
	}
	return maximum
}

// observeAfter 执行该函数负责的核心处理逻辑。
func observeAfter(indexed map[int]agentturn.Event, after int, observe Observer) error {
	if observe == nil {
		return nil
	}
	for _, event := range orderedEvents(indexed) {
		if event.Sequence > after {
			if err := observe(toEvent(event)); err != nil {
				return err
			}
		}
	}
	return nil
}

// deterministicStatus 执行该函数负责的核心处理逻辑。
func deterministicStatus(indexed map[int]agentturn.Event) (agentturn.Status, bool) {
	for index := len(orderedEvents(indexed)) - 1; index >= 0; index-- {
		event := orderedEvents(indexed)[index]
		if !strings.HasPrefix(event.Type, "turn.") {
			continue
		}
		status := agentturn.Status(strings.TrimPrefix(event.Type, "turn."))
		// 根据当前状态或类型选择对应的处理分支。
		switch status {
		case agentturn.StatusWaitingInput, agentturn.StatusWaitingApproval,
			agentturn.StatusSucceeded, agentturn.StatusFailed, agentturn.StatusCancelled:
			return status, true
		}
	}
	return "", false
}

// toEvent 执行该函数负责的核心处理逻辑。
func toEvent(event agentturn.Event) Event {
	return Event{Sequence: event.Sequence, Type: event.Type, Payload: append(json.RawMessage(nil), event.Payload...)}
}

var _ Runner = (*DurableRunner)(nil)

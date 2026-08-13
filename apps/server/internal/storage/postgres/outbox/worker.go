package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type ProjectionStore interface {
	RecoverExpiredLeases(ctx context.Context, now time.Time) (int64, error)
	Claim(ctx context.Context, params ClaimParams) ([]Event, error)
	MarkPublished(ctx context.Context, params PublishParams) error
	ScheduleRetry(ctx context.Context, params RetryParams) error
}

// 投影器 implementations 必须 treat 事件.ID as their idempotency 键. 一个
// 租约 can expire 之后 the side effect 和 之前 MarkPublished, so replay 为
// 处理失败： part of the normal contract rather 于 一个 exceptional path.
type Projector interface {
	Project(ctx context.Context, event Event) error
}

type ProjectionWorkerConfig struct {
	WorkerID         string
	LeaseDuration    time.Duration
	PollInterval     time.Duration
	ErrorBackoff     time.Duration
	RecoveryInterval time.Duration
	BatchSize        int
	MaxAttempts      int
	RetryBase        time.Duration
	RetryMax         time.Duration
	EventTypes       []string
}

type WorkerClock interface {
	Now() time.Time
}

type projectionSystemClock struct{}

// Now 执行该函数负责的核心处理逻辑。
func (projectionSystemClock) Now() time.Time { return time.Now().UTC() }

type ProjectionWorker struct {
	cfg       ProjectionWorkerConfig
	store     ProjectionStore
	projector Projector
	clock     WorkerClock
}

// NewProjectionWorker 校验依赖并创建对应实例。
func NewProjectionWorker(cfg ProjectionWorkerConfig, store ProjectionStore, projector Projector, clock WorkerClock) (*ProjectionWorker, error) {
	cfg.WorkerID = strings.TrimSpace(cfg.WorkerID)
	if cfg.WorkerID == "" || cfg.LeaseDuration <= 0 || cfg.PollInterval <= 0 || cfg.ErrorBackoff <= 0 ||
		cfg.RecoveryInterval <= 0 || cfg.BatchSize <= 0 || cfg.BatchSize > 1000 || cfg.MaxAttempts <= 0 ||
		cfg.RetryBase <= 0 || cfg.RetryMax < cfg.RetryBase || store == nil || projector == nil {
		return nil, fmt.Errorf("投影工作进程配置和依赖无效")
	}
	if clock == nil {
		clock = projectionSystemClock{}
	}
	for _, eventType := range cfg.EventTypes {
		if strings.TrimSpace(eventType) == "" {
			return nil, fmt.Errorf("投影工作进程事件 types 不能为空")
		}
	}
	return &ProjectionWorker{cfg: cfg, store: store, projector: projector, clock: clock}, nil
}

// 运行 执行该函数负责的核心处理逻辑。
func (worker *ProjectionWorker) Run(ctx context.Context) error {
	if _, err := worker.store.RecoverExpiredLeases(ctx, worker.clock.Now()); err != nil {
		return fmt.Errorf("recover 已过期 outbox 租约s：%w", err)
	}
	nextRecovery := worker.clock.Now().Add(worker.cfg.RecoveryInterval)
	for {
		if !worker.clock.Now().Before(nextRecovery) {
			if _, err := worker.store.RecoverExpiredLeases(ctx, worker.clock.Now()); err != nil {
				if waitErr := waitProjection(ctx, worker.cfg.ErrorBackoff); waitErr != nil {
					return waitErr
				}
				continue
			}
			nextRecovery = worker.clock.Now().Add(worker.cfg.RecoveryInterval)
		}
		worked, err := worker.ProcessOne(ctx)
		if err != nil {
			if waitErr := waitProjection(ctx, worker.cfg.ErrorBackoff); waitErr != nil {
				return waitErr
			}
			continue
		}
		if !worked {
			if err := waitProjection(ctx, worker.cfg.PollInterval); err != nil {
				return err
			}
		}
	}
}

// ProcessOne 执行该函数负责的核心处理逻辑。
func (worker *ProjectionWorker) ProcessOne(ctx context.Context) (bool, error) {
	now := worker.clock.Now()
	events, err := worker.store.Claim(ctx, ClaimParams{
		Now: now, WorkerID: worker.cfg.WorkerID, LeaseDuration: worker.cfg.LeaseDuration,
		Limit: worker.cfg.BatchSize, EventTypes: worker.cfg.EventTypes,
	})
	if err != nil {
		return false, fmt.Errorf("处理失败：声明 outbox projections：%w", err)
	}
	if len(events) == 0 {
		return false, nil
	}
	for _, event := range events {
		if err := worker.projector.Project(ctx, event); err != nil {
			if scheduleErr := worker.scheduleFailure(ctx, event, err); scheduleErr != nil {
				return true, scheduleErr
			}
			continue
		}
		if err := worker.store.MarkPublished(ctx, PublishParams{
			EventID: event.ID, WorkerID: worker.cfg.WorkerID,
			LeaseGeneration: event.LeaseGeneration, PublishedAt: worker.clock.Now(),
		}); err != nil {
			return true, fmt.Errorf("处理失败：mark outbox 投影 published：%w", err)
		}
	}
	return true, nil
}

// scheduleFailure 执行该函数负责的核心处理逻辑。
func (worker *ProjectionWorker) scheduleFailure(ctx context.Context, event Event, cause error) error {
	now := worker.clock.Now()
	deadLetter := event.AttemptCount >= worker.cfg.MaxAttempts
	errorJSON, _ := json.Marshal(map[string]any{
		"category": "projection_failed", "message": cause.Error(), "retryable": !deadLetter,
	})
	next := now
	if !deadLetter {
		next = now.Add(projectionBackoff(worker.cfg.RetryBase, worker.cfg.RetryMax, event.AttemptCount))
	}
	err := worker.store.ScheduleRetry(ctx, RetryParams{
		EventID: event.ID, WorkerID: worker.cfg.WorkerID, LeaseGeneration: event.LeaseGeneration,
		ErrorJSON: errorJSON, NextAttemptAt: next, Now: now, DeadLetter: deadLetter,
	})
	if err != nil {
		return fmt.Errorf("schedule outbox 投影失败：%w", err)
	}
	return nil
}

// projectionBackoff 执行该函数负责的核心处理逻辑。
func projectionBackoff(base, maximum time.Duration, attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := base
	for count := 1; count < attempt && delay < maximum; count++ {
		if delay > maximum/2 {
			return maximum
		}
		delay *= 2
	}
	if delay > maximum {
		return maximum
	}
	return delay
}

// waitProjection 执行该函数负责的核心处理逻辑。
func waitProjection(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	// 等待并发事件、取消信号或超时结果。
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

var _ ProjectionStore = (*Repository)(nil)

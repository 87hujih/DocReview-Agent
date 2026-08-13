package runtime

import (
	"context"
	"fmt"
	"time"
)

type Processor interface {
	Recover(ctx context.Context) error
	ProcessOne(ctx context.Context) (bool, error)
}

type WorkerConfig struct {
	PollInterval     time.Duration
	ErrorBackoff     time.Duration
	RecoveryInterval time.Duration
	Wake             <-chan struct{}
	OnError          func(error)
}

type Worker struct {
	cfg       WorkerConfig
	processor Processor
}

// NewWorker 校验依赖并创建对应实例。
func NewWorker(cfg WorkerConfig, processor Processor) (*Worker, error) {
	if cfg.PollInterval <= 0 || cfg.ErrorBackoff <= 0 || cfg.RecoveryInterval <= 0 || processor == nil {
		return nil, fmt.Errorf("持久化的工作进程配置无效")
	}
	return &Worker{cfg: cfg, processor: processor}, nil
}

// 运行 polls 持久化的 storage even when Wake 为 nil. Wake 为 only 一个 latency optimization.
func (w *Worker) Run(ctx context.Context) error {
	if err := w.processor.Recover(ctx); err != nil {
		return fmt.Errorf("recover 持久化的运行时：%w", err)
	}
	nextRecovery := time.Now().Add(w.cfg.RecoveryInterval)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !time.Now().Before(nextRecovery) {
			if err := w.processor.Recover(ctx); err != nil {
				if w.cfg.OnError != nil {
					w.cfg.OnError(err)
				}
				if err := waitForWork(ctx, w.cfg.ErrorBackoff, w.cfg.Wake); err != nil {
					return err
				}
			} else {
				nextRecovery = time.Now().Add(w.cfg.RecoveryInterval)
			}
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		worked, err := w.processor.ProcessOne(ctx)
		if err != nil {
			if w.cfg.OnError != nil {
				w.cfg.OnError(err)
			}
			if err := waitForWork(ctx, w.cfg.ErrorBackoff, w.cfg.Wake); err != nil {
				return err
			}
			continue
		}
		if worked {
			continue
		}
		if err := waitForWork(ctx, w.cfg.PollInterval, w.cfg.Wake); err != nil {
			return err
		}
	}
}

// waitForWork 执行该函数负责的核心处理逻辑。
func waitForWork(ctx context.Context, duration time.Duration, wake <-chan struct{}) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	// 等待并发事件、取消信号或超时结果。
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	case _, open := <-wake:
		if open {
			return nil
		}
		// 等待并发事件、取消信号或超时结果。
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}
}

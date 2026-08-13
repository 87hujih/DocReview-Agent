package runtime

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestWorkerRecoversThenPollsWithoutWakeChannel 验证对应场景下的正常路径与失败路径。
func TestWorkerRecoversThenPollsWithoutWakeChannel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	processor := &fakeProcessor{cancel: cancel, cancelAfterProcesses: 1}
	worker, err := NewWorker(WorkerConfig{PollInterval: time.Millisecond, ErrorBackoff: time.Millisecond, RecoveryInterval: time.Hour}, processor)
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}
	err = worker.Run(ctx)
	if err != context.Canceled {
		t.Fatalf("expected cancellation, got %v", err)
	}
	processor.mu.Lock()
	defer processor.mu.Unlock()
	if !processor.recovered || processor.processCalls == 0 {
		t.Fatalf("worker must recover before polling: %#v", processor)
	}
}

// TestWorkerPeriodicallyRecoversExpiredLeasesWithoutRestart 验证对应场景下的正常路径与失败路径。
func TestWorkerPeriodicallyRecoversExpiredLeasesWithoutRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	processor := &fakeProcessor{cancelAfterRecoveries: 2, cancel: cancel}
	worker, err := NewWorker(WorkerConfig{
		PollInterval: time.Millisecond, ErrorBackoff: time.Millisecond, RecoveryInterval: time.Nanosecond,
	}, processor)
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}
	if err := worker.Run(ctx); err != context.Canceled {
		t.Fatalf("expected cancellation after periodic recovery, got %v", err)
	}
	processor.mu.Lock()
	defer processor.mu.Unlock()
	if processor.recoverCalls < 2 {
		t.Fatalf("expected startup and periodic recovery, got %d calls", processor.recoverCalls)
	}
}

// TestWaitForWorkTreatsClosedWakeChannelAsOptionalHint 验证对应场景下的正常路径与失败路径。
func TestWaitForWorkTreatsClosedWakeChannelAsOptionalHint(t *testing.T) {
	wake := make(chan struct{})
	close(wake)
	started := time.Now()
	if err := waitForWork(context.Background(), 5*time.Millisecond, wake); err != nil {
		t.Fatalf("wait for work: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 4*time.Millisecond {
		t.Fatalf("closed optional wake channel caused busy polling: elapsed=%v", elapsed)
	}
}

type fakeProcessor struct {
	mu                    sync.Mutex
	recovered             bool
	recoverCalls          int
	cancelAfterRecoveries int
	processCalls          int
	cancelAfterProcesses  int
	cancel                context.CancelFunc
}

// Recover 执行该函数负责的核心处理逻辑。
func (p *fakeProcessor) Recover(context.Context) error {
	p.mu.Lock()
	p.recovered = true
	p.recoverCalls++
	if p.cancelAfterRecoveries > 0 && p.recoverCalls >= p.cancelAfterRecoveries {
		p.cancel()
	}
	p.mu.Unlock()
	return nil
}

// ProcessOne 执行该函数负责的核心处理逻辑。
func (p *fakeProcessor) ProcessOne(context.Context) (bool, error) {
	p.mu.Lock()
	p.processCalls++
	shouldCancel := p.cancelAfterProcesses > 0 && p.processCalls >= p.cancelAfterProcesses
	p.mu.Unlock()
	if shouldCancel {
		p.cancel()
	}
	return false, nil
}

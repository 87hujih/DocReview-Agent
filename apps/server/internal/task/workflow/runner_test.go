package workflow

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- 假实现 ---

// fakeExec 作为Exec的测试替身，用于在用例里提供可控的依赖行为。
type fakeExec struct {
	fn func(ctx context.Context, taskID string)
}

// Execute 实现测试替身需要的 `Execute` 接口方法，为用例分支提供可控返回。
func (f *fakeExec) Execute(ctx context.Context, taskID string) {
	if f.fn != nil {
		f.fn(ctx, taskID)
	}
}

// fakeFailRec 作为FailRec的测试替身，用于在用例里提供可控的依赖行为。
type fakeFailRec struct {
	mu      sync.Mutex
	records []string
}

// Fail 实现测试替身需要的 `Fail` 接口方法，为用例分支提供可控返回。
func (f *fakeFailRec) Fail(_ context.Context, taskID string, _ string) {
	f.mu.Lock()
	f.records = append(f.records, taskID)
	f.mu.Unlock()
}

// count 实现测试替身需要的 `count` 接口方法，为用例分支提供可控返回。
func (f *fakeFailRec) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.records)
}

// --- 测试 ---

// TestRunnerEnqueueQueueFull 验证`runnerEnqueueQueueFull`在特定边界条件下的行为，防止同类回归。
func TestRunnerEnqueueQueueFull(t *testing.T) {
	// 1 个 worker、队列容量 1；堵塞 worker 后队列满，第三次入队应返回 ErrQueueFull
	gate := make(chan struct{})
	started := make(chan struct{}, 1)

	exec := &fakeExec{fn: func(_ context.Context, _ string) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-gate
	}}

	runner := newWorkflowRunner(1, 1, 0, exec, nil)
	runner.Start()
	defer func() {
		close(gate)
		runner.Stop()
	}()

	// 先入队一个任务，等待 worker 占住执行位
	if err := runner.Enqueue("task-blocking"); err != nil {
		t.Fatalf("首次入队期望成功，实际得到 %v", err)
	}
	<-started // 确保 worker 正在执行，不在消费队列

	// 填满队列（容量 1）
	if err := runner.Enqueue("task-fill"); err != nil {
		t.Fatalf("填充队列期望成功，实际得到 %v", err)
	}

	// 第三次入队应报队列已满
	if err := runner.Enqueue("task-overflow"); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("队列已满时期望 ErrQueueFull，实际得到 %v", err)
	}
}

// TestRunnerConcurrentWorkerLimit 验证`runnerConcurrentWorkerLimit`在特定边界条件下的行为，防止同类回归。
func TestRunnerConcurrentWorkerLimit(t *testing.T) {
	// 2 个 worker；投递 2 个阻塞任务，验证并发数恰好为 2
	const workerCount = 2
	gate := make(chan struct{})
	started := make(chan struct{}, workerCount)
	var active atomic.Int32

	exec := &fakeExec{fn: func(_ context.Context, _ string) {
		active.Add(1)
		started <- struct{}{}
		<-gate
		active.Add(-1)
	}}

	runner := newWorkflowRunner(workerCount, 10, 0, exec, nil)
	runner.Start()

	for i := 0; i < workerCount; i++ {
		if err := runner.Enqueue("t"); err != nil {
			t.Fatalf("入队 %d 失败：%v", i, err)
		}
	}

	// 等待所有 worker 都到达 gate
	for i := 0; i < workerCount; i++ {
		<-started
	}

	if got := active.Load(); got != int32(workerCount) {
		t.Fatalf("期望并发 %d，实际 %d", workerCount, got)
	}

	close(gate)
	runner.Stop()
}

// TestRunnerPanicRecovery 验证`runnerPanicRecovery`在特定边界条件下的行为，防止同类回归。
func TestRunnerPanicRecovery(t *testing.T) {
	// 第一个任务 panic；验证：failer 被调用 1 次、第二个任务仍正常执行
	failer := &fakeFailRec{}
	panicStarted := make(chan struct{})
	normalDone := make(chan struct{})

	exec := &fakeExec{fn: func(_ context.Context, taskID string) {
		switch taskID {
		case "panic-task":
			close(panicStarted)
			panic("模拟崩溃")
		case "normal-task":
			close(normalDone)
		}
	}}

	runner := newWorkflowRunner(1, 10, 0, exec, failer)
	runner.Start()

	if err := runner.Enqueue("panic-task"); err != nil {
		t.Fatalf("入队 panic-task 失败：%v", err)
	}
	<-panicStarted
	time.Sleep(5 * time.Millisecond) // 等待 recover 逻辑执行完毕

	if err := runner.Enqueue("normal-task"); err != nil {
		t.Fatalf("panic 后入队 normal-task 失败：%v", err)
	}
	<-normalDone

	runner.Stop()

	if failer.count() != 1 {
		t.Fatalf("期望 failer 被调用 1 次，实际 %d 次", failer.count())
	}
}

// TestRunnerStopRejectsEnqueue 验证`runnerStop`在非法输入或失败路径下的行为，防止同类回归。
func TestRunnerStopRejectsEnqueue(t *testing.T) {
	runner := newWorkflowRunner(1, 10, 0, &fakeExec{}, nil)
	runner.Start()
	runner.Stop()

	if err := runner.Enqueue("task-after-stop"); !errors.Is(err, ErrRunnerStopped) {
		t.Fatalf("停止后入队期望 ErrRunnerStopped，实际得到 %v", err)
	}
}

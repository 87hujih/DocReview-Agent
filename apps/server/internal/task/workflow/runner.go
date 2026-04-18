package workflow

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"agent_project/apps/server/internal/storage/postgres"
)

var (
	// ErrQueueFull 表示任务队列已达容量上限，无法继续投递。
	ErrQueueFull = errors.New("任务队列已满")
	// ErrRunnerStopped 表示执行器已停止，拒绝新任务入队。
	ErrRunnerStopped = errors.New("工作流执行器已停止")
)

// taskExecutor 抽象单个任务的编排执行能力（便于单元测试注入假实现）。
type taskExecutor interface {
	Execute(ctx context.Context, taskID string)
}

// taskFailer 抽象将任务标记为失败的能力（用于 panic 恢复后写库）。
type taskFailer interface {
	Fail(ctx context.Context, taskID string, msg string)
}

// WorkflowRunner 以有限工作池并发执行任务编排，支持恐慌恢复与优雅停止。
type WorkflowRunner struct {
	workers   int
	queue     chan string
	execute   taskExecutor
	fail      taskFailer
	timeout   time.Duration
	wg        sync.WaitGroup
	mu        sync.Mutex
	isStopped bool
}

// newWorkflowRunner 构造 WorkflowRunner（包内测试使用）。
func newWorkflowRunner(workers, queueSize int, timeout time.Duration, execute taskExecutor, fail taskFailer) *WorkflowRunner {
	return &WorkflowRunner{
		workers: workers,
		queue:   make(chan string, queueSize),
		execute: execute,
		fail:    fail,
		timeout: timeout,
	}
}

// orchestratorExec 将 Orchestrator + TaskRepo 适配为 taskExecutor（生产使用）。
type orchestratorExec struct {
	orch     *Orchestrator
	taskRepo *postgres.TaskRepo
}

// Execute 执行 `Execute`，统一流程推进和副作用控制。
func (e *orchestratorExec) Execute(ctx context.Context, taskID string) {
	task, err := e.taskRepo.GetByID(ctx, taskID)
	if err != nil || task == nil {
		return
	}
	e.orch.Orchestrate(ctx, task)
}

// taskRepoFail 将 TaskRepo 适配为 taskFailer（生产使用）。
type taskRepoFail struct {
	repo *postgres.TaskRepo
}

// Fail 把 `Fail` 标记为失败，并补齐对应清理或通知逻辑。
func (f *taskRepoFail) Fail(ctx context.Context, taskID string, msg string) {
	_ = f.repo.UpdateStatus(ctx, taskID, "failed", &msg)
}

// NewOrchestratorRunner 构造连接编排器的 WorkflowRunner（服务启动时使用）。
// workers 控制并发数，queueSize 控制缓冲队列容量，timeout 为单任务超时（≤0 不设超时）。
func NewOrchestratorRunner(workers, queueSize int, timeout time.Duration, orch *Orchestrator, taskRepo *postgres.TaskRepo) *WorkflowRunner {
	exec := &orchestratorExec{orch: orch, taskRepo: taskRepo}
	fail := &taskRepoFail{repo: taskRepo}
	return newWorkflowRunner(workers, queueSize, timeout, exec, fail)
}

// Start 启动工作协程，应在服务启动时调用一次。
func (r *WorkflowRunner) Start() {
	for i := 0; i < r.workers; i++ {
		r.wg.Add(1)
		go r.runWorker()
	}
}

// Stop 停止接收新任务并等待所有工作协程退出。
func (r *WorkflowRunner) Stop() {
	r.mu.Lock()
	if r.isStopped {
		r.mu.Unlock()
		return
	}
	r.isStopped = true
	close(r.queue)
	r.mu.Unlock()
	r.wg.Wait()
}

// Enqueue 向队列投递一个任务 ID。
// 若队列已满返回 ErrQueueFull，若执行器已停止返回 ErrRunnerStopped。
func (r *WorkflowRunner) Enqueue(taskID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.isStopped {
		return ErrRunnerStopped
	}

	select {
	case r.queue <- taskID:
		return nil
	default:
		return ErrQueueFull
	}
}

// runWorker 执行 `Worker`，统一流程推进和副作用控制。
func (r *WorkflowRunner) runWorker() {
	defer r.wg.Done()
	for taskID := range r.queue {
		r.runTask(taskID)
	}
}

// runTask 执行 `任务`，统一流程推进和副作用控制。
func (r *WorkflowRunner) runTask(taskID string) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("编排器恐慌恢复：task=%s panic=%v", taskID, rec)
			if r.fail != nil {
				r.fail.Fail(context.Background(), taskID, fmt.Sprintf("编排器发生 panic：%v", rec))
			}
		}
	}()

	ctx := context.Background()
	if r.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.timeout)
		defer cancel()
	}

	r.execute.Execute(ctx, taskID)
}

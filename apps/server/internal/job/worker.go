package job

import (
	"context"
	"errors"
	"log"

	executoragent "agent_project/apps/server/internal/agent/executor"
	"agent_project/apps/server/internal/storage/postgres"
	"agent_project/apps/server/internal/task/models"
)

var errTaskNotFound = errors.New("task not found")

// Worker 负责消费执行作业并推动任务完成。
type Worker struct {
	jobCh    chan postgres.ExecutionJob
	jobRepo  *postgres.JobRepo
	executor *executoragent.Executor
	taskRepo *postgres.TaskRepo
}

// New 构造 channel-based worker。
func New(
	jobRepo *postgres.JobRepo,
	executor *executoragent.Executor,
	taskRepo *postgres.TaskRepo,
	bufSize int,
) *Worker {
	if bufSize <= 0 {
		bufSize = 1
	}

	return &Worker{
		jobCh:    make(chan postgres.ExecutionJob, bufSize),
		jobRepo:  jobRepo,
		executor: executor,
		taskRepo: taskRepo,
	}
}

// JobCh 返回用于投递执行信号的发送端。
func (w *Worker) JobCh() chan<- postgres.ExecutionJob {
	return w.jobCh
}

// Start 启动 n 个 worker goroutine。
func (w *Worker) Start(ctx context.Context, n int) {
	if n <= 0 {
		n = 1
	}

	for workerIndex := 0; workerIndex < n; workerIndex++ {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-w.jobCh:
					w.processPendingJobs(ctx)
				}
			}
		}()
	}
}

func (w *Worker) processPendingJobs(ctx context.Context) {
	for {
		job, err := w.jobRepo.ClaimNext(ctx)
		if err != nil {
			log.Printf("ERROR: claim execution job failed: %v", err)
			return
		}
		if job == nil {
			return
		}

		w.processJob(ctx, job)
	}
}

func (w *Worker) processJob(ctx context.Context, job *postgres.ExecutionJob) {
	newVersionID, err := w.executor.Execute(ctx, job)
	if err != nil {
		errorMessage := err.Error()
		if updateErr := w.jobRepo.UpdateStatus(ctx, job.ID, "failed", &errorMessage, nil); updateErr != nil {
			log.Printf("ERROR: update failed job status failed: job=%s err=%v", job.ID, updateErr)
		}
		if updateErr := w.transitionTask(ctx, job.TaskID, models.StatusFailed, &errorMessage); updateErr != nil {
			log.Printf("ERROR: transition task to failed failed: task=%s err=%v", job.TaskID, updateErr)
		}
		return
	}

	if err := w.jobRepo.UpdateStatus(ctx, job.ID, "done", nil, &newVersionID); err != nil {
		log.Printf("ERROR: update finished job status failed: job=%s err=%v", job.ID, err)
		return
	}
	if err := w.transitionTask(ctx, job.TaskID, models.StatusCompleted, nil); err != nil {
		log.Printf("ERROR: transition task to completed failed: task=%s err=%v", job.TaskID, err)
	}
}

func (w *Worker) transitionTask(ctx context.Context, taskID string, to string, errorMessage *string) error {
	task, err := w.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		return err
	}
	if task == nil {
		return errTaskNotFound
	}

	if err := models.Transition(task.Status, to); err != nil {
		return err
	}

	return w.taskRepo.UpdateStatus(ctx, taskID, to, errorMessage)
}

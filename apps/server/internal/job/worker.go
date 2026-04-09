package job

import (
	"context"
	"errors"
	"log"

	executoragent "agent_project/apps/server/internal/agent/executor"
	"agent_project/apps/server/internal/storage/postgres"
	taskevents "agent_project/apps/server/internal/task/events"
	"agent_project/apps/server/internal/task/models"
)

var errTaskNotFound = errors.New("任务不存在")

// Worker 负责消费执行作业并推动任务完成。
type Worker struct {
	jobCh    chan postgres.ExecutionJob
	jobRepo  *postgres.JobRepo
	executor *executoragent.Executor
	taskRepo *postgres.TaskRepo
	eventSvc *taskevents.Service
}

// New 构造 channel-based worker。
func New(
	jobRepo *postgres.JobRepo,
	executor *executoragent.Executor,
	taskRepo *postgres.TaskRepo,
	bufSize int,
	eventService *taskevents.Service,
) *Worker {
	if bufSize <= 0 {
		bufSize = 1
	}

	return &Worker{
		jobCh:    make(chan postgres.ExecutionJob, bufSize),
		jobRepo:  jobRepo,
		executor: executor,
		taskRepo: taskRepo,
		eventSvc: eventService,
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
			log.Printf("错误：领取执行任务失败：%v", err)
			return
		}
		if job == nil {
			return
		}

		w.recordJobEvent(ctx, job, "info", "job.claimed", "执行作业已被 worker 领取", map[string]any{
			"approval_id": job.ApprovalID,
			"job_status":  job.Status,
		})
		w.processJob(ctx, job)
	}
}

func (w *Worker) processJob(ctx context.Context, job *postgres.ExecutionJob) {
	newVersionID, err := w.executor.Execute(ctx, job)
	if err != nil {
		errorMessage := err.Error()
		if updateErr := w.jobRepo.UpdateStatus(ctx, job.ID, "failed", &errorMessage, nil); updateErr != nil {
			log.Printf("错误：更新失败作业状态失败：job=%s err=%v", job.ID, updateErr)
		}
		if updateErr := w.transitionTask(ctx, job.TaskID, models.StatusFailed, &errorMessage); updateErr != nil {
			log.Printf("错误：将任务切换为失败状态时出错：task=%s err=%v", job.TaskID, updateErr)
		}
		w.recordJobEvent(ctx, job, "error", "job.failed", "执行作业执行失败", map[string]any{
			"approval_id":   job.ApprovalID,
			"error_message": errorMessage,
		})
		return
	}

	if err := w.jobRepo.UpdateStatus(ctx, job.ID, "done", nil, &newVersionID); err != nil {
		log.Printf("错误：更新已完成作业状态失败：job=%s err=%v", job.ID, err)
		return
	}
	w.recordJobEvent(ctx, job, "info", "job.completed", "执行作业已完成", map[string]any{
		"approval_id":    job.ApprovalID,
		"new_version_id": newVersionID,
	})
	if err := w.transitionTask(ctx, job.TaskID, models.StatusCompleted, nil); err != nil {
		log.Printf("错误：将任务切换为已完成状态时出错：task=%s err=%v", job.TaskID, err)
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

func (w *Worker) recordJobEvent(
	ctx context.Context,
	job *postgres.ExecutionJob,
	level string,
	eventType string,
	message string,
	payload map[string]any,
) {
	if w.eventSvc == nil {
		return
	}

	runID := job.ID
	if _, err := w.eventSvc.Record(ctx, taskevents.RecordInput{
		TaskID:    job.TaskID,
		RunID:     &runID,
		Source:    "job_worker",
		Level:     level,
		EventType: eventType,
		Message:   message,
		Payload:   payload,
	}); err != nil {
		log.Printf("警告：记录任务事件失败：task=%s run=%s event=%s err=%v", job.TaskID, job.ID, eventType, err)
	}
}

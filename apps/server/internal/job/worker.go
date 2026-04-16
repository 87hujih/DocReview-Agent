package job

import (
	"context"
	"fmt"
	"log"

	executoragent "agent_project/apps/server/internal/agent/executor"
	"agent_project/apps/server/internal/storage/postgres"
	taskevents "agent_project/apps/server/internal/task/events"

	"github.com/jackc/pgx/v5"
)

// Worker 负责消费执行作业并推动任务完成。
type Worker struct {
	jobCh    chan struct{}
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
		jobCh:    make(chan struct{}, bufSize),
		jobRepo:  jobRepo,
		executor: executor,
		taskRepo: taskRepo,
		eventSvc: eventService,
	}
}

// JobCh 返回用于投递执行信号的发送端。
func (w *Worker) JobCh() chan<- struct{} {
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
	prepared, err := w.executor.Prepare(ctx, job)
	if err != nil {
		errorMessage := err.Error()
		if finalizeErr := w.finalizeJobFailure(ctx, job, errorMessage, "执行作业执行失败"); finalizeErr != nil {
			log.Printf("错误：原子收尾失败作业失败：job=%s err=%v", job.ID, finalizeErr)
		}
		return
	}

	result, err := w.commitPreparedJob(ctx, job, prepared)
	if err != nil {
		errorMessage := fmt.Sprintf("提交 prepared execution 失败：%v", err)
		log.Printf("错误：%s job=%s", errorMessage, job.ID)
		if finalizeErr := w.finalizeJobFailure(ctx, job, errorMessage, "执行作业落库失败"); finalizeErr != nil {
			log.Printf("错误：回退作业到 failed 状态失败：job=%s err=%v", job.ID, finalizeErr)
		}
		return
	}

	if result.JobStatus == "failed" {
		return
	}
}

func (w *Worker) finalizeJobSuccess(ctx context.Context, job *postgres.ExecutionJob, newVersionID string) error {
	return w.jobRepo.FinalizeSuccess(ctx, job.ID, newVersionID, func(
		ctx context.Context,
		tx pgx.Tx,
		task *postgres.Task,
		job *postgres.ExecutionJob,
		fromTaskStatus string,
	) error {
		if err := w.recordEventTx(ctx, tx, taskevents.RecordInput{
			TaskID:    task.ID,
			RunID:     stringPointer(job.ID),
			Source:    "job_worker",
			Level:     "info",
			EventType: "job.completed",
			Message:   "执行作业已完成",
			Payload: map[string]any{
				"approval_id":    job.ApprovalID,
				"new_version_id": newVersionID,
			},
		}); err != nil {
			return err
		}

		return w.recordEventTx(ctx, tx, taskevents.RecordInput{
			TaskID:    task.ID,
			RunID:     stringPointer(job.ID),
			Source:    "job_worker",
			Level:     "info",
			EventType: "task.status_changed",
			Message:   "任务状态已更新",
			Payload: map[string]any{
				"from_status": fromTaskStatus,
				"to_status":   task.Status,
			},
		})
	})
}

func (w *Worker) commitPreparedJob(
	ctx context.Context,
	job *postgres.ExecutionJob,
	prepared *executoragent.PreparedExecution,
) (*postgres.CommitPreparedExecutionResult, error) {
	return w.jobRepo.CommitPreparedExecution(ctx, postgres.CommitPreparedExecutionParams{
		JobID:         job.ID,
		BaseVersionID: prepared.BaseVersion.ID,
		NewContent:    prepared.NewContent,
		Chunks:        prepared.PreparedChunks,
	}, func(
		ctx context.Context,
		tx pgx.Tx,
		task *postgres.Task,
		job *postgres.ExecutionJob,
		fromTaskStatus string,
	) error {
		if job.Status == "done" {
			if err := w.recordEventTx(ctx, tx, taskevents.RecordInput{
				TaskID:    task.ID,
				RunID:     stringPointer(job.ID),
				Source:    "job_worker",
				Level:     "info",
				EventType: "job.completed",
				Message:   "执行作业已完成",
				Payload: map[string]any{
					"approval_id":    job.ApprovalID,
					"new_version_id": dereferenceString(job.NewVersionID),
				},
			}); err != nil {
				return err
			}

			return w.recordEventTx(ctx, tx, taskevents.RecordInput{
				TaskID:    task.ID,
				RunID:     stringPointer(job.ID),
				Source:    "job_worker",
				Level:     "info",
				EventType: "task.status_changed",
				Message:   "任务状态已更新",
				Payload: map[string]any{
					"from_status": fromTaskStatus,
					"to_status":   task.Status,
				},
			})
		}

		if err := w.recordEventTx(ctx, tx, taskevents.RecordInput{
			TaskID:    task.ID,
			RunID:     stringPointer(job.ID),
			Source:    "job_worker",
			Level:     "error",
			EventType: "job.failed",
			Message:   "执行作业执行失败",
			Payload: map[string]any{
				"approval_id":   job.ApprovalID,
				"error_message": dereferenceString(job.ErrorMessage),
			},
		}); err != nil {
			return err
		}

		return w.recordEventTx(ctx, tx, taskevents.RecordInput{
			TaskID:    task.ID,
			RunID:     stringPointer(job.ID),
			Source:    "job_worker",
			Level:     "error",
			EventType: "task.status_changed",
			Message:   "任务状态已更新",
			Payload: map[string]any{
				"from_status":   fromTaskStatus,
				"to_status":     task.Status,
				"error_message": dereferenceString(job.ErrorMessage),
			},
		})
	})
}

func (w *Worker) finalizeJobFailure(ctx context.Context, job *postgres.ExecutionJob, errorMessage string, jobMessage string) error {
	return w.jobRepo.FinalizeFailure(ctx, job.ID, errorMessage, func(
		ctx context.Context,
		tx pgx.Tx,
		task *postgres.Task,
		job *postgres.ExecutionJob,
		fromTaskStatus string,
	) error {
		if err := w.recordEventTx(ctx, tx, taskevents.RecordInput{
			TaskID:    task.ID,
			RunID:     stringPointer(job.ID),
			Source:    "job_worker",
			Level:     "error",
			EventType: "job.failed",
			Message:   jobMessage,
			Payload: map[string]any{
				"approval_id":   job.ApprovalID,
				"error_message": errorMessage,
			},
		}); err != nil {
			return err
		}

		return w.recordEventTx(ctx, tx, taskevents.RecordInput{
			TaskID:    task.ID,
			RunID:     stringPointer(job.ID),
			Source:    "job_worker",
			Level:     "error",
			EventType: "task.status_changed",
			Message:   "任务状态已更新",
			Payload: map[string]any{
				"from_status":   fromTaskStatus,
				"to_status":     task.Status,
				"error_message": errorMessage,
			},
		})
	})
}

func (w *Worker) recordEventTx(ctx context.Context, tx pgx.Tx, input taskevents.RecordInput) error {
	if w.eventSvc == nil {
		return nil
	}

	_, err := w.eventSvc.RecordTx(ctx, tx, input)
	return err
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

func stringPointer(value string) *string {
	return &value
}

func dereferenceString(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}

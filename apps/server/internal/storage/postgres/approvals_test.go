package postgres

import (
	"context"
	"testing"

	"agent_project/apps/server/internal/task/models"

	"github.com/jackc/pgx/v5"
)

// TestApprovalRepoCRUD 验证审批仓储的基本读写流程。
func TestApprovalRepoCRUD(t *testing.T) {
	pool := newTestPool(t)
	repo := NewApprovalRepo(pool)
	resourceRepo := NewResourceRepo(pool)
	taskRepo := NewTaskRepo(pool)
	ctx := testContext(t)

	resource, err := resourceRepo.Create(ctx, "审批仓储测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})
	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 第一章\n原始正文", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	task, err := taskRepo.Create(ctx, resource.ID, "测试审批流程")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	// 创建 pending approval
	approvalRecord, err := repo.Create(ctx, task.ID, version.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}
	if approvalRecord.Status != "pending" {
		t.Fatalf("expected status pending, got %q", approvalRecord.Status)
	}
	if approvalRecord.TaskID != task.ID {
		t.Fatalf("expected task_id %q, got %q", task.ID, approvalRecord.TaskID)
	}
	if approvalRecord.BaseVersionID == nil || *approvalRecord.BaseVersionID != version.ID {
		t.Fatalf("expected base version %q, got %#v", version.ID, approvalRecord.BaseVersionID)
	}

	// 按 ID 查询
	got, err := repo.GetByID(ctx, approvalRecord.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if got == nil || got.ID != approvalRecord.ID {
		t.Fatalf("expected approval %q, got %v", approvalRecord.ID, got)
	}

	// 按 task_id 查询
	gotByTask, err := repo.GetByTaskID(ctx, task.ID)
	if err != nil {
		t.Fatalf("get by task_id: %v", err)
	}
	if gotByTask == nil || gotByTask.ID != approvalRecord.ID {
		t.Fatalf("expected approval %q by task, got %v", approvalRecord.ID, gotByTask)
	}

	// 更新为 approved
	if err := repo.UpdateStatus(ctx, approvalRecord.ID, "approved", nil); err != nil {
		t.Fatalf("update status to approved: %v", err)
	}
	updated, err := repo.GetByID(ctx, approvalRecord.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if updated.Status != "approved" {
		t.Fatalf("expected status approved, got %q", updated.Status)
	}
	if updated.DecidedAt == nil {
		t.Fatal("expected decided_at to be set after approval")
	}
}

func TestUpdateApprovalStatusTxReturningReturnsUpdatedApproval(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	taskRepo := NewTaskRepo(pool)
	approvalRepo := NewApprovalRepo(pool)
	ctx := testContext(t)

	resource, err := resourceRepo.Create(ctx, "审批事务内 returning 测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() { cleanupResource(t, pool, resource.ID) })
	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 第一章\n原始正文", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	task, err := taskRepo.Create(ctx, resource.ID, "验证事务内 returning")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	approvalRecord, err := approvalRepo.Create(ctx, task.ID, version.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	rejectReason := "需要补充依据"
	updatedApproval, err := UpdateApprovalStatusTxReturning(ctx, tx, approvalRecord.ID, "rejected", &rejectReason)
	if err != nil {
		t.Fatalf("update approval status returning: %v", err)
	}
	if updatedApproval == nil {
		t.Fatal("expected updated approval, got nil")
	}
	if updatedApproval.Status != "rejected" {
		t.Fatalf("expected status %q, got %q", "rejected", updatedApproval.Status)
	}
	if updatedApproval.RejectReason == nil || *updatedApproval.RejectReason != rejectReason {
		t.Fatalf("expected reject reason %q, got %#v", rejectReason, updatedApproval.RejectReason)
	}
	if updatedApproval.DecidedAt == nil || updatedApproval.DecidedAt.IsZero() {
		t.Fatal("expected decided_at to be set")
	}
	if updatedApproval.BaseVersionID == nil || *updatedApproval.BaseVersionID != version.ID {
		t.Fatalf("expected base version %q, got %#v", version.ID, updatedApproval.BaseVersionID)
	}
}

// TestApprovalRepoListIsolated 验证审批列表查询只断言本测试创建的数据，不依赖共享库全局状态。
func TestApprovalRepoListIsolated(t *testing.T) {
	pool := newTestPool(t)
	repo := NewApprovalRepo(pool)
	resourceRepo := NewResourceRepo(pool)
	taskRepo := NewTaskRepo(pool)
	ctx := testContext(t)

	resource1, err := resourceRepo.Create(ctx, "审批列表隔离测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource1: %v", err)
	}
	t.Cleanup(func() { cleanupResource(t, pool, resource1.ID) })
	version1, err := resourceRepo.CreateVersion(ctx, resource1.ID, 1, "## 第一章\n原始正文", "original")
	if err != nil {
		t.Fatalf("create version1: %v", err)
	}

	resource2, err := resourceRepo.Create(ctx, "审批列表隔离测试2-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource2: %v", err)
	}
	t.Cleanup(func() { cleanupResource(t, pool, resource2.ID) })
	version2, err := resourceRepo.CreateVersion(ctx, resource2.ID, 1, "## 第一章\n原始正文", "original")
	if err != nil {
		t.Fatalf("create version2: %v", err)
	}
	task1, err := taskRepo.Create(ctx, resource1.ID, "审批列表测试任务1")
	if err != nil {
		t.Fatalf("create task1: %v", err)
	}
	task2, err := taskRepo.Create(ctx, resource2.ID, "审批列表测试任务2")
	if err != nil {
		t.Fatalf("create task2: %v", err)
	}

	approval1, err := repo.Create(ctx, task1.ID, version1.ID)
	if err != nil {
		t.Fatalf("create approval1: %v", err)
	}
	approval2, err := repo.Create(ctx, task2.ID, version2.ID)
	if err != nil {
		t.Fatalf("create approval2: %v", err)
	}

	// 将 approval2 更新为 rejected
	reason := "测试拒绝原因"
	if err := repo.UpdateStatus(ctx, approval2.ID, "rejected", &reason); err != nil {
		t.Fatalf("update approval2 to rejected: %v", err)
	}

	testIDs := map[string]struct{}{
		approval1.ID: {},
		approval2.ID: {},
	}

	// 查全量列表，按本测试的 IDs 过滤后断言状态分布，不断言全库数量
	all, err := repo.List(ctx, "")
	if err != nil {
		t.Fatalf("list all: %v", err)
	}

	var pendingCount, rejectedCount int
	for _, a := range all {
		if _, ok := testIDs[a.ID]; !ok {
			continue
		}
		switch a.Status {
		case "pending":
			pendingCount++
		case "rejected":
			rejectedCount++
		}
	}

	if pendingCount != 1 {
		t.Fatalf("expected 1 pending in test set, got %d", pendingCount)
	}
	if rejectedCount != 1 {
		t.Fatalf("expected 1 rejected in test set, got %d", rejectedCount)
	}

	// 按 status 过滤
	pendingList, err := repo.List(ctx, "pending")
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}

	var approval1Found bool
	for _, a := range pendingList {
		if a.Status != "pending" {
			t.Fatalf("list(pending) 返回了非 pending 记录 %q，状态 %q", a.ID, a.Status)
		}
		if a.ID == approval1.ID {
			approval1Found = true
		}
	}
	if !approval1Found {
		t.Fatalf("pending 列表中未找到 approval1 %q", approval1.ID)
	}
}

// TestCreateForTaskAwaitingApprovalSuccess 验证原子创建审批记录并切换任务状态的正常路径。
func TestCreateForTaskAwaitingApprovalSuccess(t *testing.T) {
	pool := newTestPool(t)
	repo := NewApprovalRepo(pool)
	resourceRepo := NewResourceRepo(pool)
	taskRepo := NewTaskRepo(pool)
	ctx := testContext(t)

	resource, err := resourceRepo.Create(ctx, "原子审批测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() { cleanupResource(t, pool, resource.ID) })

	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 第一章\n原始正文", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}

	task, err := taskRepo.Create(ctx, resource.ID, "原子审批流程测试")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	// 手动将任务状态设置为 drafting
	if _, err := pool.Exec(ctx, `UPDATE tasks SET status = 'drafting' WHERE id = $1`, task.ID); err != nil {
		t.Fatalf("set task status to drafting: %v", err)
	}

	approvalRecord, err := repo.CreateForTaskAwaitingApproval(ctx, task.ID, version.ID)
	if err != nil {
		t.Fatalf("CreateForTaskAwaitingApproval 期望成功，实际得到 %v", err)
	}
	if approvalRecord.Status != "pending" {
		t.Fatalf("期望审批状态为 pending，实际为 %q", approvalRecord.Status)
	}
	if approvalRecord.TaskID != task.ID {
		t.Fatalf("期望 task_id %q，实际 %q", task.ID, approvalRecord.TaskID)
	}
	if approvalRecord.BaseVersionID == nil || *approvalRecord.BaseVersionID != version.ID {
		t.Fatalf("期望审批记录固化 base_version_id=%q，实际为 %#v", version.ID, approvalRecord.BaseVersionID)
	}

	// 验证任务状态已切换为 awaiting_approval
	updated, err := taskRepo.GetByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task after atomic approval: %v", err)
	}
	if updated.Status != "awaiting_approval" {
		t.Fatalf("期望任务状态为 awaiting_approval，实际为 %q", updated.Status)
	}
}

func TestCreateForTaskAwaitingApprovalStoresBaseVersionID(t *testing.T) {
	pool := newTestPool(t)
	repo := NewApprovalRepo(pool)
	resourceRepo := NewResourceRepo(pool)
	taskRepo := NewTaskRepo(pool)
	ctx := testContext(t)

	resource, err := resourceRepo.Create(ctx, "原子审批 base version 测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() { cleanupResource(t, pool, resource.ID) })

	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 第一章\n原始正文", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}

	task, err := taskRepo.Create(ctx, resource.ID, "审批链需要固化 base version")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	if _, err := pool.Exec(ctx, `UPDATE tasks SET status = 'drafting' WHERE id = $1`, task.ID); err != nil {
		t.Fatalf("set task status to drafting: %v", err)
	}

	approvalRecord, err := repo.CreateForTaskAwaitingApproval(ctx, task.ID, version.ID)
	if err != nil {
		t.Fatalf("CreateForTaskAwaitingApproval 期望成功，实际得到 %v", err)
	}
	if approvalRecord.BaseVersionID == nil || *approvalRecord.BaseVersionID != version.ID {
		t.Fatalf("期望审批记录固化 base_version_id=%q，实际为 %#v", version.ID, approvalRecord.BaseVersionID)
	}
}

// TestCreateForTaskAwaitingApprovalNonDrafting 验证任务非 drafting 状态时返回错误且不创建审批记录。
func TestCreateForTaskAwaitingApprovalNonDrafting(t *testing.T) {
	pool := newTestPool(t)
	repo := NewApprovalRepo(pool)
	resourceRepo := NewResourceRepo(pool)
	taskRepo := NewTaskRepo(pool)
	ctx := testContext(t)

	resource, err := resourceRepo.Create(ctx, "非法状态审批测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() { cleanupResource(t, pool, resource.ID) })

	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 第一章\n原始正文", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}

	task, err := taskRepo.Create(ctx, resource.ID, "非法状态审批测试任务")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	// task 初始状态为 pending，不是 drafting

	_, err = repo.CreateForTaskAwaitingApproval(ctx, task.ID, version.ID)
	if err == nil {
		t.Fatal("任务状态非 drafting 时期望返回错误，实际得到 nil")
	}

	// 验证未创建审批记录
	existing, err := repo.GetByTaskID(ctx, task.ID)
	if err != nil {
		t.Fatalf("get approval by task_id: %v", err)
	}
	if existing != nil {
		t.Fatalf("事务回滚后不应存在审批记录，实际找到 %q", existing.ID)
	}
}

func TestUpdateApprovalStatusTxReturningReturnsRejectReason(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	taskRepo := NewTaskRepo(pool)
	approvalRepo := NewApprovalRepo(pool)
	ctx := testContext(t)

	resource, err := resourceRepo.Create(ctx, "审批拒绝 returning 测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() { cleanupResource(t, pool, resource.ID) })
	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 第一章\n原始正文", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}

	task, err := taskRepo.Create(ctx, resource.ID, "验证拒绝 returning")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE tasks SET status = 'drafting' WHERE id = $1`, task.ID); err != nil {
		t.Fatalf("set task status to drafting: %v", err)
	}
	approvalRecord, err := approvalRepo.CreateForTaskAwaitingApproval(ctx, task.ID, version.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	reason := "缺少关键信息"
	updated, err := UpdateApprovalStatusTxReturning(ctx, tx, approvalRecord.ID, "rejected", &reason)
	if err != nil {
		t.Fatalf("update approval reject status returning: %v", err)
	}
	if updated.Status != "rejected" {
		t.Fatalf("expected approval status %q, got %q", "rejected", updated.Status)
	}
	if updated.RejectReason == nil || *updated.RejectReason != reason {
		t.Fatalf("expected reject reason %q, got %#v", reason, updated.RejectReason)
	}
	if updated.DecidedAt == nil || updated.DecidedAt.IsZero() {
		t.Fatal("expected decided_at to be set")
	}
	if updated.BaseVersionID == nil || *updated.BaseVersionID != version.ID {
		t.Fatalf("expected base_version_id %q, got %#v", version.ID, updated.BaseVersionID)
	}

	storedBeforeCommit, err := approvalRepo.GetByID(ctx, approvalRecord.ID)
	if err != nil {
		t.Fatalf("get approval before rollback: %v", err)
	}
	if storedBeforeCommit == nil {
		t.Fatal("expected approval before rollback, got nil")
	}
	if storedBeforeCommit.Status != "pending" {
		t.Fatalf("expected stored approval to remain pending before commit, got %q", storedBeforeCommit.Status)
	}
	if storedBeforeCommit.RejectReason != nil {
		t.Fatalf("expected stored reject reason nil before commit, got %#v", storedBeforeCommit.RejectReason)
	}
}

func TestJobRepoCreateRejectsDuplicateApprovalID(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	taskRepo := NewTaskRepo(pool)
	approvalRepo := NewApprovalRepo(pool)
	jobRepo := NewJobRepo(pool)
	ctx := testContext(t)

	resource, err := resourceRepo.Create(ctx, "执行作业唯一约束测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() { cleanupResource(t, pool, resource.ID) })
	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 第一章\n原始正文", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}

	task, err := taskRepo.Create(ctx, resource.ID, "相同审批不应重复创建 job")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	approvalRecord, err := approvalRepo.Create(ctx, task.ID, version.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}

	if _, err := jobRepo.Create(ctx, task.ID, approvalRecord.ID, version.ID); err != nil {
		t.Fatalf("create first job: %v", err)
	}
	if _, err := jobRepo.Create(ctx, task.ID, approvalRecord.ID, version.ID); err == nil {
		t.Fatal("expected duplicate approval_id job creation to fail")
	}
}

func TestJobRepoGetByApprovalIDReturnsJob(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	taskRepo := NewTaskRepo(pool)
	approvalRepo := NewApprovalRepo(pool)
	jobRepo := NewJobRepo(pool)
	ctx := testContext(t)

	resource, err := resourceRepo.Create(ctx, "按审批读取作业测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() { cleanupResource(t, pool, resource.ID) })
	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 第一章\n原始正文", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	task, err := taskRepo.Create(ctx, resource.ID, "按 approval_id 查 job")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	approvalRecord, err := approvalRepo.Create(ctx, task.ID, version.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}
	jobRecord, err := jobRepo.Create(ctx, task.ID, approvalRecord.ID, version.ID)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	gotJob, err := jobRepo.GetByApprovalID(ctx, approvalRecord.ID)
	if err != nil {
		t.Fatalf("get job by approval id: %v", err)
	}
	if gotJob == nil {
		t.Fatal("expected job, got nil")
	}
	if gotJob.ID != jobRecord.ID {
		t.Fatalf("expected job id %q, got %q", jobRecord.ID, gotJob.ID)
	}
	if gotJob.ApprovalID != approvalRecord.ID {
		t.Fatalf("expected approval id %q, got %q", approvalRecord.ID, gotJob.ApprovalID)
	}
	if gotJob.BaseVersionID == nil || *gotJob.BaseVersionID != version.ID {
		t.Fatalf("expected base version %q, got %#v", version.ID, gotJob.BaseVersionID)
	}
}

func TestJobRepoGetByApprovalIDReturnsNilWhenMissing(t *testing.T) {
	pool := newTestPool(t)
	jobRepo := NewJobRepo(pool)
	ctx := testContext(t)

	gotJob, err := jobRepo.GetByApprovalID(ctx, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("get missing job by approval id: %v", err)
	}
	if gotJob != nil {
		t.Fatalf("expected nil job, got %#v", gotJob)
	}
}

func TestApprovalRepoCreateRejectsMissingBaseVersionID(t *testing.T) {
	pool := newTestPool(t)
	repo := NewApprovalRepo(pool)
	resourceRepo := NewResourceRepo(pool)
	taskRepo := NewTaskRepo(pool)
	ctx := testContext(t)

	resource, err := resourceRepo.Create(ctx, "审批 helper 缺少 base version 测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() { cleanupResource(t, pool, resource.ID) })
	task, err := taskRepo.Create(ctx, resource.ID, "审批 helper 缺少 base_version_id")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	if _, err := repo.Create(ctx, task.ID, ""); err == nil {
		t.Fatal("expected create approval without base_version_id to fail")
	}
}

func TestJobRepoCreateRejectsMissingBaseVersionID(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	taskRepo := NewTaskRepo(pool)
	approvalRepo := NewApprovalRepo(pool)
	jobRepo := NewJobRepo(pool)
	ctx := testContext(t)

	resource, err := resourceRepo.Create(ctx, "作业 helper 缺少 base version 测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() { cleanupResource(t, pool, resource.ID) })
	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 第一章\n原始正文", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	task, err := taskRepo.Create(ctx, resource.ID, "作业 helper 缺少 base_version_id")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	approvalRecord, err := approvalRepo.Create(ctx, task.ID, version.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}

	if _, err := jobRepo.Create(ctx, task.ID, approvalRecord.ID, ""); err == nil {
		t.Fatal("expected create job without base_version_id to fail")
	}
}

func TestJobRepoFinalizeSuccessWritesTaskStateAndEventsAtomically(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	taskRepo := NewTaskRepo(pool)
	approvalRepo := NewApprovalRepo(pool)
	jobRepo := NewJobRepo(pool)
	eventRepo := NewTaskEventRepo(pool)
	ctx := testContext(t)

	resource, err := resourceRepo.Create(ctx, "作业成功收尾测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() { cleanupResource(t, pool, resource.ID) })

	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "原始版本内容", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}

	task, err := taskRepo.Create(ctx, resource.ID, "验证作业成功收尾")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusExecuting, nil); err != nil {
		t.Fatalf("update task to executing: %v", err)
	}

	approvalRecord, err := approvalRepo.Create(ctx, task.ID, version.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}
	job, err := jobRepo.Create(ctx, task.ID, approvalRecord.ID, version.ID)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if err := jobRepo.UpdateStatus(ctx, job.ID, "running", nil, nil); err != nil {
		t.Fatalf("mark job running: %v", err)
	}

	if err := jobRepo.FinalizeSuccess(ctx, job.ID, version.ID, func(
		ctx context.Context,
		tx pgx.Tx,
		task *Task,
		job *ExecutionJob,
		fromTaskStatus string,
	) error {
		if _, err := eventRepo.AddTx(ctx, tx, TaskEventCreateParams{
			TaskID:    task.ID,
			RunID:     &job.ID,
			Source:    "job_worker",
			Level:     "info",
			EventType: "job.completed",
			Message:   "执行作业已完成",
			Payload:   []byte(`{"approval_id":"` + job.ApprovalID + `","new_version_id":"` + version.ID + `"}`),
		}); err != nil {
			return err
		}
		if _, err := eventRepo.AddTx(ctx, tx, TaskEventCreateParams{
			TaskID:    task.ID,
			RunID:     &job.ID,
			Source:    "job_worker",
			Level:     "info",
			EventType: "task.status_changed",
			Message:   "任务状态已更新",
			Payload:   []byte(`{"from_status":"` + fromTaskStatus + `","to_status":"` + task.Status + `"}`),
		}); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("finalize success: %v", err)
	}

	storedJob, err := jobRepo.GetByID(ctx, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if storedJob == nil || storedJob.Status != "done" {
		t.Fatalf("expected job status done, got %#v", storedJob)
	}
	if storedJob.NewVersionID == nil || *storedJob.NewVersionID != version.ID {
		t.Fatalf("expected new version id %q, got %#v", version.ID, storedJob.NewVersionID)
	}

	storedTask, err := taskRepo.GetByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if storedTask == nil || storedTask.Status != models.StatusCompleted {
		t.Fatalf("expected task status %q, got %#v", models.StatusCompleted, storedTask)
	}

	events, err := eventRepo.ListByTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("list task events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 task events, got %d", len(events))
	}
	if events[0].EventType != "job.completed" {
		t.Fatalf("expected first event type %q, got %q", "job.completed", events[0].EventType)
	}
	if events[1].EventType != "task.status_changed" {
		t.Fatalf("expected second event type %q, got %q", "task.status_changed", events[1].EventType)
	}
}

func TestJobRepoFinalizeFailureWritesTaskStateAndEventsAtomically(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	taskRepo := NewTaskRepo(pool)
	approvalRepo := NewApprovalRepo(pool)
	jobRepo := NewJobRepo(pool)
	eventRepo := NewTaskEventRepo(pool)
	ctx := testContext(t)

	resource, err := resourceRepo.Create(ctx, "作业失败收尾测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() { cleanupResource(t, pool, resource.ID) })
	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "原始版本内容", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	task, err := taskRepo.Create(ctx, resource.ID, "验证作业失败收尾")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusExecuting, nil); err != nil {
		t.Fatalf("update task to executing: %v", err)
	}

	approvalRecord, err := approvalRepo.Create(ctx, task.ID, version.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}
	job, err := jobRepo.Create(ctx, task.ID, approvalRecord.ID, version.ID)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if err := jobRepo.UpdateStatus(ctx, job.ID, "running", nil, nil); err != nil {
		t.Fatalf("mark job running: %v", err)
	}

	errorMessage := "执行作业失败"
	if err := jobRepo.FinalizeFailure(ctx, job.ID, errorMessage, func(
		ctx context.Context,
		tx pgx.Tx,
		task *Task,
		job *ExecutionJob,
		fromTaskStatus string,
	) error {
		if _, err := eventRepo.AddTx(ctx, tx, TaskEventCreateParams{
			TaskID:    task.ID,
			RunID:     &job.ID,
			Source:    "job_worker",
			Level:     "error",
			EventType: "job.failed",
			Message:   "执行作业执行失败",
			Payload:   []byte(`{"approval_id":"` + job.ApprovalID + `","error_message":"` + errorMessage + `"}`),
		}); err != nil {
			return err
		}
		if _, err := eventRepo.AddTx(ctx, tx, TaskEventCreateParams{
			TaskID:    task.ID,
			RunID:     &job.ID,
			Source:    "job_worker",
			Level:     "error",
			EventType: "task.status_changed",
			Message:   "任务状态已更新",
			Payload:   []byte(`{"from_status":"` + fromTaskStatus + `","to_status":"` + task.Status + `","error_message":"` + errorMessage + `"}`),
		}); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("finalize failure: %v", err)
	}

	storedJob, err := jobRepo.GetByID(ctx, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if storedJob == nil || storedJob.Status != "failed" {
		t.Fatalf("expected job status failed, got %#v", storedJob)
	}
	if storedJob.ErrorMessage == nil || *storedJob.ErrorMessage != errorMessage {
		t.Fatalf("expected job error %q, got %#v", errorMessage, storedJob.ErrorMessage)
	}

	storedTask, err := taskRepo.GetByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if storedTask == nil || storedTask.Status != models.StatusFailed {
		t.Fatalf("expected task status %q, got %#v", models.StatusFailed, storedTask)
	}
	if storedTask.ErrorMessage == nil || *storedTask.ErrorMessage != errorMessage {
		t.Fatalf("expected task error %q, got %#v", errorMessage, storedTask.ErrorMessage)
	}

	events, err := eventRepo.ListByTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("list task events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 task events, got %d", len(events))
	}
	if events[0].EventType != "job.failed" {
		t.Fatalf("expected first event type %q, got %q", "job.failed", events[0].EventType)
	}
	if events[1].EventType != "task.status_changed" {
		t.Fatalf("expected second event type %q, got %q", "task.status_changed", events[1].EventType)
	}
}

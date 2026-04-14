package postgres

import (
	"testing"
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

	task, err := taskRepo.Create(ctx, resource.ID, "测试审批流程")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	// 创建 pending approval
	approvalRecord, err := repo.Create(ctx, task.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}
	if approvalRecord.Status != "pending" {
		t.Fatalf("expected status pending, got %q", approvalRecord.Status)
	}
	if approvalRecord.TaskID != task.ID {
		t.Fatalf("expected task_id %q, got %q", task.ID, approvalRecord.TaskID)
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

	resource2, err := resourceRepo.Create(ctx, "审批列表隔离测试2-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource2: %v", err)
	}
	t.Cleanup(func() { cleanupResource(t, pool, resource2.ID) })

	task1, err := taskRepo.Create(ctx, resource1.ID, "审批列表测试任务1")
	if err != nil {
		t.Fatalf("create task1: %v", err)
	}
	task2, err := taskRepo.Create(ctx, resource2.ID, "审批列表测试任务2")
	if err != nil {
		t.Fatalf("create task2: %v", err)
	}

	approval1, err := repo.Create(ctx, task1.ID)
	if err != nil {
		t.Fatalf("create approval1: %v", err)
	}
	approval2, err := repo.Create(ctx, task2.ID)
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

	task, err := taskRepo.Create(ctx, resource.ID, "原子审批流程测试")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	// 手动将任务状态设置为 drafting
	if _, err := pool.Exec(ctx, `UPDATE tasks SET status = 'drafting' WHERE id = $1`, task.ID); err != nil {
		t.Fatalf("set task status to drafting: %v", err)
	}

	approvalRecord, err := repo.CreateForTaskAwaitingApproval(ctx, task.ID)
	if err != nil {
		t.Fatalf("CreateForTaskAwaitingApproval 期望成功，实际得到 %v", err)
	}
	if approvalRecord.Status != "pending" {
		t.Fatalf("期望审批状态为 pending，实际为 %q", approvalRecord.Status)
	}
	if approvalRecord.TaskID != task.ID {
		t.Fatalf("期望 task_id %q，实际 %q", task.ID, approvalRecord.TaskID)
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

	task, err := taskRepo.Create(ctx, resource.ID, "非法状态审批测试任务")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	// task 初始状态为 pending，不是 drafting

	_, err = repo.CreateForTaskAwaitingApproval(ctx, task.ID)
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
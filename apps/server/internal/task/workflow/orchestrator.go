package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"agent_project/apps/server/internal/agent/editor"
	"agent_project/apps/server/internal/agent/planner"
	"agent_project/apps/server/internal/knowledge/citation"
	"agent_project/apps/server/internal/storage/postgres"
	taskevents "agent_project/apps/server/internal/task/events"
	"agent_project/apps/server/internal/task/models"
)

const retrieverLimit = 5
const failurePersistenceTimeout = 5 * time.Second

// Orchestrator 负责驱动任务的顶层业务编排。
type Orchestrator struct {
	taskRepo        *postgres.TaskRepo
	resourceRepo    *postgres.ResourceRepo
	approvalRepo    *postgres.ApprovalRepo
	plannerAgent    plannerRunner
	reviewerAgent   reviewerRunner
	editorAgent     editorRunner
	retrieverSvc    resourceSearcher
	eventService    *taskevents.Service
	contextMaxRunes int
}

type plannerRunner interface {
	Plan(ctx context.Context, instruction string, resourceTitle string, resourceSummary string) (*planner.PlanResult, error)
}

type reviewerRunner interface {
	Review(ctx context.Context, resourceContent string, citations []citation.Citation, intent string) (string, error)
}

type editorRunner interface {
	Edit(ctx context.Context, resourceContent string, reviewSummary string, citations []citation.Citation) (*editor.DiffPreview, error)
}

type resourceSearcher interface {
	SearchByResource(ctx context.Context, resourceID string, query string, limit int) ([]citation.Citation, error)
}

// New 构造任务编排器。contextMaxRunes 限制传给审阅和编辑代理的上下文字符数（≤0 使用 24000 默认值）。
func New(
	taskRepo *postgres.TaskRepo,
	resourceRepo *postgres.ResourceRepo,
	approvalRepo *postgres.ApprovalRepo,
	plannerAgent plannerRunner,
	reviewerAgent reviewerRunner,
	editorAgent editorRunner,
	retrieverSvc resourceSearcher,
	eventService *taskevents.Service,
	contextMaxRunes int,
) *Orchestrator {
	return &Orchestrator{
		taskRepo:        taskRepo,
		resourceRepo:    resourceRepo,
		approvalRepo:    approvalRepo,
		plannerAgent:    plannerAgent,
		reviewerAgent:   reviewerAgent,
		editorAgent:     editorAgent,
		retrieverSvc:    retrieverSvc,
		eventService:    eventService,
		contextMaxRunes: contextMaxRunes,
	}
}

// Orchestrate 顺序执行 planner -> retriever -> reviewer -> editor 四步流程。
func (o *Orchestrator) Orchestrate(ctx context.Context, task *postgres.Task) {
	resource, err := o.resourceRepo.GetByID(ctx, task.ResourceID)
	if err != nil {
		o.failTask(ctx, task, nil, err)
		return
	}
	if resource == nil {
		o.failTask(ctx, task, nil, fmt.Errorf("资源不存在"))
		return
	}

	version, err := o.resourceRepo.GetCurrentVersion(ctx, task.ResourceID)
	if err != nil {
		o.failTask(ctx, task, nil, err)
		return
	}
	if version == nil {
		o.failTask(ctx, task, nil, fmt.Errorf("资源当前版本不存在"))
		return
	}

	resourceSummary := summarizeContent(version.Content, 500)

	if err := o.transitionTask(ctx, task, models.StatusPlanning, nil); err != nil {
		o.failTask(ctx, task, nil, err)
		return
	}

	plannerStep, err := o.taskRepo.AddStep(ctx, task.ID, models.StepPlanner)
	if err != nil {
		o.failTask(ctx, task, nil, err)
		return
	}
	o.recordStepEvent(ctx, task, plannerStep, "info", "step.started", "规划步骤开始")

	planResult, err := o.plannerAgent.Plan(ctx, task.Instruction, resource.Title, resourceSummary)
	if err != nil {
		o.failTask(ctx, task, plannerStep, err)
		return
	}
	if err := validatePlanResult(planResult); err != nil {
		o.failTask(ctx, task, plannerStep, err)
		return
	}

	// 2.1: 将 planner 输出落库为 planner_result artifact
	plannerResultContent, err := json.Marshal(map[string]any{
		"intent":         planResult.Intent,
		"search_queries": planResult.SearchQueries,
		"focus_sections": planResult.FocusSections,
		"created_at":     time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		o.failTask(ctx, task, plannerStep, err)
		return
	}
	plannerArtifact, err := o.taskRepo.AddArtifact(ctx, task.ID, "planner_result", plannerResultContent)
	if err != nil {
		o.failTask(ctx, task, plannerStep, err)
		return
	}
	o.recordArtifactCreated(ctx, task, plannerArtifact, map[string]any{
		"focus_section_count": len(planResult.FocusSections),
	})

	if err := o.taskRepo.UpdateStep(ctx, plannerStep.ID, "completed", nil); err != nil {
		o.failTask(ctx, task, plannerStep, err)
		return
	}
	plannerStep.Status = "completed"
	o.recordStepEvent(ctx, task, plannerStep, "info", "step.completed", "规划步骤完成")

	if err := o.transitionTask(ctx, task, models.StatusRetrieving, nil); err != nil {
		o.failTask(ctx, task, nil, err)
		return
	}

	retrieverStep, err := o.taskRepo.AddStep(ctx, task.ID, models.StepRetriever)
	if err != nil {
		o.failTask(ctx, task, nil, err)
		return
	}
	o.recordStepEvent(ctx, task, retrieverStep, "info", "step.started", "检索步骤开始")

	allCitations := make([]citation.Citation, 0)
	for _, query := range planResult.SearchQueries {
		results, err := o.retrieverSvc.SearchByResource(ctx, task.ResourceID, query, retrieverLimit)
		if err != nil {
			o.failTask(ctx, task, retrieverStep, err)
			return
		}

		allCitations = append(allCitations, results...)
	}

	citations := dedupeCitations(allCitations)
	citationsContent, err := json.Marshal(citations)
	if err != nil {
		o.failTask(ctx, task, retrieverStep, err)
		return
	}
	citationsArtifact, err := o.taskRepo.AddArtifact(ctx, task.ID, "citations", citationsContent)
	if err != nil {
		o.failTask(ctx, task, retrieverStep, err)
		return
	}
	o.recordArtifactCreated(ctx, task, citationsArtifact, map[string]any{
		"citation_count": len(citations),
	})
	if err := o.taskRepo.UpdateStep(ctx, retrieverStep.ID, "completed", nil); err != nil {
		o.failTask(ctx, task, retrieverStep, err)
		return
	}
	retrieverStep.Status = "completed"
	o.recordStepEvent(ctx, task, retrieverStep, "info", "step.completed", "检索步骤完成")

	if err := o.transitionTask(ctx, task, models.StatusDrafting, nil); err != nil {
		o.failTask(ctx, task, nil, err)
		return
	}

	// 2.2/2.3: 构建聚焦上下文，传给 reviewer 和 editor
	cb := ContextBuilder{}
	agentCtx := cb.Build(version.Content, planResult.FocusSections, citations, o.contextMaxRunes)
	o.recordEvent(ctx, taskevents.RecordInput{
		TaskID:    task.ID,
		Source:    "orchestrator",
		Level:     "info",
		EventType: "context_summary",
		Message:   "已构建聚焦上下文",
		Payload: map[string]any{
			"used_sections": agentCtx.UsedSections,
			"total_runes":   len([]rune(agentCtx.Content)),
			"trimmed_runes": agentCtx.TrimmedRunes,
			"trim_reason":   agentCtx.TrimReason,
		},
	})

	reviewerStep, err := o.taskRepo.AddStep(ctx, task.ID, models.StepReviewer)
	if err != nil {
		o.failTask(ctx, task, nil, err)
		return
	}
	o.recordStepEvent(ctx, task, reviewerStep, "info", "step.started", "审阅步骤开始")

	reviewSummary, err := o.reviewerAgent.Review(ctx, agentCtx.Content, citations, planResult.Intent)
	if err != nil {
		o.failTask(ctx, task, reviewerStep, err)
		return
	}
	reviewSummary = strings.TrimSpace(reviewSummary)
	if reviewSummary == "" {
		o.failTask(ctx, task, reviewerStep, fmt.Errorf("审阅代理返回了空摘要"))
		return
	}

	reviewSummaryContent, err := json.Marshal(map[string]string{"summary": reviewSummary})
	if err != nil {
		o.failTask(ctx, task, reviewerStep, err)
		return
	}
	reviewArtifact, err := o.taskRepo.AddArtifact(ctx, task.ID, "review_summary", reviewSummaryContent)
	if err != nil {
		o.failTask(ctx, task, reviewerStep, err)
		return
	}
	o.recordArtifactCreated(ctx, task, reviewArtifact, nil)
	if err := o.taskRepo.UpdateStep(ctx, reviewerStep.ID, "completed", nil); err != nil {
		o.failTask(ctx, task, reviewerStep, err)
		return
	}
	reviewerStep.Status = "completed"
	o.recordStepEvent(ctx, task, reviewerStep, "info", "step.completed", "审阅步骤完成")

	editorStep, err := o.taskRepo.AddStep(ctx, task.ID, models.StepEditor)
	if err != nil {
		o.failTask(ctx, task, nil, err)
		return
	}
	o.recordStepEvent(ctx, task, editorStep, "info", "step.started", "编辑步骤开始")

	diffPreview, err := o.editorAgent.Edit(ctx, agentCtx.Content, reviewSummary, citations)
	if err != nil {
		o.failTask(ctx, task, editorStep, err)
		return
	}

	// 2.5: no-change 分支：任务直接完成，不创建 approval
	if diffPreview.NoChange {
		noChangeSummary, _ := json.Marshal(map[string]string{"reason": "编辑代理判断文档无需修改"})
		noChangeArtifact, err := o.taskRepo.AddArtifact(ctx, task.ID, "no_change_summary", noChangeSummary)
		if err != nil {
			o.failTask(ctx, task, editorStep, err)
			return
		}
		o.recordArtifactCreated(ctx, task, noChangeArtifact, nil)
		if err := o.taskRepo.UpdateStep(ctx, editorStep.ID, "completed", nil); err != nil {
			o.failTask(ctx, task, editorStep, err)
			return
		}
		editorStep.Status = "completed"
		o.recordStepEvent(ctx, task, editorStep, "info", "step.completed", "编辑步骤完成（无需修改）")
		if err := o.transitionTask(ctx, task, models.StatusCompleted, nil); err != nil {
			o.failTask(ctx, task, nil, err)
		}
		return
	}

	// 2.4: 严格校验 diff 内容
	if err := validateDiffPreview(diffPreview, citations); err != nil {
		o.failTask(ctx, task, editorStep, err)
		return
	}

	diffPreviewContent, err := json.Marshal(diffPreview)
	if err != nil {
		o.failTask(ctx, task, editorStep, err)
		return
	}
	diffArtifact, err := o.taskRepo.AddArtifact(ctx, task.ID, "diff_preview", diffPreviewContent)
	if err != nil {
		o.failTask(ctx, task, editorStep, err)
		return
	}
	o.recordArtifactCreated(ctx, task, diffArtifact, map[string]any{
		"section_count": len(diffPreview.Sections),
	})
	if err := o.taskRepo.UpdateStep(ctx, editorStep.ID, "completed", nil); err != nil {
		o.failTask(ctx, task, editorStep, err)
		return
	}
	editorStep.Status = "completed"
	o.recordStepEvent(ctx, task, editorStep, "info", "step.completed", "编辑步骤完成")

	approvalRecord, err := o.approvalRepo.CreateForTaskAwaitingApproval(ctx, task.ID, version.ID)
	if err != nil {
		o.failTask(ctx, task, nil, err)
		return
	}
	// 事务已在 CreateForTaskAwaitingApproval 中原子更新 DB，同步内存状态
	from := task.Status
	task.Status = models.StatusAwaitingApproval
	o.recordEvent(ctx, taskevents.RecordInput{
		TaskID:    task.ID,
		Source:    "orchestrator",
		Level:     "info",
		EventType: "task.status_changed",
		Message:   "任务状态已更新",
		Payload: map[string]any{
			"from_status": from,
			"to_status":   models.StatusAwaitingApproval,
		},
	})
	o.recordEvent(ctx, taskevents.RecordInput{
		TaskID:    task.ID,
		Source:    "orchestrator",
		Level:     "info",
		EventType: "approval.created",
		Message:   "审批记录已创建，等待人工处理",
		Payload: map[string]any{
			"approval_id": approvalRecord.ID,
			"status":      approvalRecord.Status,
		},
	})
}

func (o *Orchestrator) transitionTask(ctx context.Context, task *postgres.Task, to string, errorMessage *string) error {
	from := task.Status
	if err := models.Transition(task.Status, to); err != nil {
		return err
	}

	if err := o.taskRepo.UpdateStatus(ctx, task.ID, to, errorMessage); err != nil {
		return err
	}

	task.Status = to
	task.ErrorMessage = errorMessage
	o.recordEvent(ctx, taskevents.RecordInput{
		TaskID:    task.ID,
		Source:    "orchestrator",
		Level:     "info",
		EventType: "task.status_changed",
		Message:   "任务状态已更新",
		Payload: map[string]any{
			"from_status": from,
			"to_status":   to,
		},
	})
	return nil
}

func (o *Orchestrator) failTask(ctx context.Context, task *postgres.Task, step *postgres.TaskStep, cause error) {
	cleanupCtx, cancel := failureContext(ctx)
	defer cancel()

	if step != nil {
		errorMessage := cause.Error()
		_ = o.taskRepo.UpdateStep(cleanupCtx, step.ID, "failed", &errorMessage)
		step.Status = "failed"
		step.ErrorMessage = &errorMessage
		o.recordStepEvent(cleanupCtx, task, step, "error", "step.failed", "步骤执行失败")
	}

	errorMessage := cause.Error()
	if err := models.Transition(task.Status, models.StatusFailed); err == nil {
		from := task.Status
		_ = o.taskRepo.UpdateStatus(cleanupCtx, task.ID, models.StatusFailed, &errorMessage)
		task.Status = models.StatusFailed
		task.ErrorMessage = &errorMessage
		o.recordEvent(cleanupCtx, taskevents.RecordInput{
			TaskID:    task.ID,
			Source:    "orchestrator",
			Level:     "error",
			EventType: "task.status_changed",
			Message:   "任务状态已更新为失败",
			Payload: map[string]any{
				"error_message": errorMessage,
				"from_status":   from,
				"to_status":     models.StatusFailed,
			},
		})
	}
}

func failureContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		return context.WithTimeout(context.Background(), failurePersistenceTimeout)
	}

	return context.WithTimeout(context.WithoutCancel(parent), failurePersistenceTimeout)
}

func (o *Orchestrator) recordStepEvent(
	ctx context.Context,
	task *postgres.Task,
	step *postgres.TaskStep,
	level string,
	eventType string,
	message string,
) {
	o.recordEvent(ctx, taskevents.RecordInput{
		TaskID:    task.ID,
		StepName:  step.StepName,
		Source:    "orchestrator",
		Level:     level,
		EventType: eventType,
		Message:   message,
		Payload: map[string]any{
			"step_id":   step.ID,
			"step_name": step.StepName,
			"status":    step.Status,
		},
	})
}

func (o *Orchestrator) recordArtifactCreated(
	ctx context.Context,
	task *postgres.Task,
	artifact *postgres.TaskArtifact,
	extraPayload map[string]any,
) {
	payload := map[string]any{
		"artifact_id":   artifact.ID,
		"artifact_type": artifact.ArtifactType,
	}
	for key, value := range extraPayload {
		payload[key] = value
	}

	o.recordEvent(ctx, taskevents.RecordInput{
		TaskID:    task.ID,
		Source:    "orchestrator",
		Level:     "info",
		EventType: "artifact.created",
		Message:   "任务产物已生成",
		Payload:   payload,
	})
}

func (o *Orchestrator) recordEvent(ctx context.Context, input taskevents.RecordInput) {
	if o.eventService == nil {
		return
	}

	if _, err := o.eventService.Record(ctx, input); err != nil {
		log.Printf("警告：记录任务事件失败：task=%s event=%s err=%v", input.TaskID, input.EventType, err)
	}
}

func summarizeContent(content string, maxRunes int) string {
	runes := []rune(content)
	if len(runes) <= maxRunes {
		return content
	}

	return string(runes[:maxRunes])
}

func dedupeCitations(input []citation.Citation) []citation.Citation {
	seen := make(map[string]struct{})
	output := make([]citation.Citation, 0, len(input))

	for _, item := range input {
		key := item.ResourceID + "\n" + item.SectionTitle + "\n" + item.Snippet
		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}
		output = append(output, citation.Citation{
			ResourceID:   item.ResourceID,
			SectionTitle: item.SectionTitle,
			Snippet:      item.Snippet,
		})
	}

	for index := range output {
		output[index].CitationID = fmt.Sprintf("cite_%d", index+1)
	}

	return output
}

func validatePlanResult(result *planner.PlanResult) error {
	if result == nil {
		return fmt.Errorf("规划代理返回了空结果")
	}
	if strings.TrimSpace(result.Intent) == "" {
		return fmt.Errorf("规划代理返回的意图为空")
	}
	if len(result.SearchQueries) == 0 {
		return fmt.Errorf("规划代理未返回检索查询")
	}

	return nil
}

// validateDiffPreview 对 DiffPreview 做严格结构性校验。
// no-change 预览（NoChange==true）直接通过，不校验 sections。
func validateDiffPreview(preview *editor.DiffPreview, citations []citation.Citation) error {
	if preview == nil {
		return fmt.Errorf("编辑代理返回了空 diff 预览")
	}

	// no-change 分支不需要 sections，跳过校验
	if preview.NoChange {
		return nil
	}

	if len(preview.Sections) == 0 {
		return fmt.Errorf("diff 预览未包含任何修改章节")
	}

	const maxSections = 50
	const maxFieldRunes = 10000

	if len(preview.Sections) > maxSections {
		return fmt.Errorf("diff 预览章节数量超过上限 %d（实际 %d）", maxSections, len(preview.Sections))
	}

	knownCitationIDs := make(map[string]struct{}, len(citations))
	for _, item := range citations {
		knownCitationIDs[item.CitationID] = struct{}{}
	}

	for i, section := range preview.Sections {
		if strings.TrimSpace(section.SectionTitle) == "" {
			return fmt.Errorf("diff 预览第 %d 个章节的 section_title 为空", i)
		}
		if section.SectionOccurrence <= 0 {
			return fmt.Errorf("diff 预览第 %d 个章节的 section_occurrence 非法", i)
		}
		if strings.TrimSpace(section.Original) == "" {
			return fmt.Errorf("diff 预览第 %d 个章节的 original 为空", i)
		}
		if strings.TrimSpace(section.Revised) == "" {
			return fmt.Errorf("diff 预览第 %d 个章节的 revised 为空", i)
		}
		if strings.TrimSpace(section.Reason) == "" {
			return fmt.Errorf("diff 预览第 %d 个章节的 reason 为空", i)
		}
		if section.Original == section.Revised {
			return fmt.Errorf("diff 预览第 %d 个章节的 original 与 revised 相同", i)
		}
		if len([]rune(section.SectionTitle)) > maxFieldRunes {
			return fmt.Errorf("diff 预览第 %d 个章节的 section_title 超过长度上限", i)
		}
		if len([]rune(section.Original)) > maxFieldRunes {
			return fmt.Errorf("diff 预览第 %d 个章节的 original 超过长度上限", i)
		}
		if len([]rune(section.Revised)) > maxFieldRunes {
			return fmt.Errorf("diff 预览第 %d 个章节的 revised 超过长度上限", i)
		}
		if len(section.CitationIDs) == 0 {
			return fmt.Errorf("diff 预览第 %d 个章节的 citation_ids 为空", i)
		}
		for _, citationID := range section.CitationIDs {
			if _, ok := knownCitationIDs[citationID]; !ok {
				return fmt.Errorf("diff 预览第 %d 个章节引用了未知 citation_id %s", i, citationID)
			}
		}
	}

	return nil
}

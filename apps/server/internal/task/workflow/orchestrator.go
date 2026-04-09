package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agent_project/apps/server/internal/agent/editor"
	"agent_project/apps/server/internal/agent/planner"
	"agent_project/apps/server/internal/agent/reviewer"
	"agent_project/apps/server/internal/knowledge/citation"
	"agent_project/apps/server/internal/knowledge/retriever"
	"agent_project/apps/server/internal/storage/postgres"
	"agent_project/apps/server/internal/task/models"
)

const retrieverLimit = 5

// Orchestrator 负责驱动任务的顶层业务编排。
type Orchestrator struct {
	taskRepo      *postgres.TaskRepo
	resourceRepo  *postgres.ResourceRepo
	approvalRepo  *postgres.ApprovalRepo
	plannerAgent  *planner.Agent
	reviewerAgent *reviewer.Agent
	editorAgent   *editor.Agent
	retrieverSvc  *retriever.Service
}

// New 构造任务编排器。
func New(
	taskRepo *postgres.TaskRepo,
	resourceRepo *postgres.ResourceRepo,
	approvalRepo *postgres.ApprovalRepo,
	plannerAgent *planner.Agent,
	reviewerAgent *reviewer.Agent,
	editorAgent *editor.Agent,
	retrieverSvc *retriever.Service,
) *Orchestrator {
	return &Orchestrator{
		taskRepo:      taskRepo,
		resourceRepo:  resourceRepo,
		approvalRepo:  approvalRepo,
		plannerAgent:  plannerAgent,
		reviewerAgent: reviewerAgent,
		editorAgent:   editorAgent,
		retrieverSvc:  retrieverSvc,
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

	planResult, err := o.plannerAgent.Plan(ctx, task.Instruction, resource.Title, resourceSummary)
	if err != nil {
		o.failTask(ctx, task, plannerStep, err)
		return
	}
	if err := validatePlanResult(planResult); err != nil {
		o.failTask(ctx, task, plannerStep, err)
		return
	}
	if err := o.taskRepo.UpdateStep(ctx, plannerStep.ID, "completed", nil); err != nil {
		o.failTask(ctx, task, plannerStep, err)
		return
	}

	if err := o.transitionTask(ctx, task, models.StatusRetrieving, nil); err != nil {
		o.failTask(ctx, task, nil, err)
		return
	}

	retrieverStep, err := o.taskRepo.AddStep(ctx, task.ID, models.StepRetriever)
	if err != nil {
		o.failTask(ctx, task, nil, err)
		return
	}

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
	if _, err := o.taskRepo.AddArtifact(ctx, task.ID, "citations", citationsContent); err != nil {
		o.failTask(ctx, task, retrieverStep, err)
		return
	}
	if err := o.taskRepo.UpdateStep(ctx, retrieverStep.ID, "completed", nil); err != nil {
		o.failTask(ctx, task, retrieverStep, err)
		return
	}

	if err := o.transitionTask(ctx, task, models.StatusDrafting, nil); err != nil {
		o.failTask(ctx, task, nil, err)
		return
	}

	reviewerStep, err := o.taskRepo.AddStep(ctx, task.ID, models.StepReviewer)
	if err != nil {
		o.failTask(ctx, task, nil, err)
		return
	}

	reviewSummary, err := o.reviewerAgent.Review(ctx, version.Content, citations, planResult.Intent)
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
	if _, err := o.taskRepo.AddArtifact(ctx, task.ID, "review_summary", reviewSummaryContent); err != nil {
		o.failTask(ctx, task, reviewerStep, err)
		return
	}
	if err := o.taskRepo.UpdateStep(ctx, reviewerStep.ID, "completed", nil); err != nil {
		o.failTask(ctx, task, reviewerStep, err)
		return
	}

	editorStep, err := o.taskRepo.AddStep(ctx, task.ID, models.StepEditor)
	if err != nil {
		o.failTask(ctx, task, nil, err)
		return
	}

	diffPreview, err := o.editorAgent.Edit(ctx, version.Content, reviewSummary, citations)
	if err != nil {
		o.failTask(ctx, task, editorStep, err)
		return
	}
	if err := validateDiffPreview(diffPreview, citations); err != nil {
		o.failTask(ctx, task, editorStep, err)
		return
	}

	diffPreviewContent, err := json.Marshal(diffPreview)
	if err != nil {
		o.failTask(ctx, task, editorStep, err)
		return
	}
	if _, err := o.taskRepo.AddArtifact(ctx, task.ID, "diff_preview", diffPreviewContent); err != nil {
		o.failTask(ctx, task, editorStep, err)
		return
	}
	if err := o.taskRepo.UpdateStep(ctx, editorStep.ID, "completed", nil); err != nil {
		o.failTask(ctx, task, editorStep, err)
		return
	}

	if _, err := o.approvalRepo.Create(ctx, task.ID); err != nil {
		o.failTask(ctx, task, nil, err)
		return
	}

	if err := o.transitionTask(ctx, task, models.StatusAwaitingApproval, nil); err != nil {
		o.failTask(ctx, task, nil, err)
		return
	}
}

func (o *Orchestrator) transitionTask(ctx context.Context, task *postgres.Task, to string, errorMessage *string) error {
	if err := models.Transition(task.Status, to); err != nil {
		return err
	}

	if err := o.taskRepo.UpdateStatus(ctx, task.ID, to, errorMessage); err != nil {
		return err
	}

	task.Status = to
	task.ErrorMessage = errorMessage
	return nil
}

func (o *Orchestrator) failTask(ctx context.Context, task *postgres.Task, step *postgres.TaskStep, cause error) {
	if step != nil {
		errorMessage := cause.Error()
		_ = o.taskRepo.UpdateStep(ctx, step.ID, "failed", &errorMessage)
	}

	errorMessage := cause.Error()
	if err := models.Transition(task.Status, models.StatusFailed); err == nil {
		_ = o.taskRepo.UpdateStatus(ctx, task.ID, models.StatusFailed, &errorMessage)
		task.Status = models.StatusFailed
		task.ErrorMessage = &errorMessage
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

func validateDiffPreview(preview *editor.DiffPreview, citations []citation.Citation) error {
	if preview == nil {
		return fmt.Errorf("编辑代理返回了空 diff 预览")
	}

	knownCitationIDs := make(map[string]struct{}, len(citations))
	for _, item := range citations {
		knownCitationIDs[item.CitationID] = struct{}{}
	}

	for index, section := range preview.Sections {
		if len(section.CitationIDs) == 0 {
			return fmt.Errorf("diff 预览第 %d 个章节的 citation_ids 为空", index)
		}

		for _, citationID := range section.CitationIDs {
			if _, ok := knownCitationIDs[citationID]; !ok {
				return fmt.Errorf("diff 预览第 %d 个章节引用了未知 citation_id %s", index, citationID)
			}
		}
	}

	return nil
}

package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agent_project/apps/server/internal/agent/editor"
	"agent_project/apps/server/internal/knowledge/indexer"
	"agent_project/apps/server/internal/knowledge/sections"
	"agent_project/apps/server/internal/storage/postgres"
)

type taskReader interface {
	GetArtifacts(ctx context.Context, taskID string) ([]postgres.TaskArtifact, error)
	GetByID(ctx context.Context, id string) (*postgres.Task, error)
}

type resourceVersionStore interface {
	GetByID(ctx context.Context, id string) (*postgres.Resource, error)
	GetCurrentVersion(ctx context.Context, resourceID string) (*postgres.ResourceVersion, error)
	GetVersionByID(ctx context.Context, versionID string) (*postgres.ResourceVersion, error)
	CreateVersion(ctx context.Context, resourceID string, versionNumber int, content string, source string) (*postgres.ResourceVersion, error)
}

type versionIndexer interface {
	BuildVersionChunks(ctx context.Context, input indexer.Input) ([]postgres.ResourceChunkInput, error)
	ReindexVersion(ctx context.Context, input indexer.Input) error
}

// PreparedExecution 表示事务外 prepare 阶段构造出的待提交结果。
type PreparedExecution struct {
	Resource       postgres.Resource
	Task           postgres.Task
	BaseVersion    postgres.ResourceVersion
	NewContent     string
	PreparedChunks []postgres.ResourceChunkInput
}

// Executor 负责把 diff_preview 应用到当前文档版本并创建新版本。
type Executor struct {
	taskRepo     taskReader
	resourceRepo resourceVersionStore
	indexer      versionIndexer
}

// New 构造执行代理。
func New(taskRepo taskReader, resourceRepo resourceVersionStore, indexer versionIndexer) *Executor {
	return &Executor{
		taskRepo:     taskRepo,
		resourceRepo: resourceRepo,
		indexer:      indexer,
	}
}

// Execute 读取任务产物中的 diff_preview，并将修订内容落成新的资源版本。
func (e *Executor) Execute(ctx context.Context, job *postgres.ExecutionJob) (string, error) {
	if job == nil {
		return "", fmt.Errorf("执行作业不能为空")
	}

	artifacts, err := e.taskRepo.GetArtifacts(ctx, job.TaskID)
	if err != nil {
		return "", err
	}

	preview, err := extractDiffPreview(artifacts)
	if err != nil {
		return "", err
	}

	task, err := e.taskRepo.GetByID(ctx, job.TaskID)
	if err != nil {
		return "", err
	}
	if task == nil {
		return "", fmt.Errorf("任务不存在")
	}

	resource, err := e.resourceRepo.GetByID(ctx, task.ResourceID)
	if err != nil {
		return "", err
	}
	if resource == nil {
		return "", fmt.Errorf("资源不存在")
	}

	currentVersion, err := e.resourceRepo.GetCurrentVersion(ctx, task.ResourceID)
	if err != nil {
		return "", err
	}
	if currentVersion == nil {
		return "", fmt.Errorf("资源当前版本不存在")
	}

	newContent, matchedTitles := applySectionReplacementsDetailed(currentVersion.Content, preview.Sections)
	if len(matchedTitles) != len(preview.Sections) {
		missingTitles := make([]string, 0, len(preview.Sections)-len(matchedTitles))
		for _, section := range preview.Sections {
			if _, ok := matchedTitles[section.SectionTitle]; ok {
				continue
			}

			missingTitles = append(missingTitles, section.SectionTitle)
		}

		return "", fmt.Errorf("文档中未找到 diff 预览对应章节：%s", strings.Join(missingTitles, ", "))
	}

	newVersion, err := e.resourceRepo.CreateVersion(
		ctx,
		task.ResourceID,
		currentVersion.VersionNumber+1,
		newContent,
		"agent_edit",
	)
	if err != nil {
		return "", err
	}

	if err := e.indexer.ReindexVersion(ctx, indexer.Input{
		Resource: *resource,
		Version:  *newVersion,
	}); err != nil {
		return "", err
	}

	return newVersion.ID, nil
}

// Prepare 基于 job.base_version_id 和 diff_preview 构造待提交的新正文与 chunks。
func (e *Executor) Prepare(ctx context.Context, job *postgres.ExecutionJob) (*PreparedExecution, error) {
	if job == nil {
		return nil, fmt.Errorf("执行作业不能为空")
	}
	if job.BaseVersionID == nil || strings.TrimSpace(*job.BaseVersionID) == "" {
		return nil, fmt.Errorf("legacy job 缺少 base_version_id")
	}

	artifacts, err := e.taskRepo.GetArtifacts(ctx, job.TaskID)
	if err != nil {
		return nil, err
	}

	preview, err := extractDiffPreview(artifacts)
	if err != nil {
		return nil, err
	}

	task, err := e.taskRepo.GetByID(ctx, job.TaskID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("任务不存在")
	}

	resource, err := e.resourceRepo.GetByID(ctx, task.ResourceID)
	if err != nil {
		return nil, err
	}
	if resource == nil {
		return nil, fmt.Errorf("资源不存在")
	}

	baseVersion, err := e.resourceRepo.GetVersionByID(ctx, *job.BaseVersionID)
	if err != nil {
		return nil, err
	}
	if baseVersion == nil {
		return nil, fmt.Errorf("base version 不存在")
	}

	newContent, err := applyPreparedRevisions(baseVersion.Content, preview.Sections)
	if err != nil {
		return nil, err
	}

	preparedVersion := *baseVersion
	preparedVersion.Content = newContent
	inputChunks, err := e.indexer.BuildVersionChunks(ctx, indexer.Input{
		Resource: *resource,
		Version:  preparedVersion,
	})
	if err != nil {
		return nil, err
	}

	return &PreparedExecution{
		Resource:       *resource,
		Task:           *task,
		BaseVersion:    *baseVersion,
		NewContent:     newContent,
		PreparedChunks: inputChunks,
	}, nil
}

// extractDiffPreview 从现有内容里提取 `差异预览`，避免调用方重复解析同一份数据。
func extractDiffPreview(artifacts []postgres.TaskArtifact) (*editor.DiffPreview, error) {
	for _, artifact := range artifacts {
		if artifact.ArtifactType != "diff_preview" {
			continue
		}

		var preview editor.DiffPreview
		if err := json.Unmarshal(artifact.Content, &preview); err != nil {
			return nil, fmt.Errorf("解析 diff 预览失败：%w", err)
		}

		return &preview, nil
	}

	return nil, fmt.Errorf("未找到 diff 预览产物")
}

// applySectionReplacements 把模型返回的 section 修改应用到原始文档内容，输出替换后的完整文本。
func applySectionReplacements(content string, sections []editor.DiffSection) string {
	updated, _ := applySectionReplacementsDetailed(content, sections)
	return updated
}

// applyPreparedRevisions 按已匹配好的修订列表批量改写文档内容，统一处理偏移修正。
func applyPreparedRevisions(content string, diffs []editor.DiffSection) (string, error) {
	if len(diffs) == 0 {
		return content, nil
	}

	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	parsedSections := sections.ParseMarkdown(normalized)
	if len(parsedSections) == 0 {
		return content, fmt.Errorf("文档中未找到可替换的章节")
	}

	type replacement struct {
		section sections.Section
		diff    editor.DiffSection
	}

	replacements := make(map[string]replacement, len(diffs))
	for _, diff := range diffs {
		matchedSection, err := matchDiffSection(parsedSections, diff)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(diff.Original) != matchedSection.Body {
			return "", fmt.Errorf("章节 %q 的 original 与 base version 不匹配", diff.SectionTitle)
		}

		replacements[sectionKey(matchedSection.Title, matchedSection.Occurrence)] = replacement{
			section: matchedSection,
			diff:    diff,
		}
	}

	result := make([]string, 0, len(lines))
	cursor := 0
	for _, section := range parsedSections {
		result = append(result, lines[cursor:section.StartLine]...)

		key := sectionKey(section.Title, section.Occurrence)
		replacement, ok := replacements[key]
		if !ok {
			result = append(result, lines[section.StartLine:section.EndLine]...)
			cursor = section.EndLine
			continue
		}

		if section.Heading == "" {
			result = append(result, normalizeWholeDocumentRevisedLines(replacement.diff.Revised)...)
			cursor = section.EndLine
			continue
		}

		result = append(result, lines[section.StartLine])
		result = append(result, normalizeRevisedLines(section.Title, replacement.diff.Revised)...)
		for blankLine := 0; blankLine < countTrailingBlankLines(lines[section.StartLine+1:section.EndLine]); blankLine++ {
			result = append(result, "")
		}
		cursor = section.EndLine
	}

	result = append(result, lines[cursor:]...)
	return strings.Join(result, "\n"), nil
}

// matchDiffSection 为单个差异片段定位它对应的 section 范围，便于后续按 section 回写。
func matchDiffSection(parsed []sections.Section, diff editor.DiffSection) (sections.Section, error) {
	candidates := make([]sections.Section, 0)
	for _, section := range parsed {
		if section.Title != strings.TrimSpace(diff.SectionTitle) {
			continue
		}

		candidates = append(candidates, section)
	}

	if len(candidates) == 0 {
		return sections.Section{}, fmt.Errorf("文档中未找到 diff 预览对应章节：%s", diff.SectionTitle)
	}

	if diff.SectionOccurrence <= 0 {
		if len(candidates) == 1 {
			return candidates[0], nil
		}
		return sections.Section{}, fmt.Errorf("legacy diff_preview 缺少 section_occurrence，章节 %q 存在重复标题", diff.SectionTitle)
	}

	for _, candidate := range candidates {
		if candidate.Occurrence == diff.SectionOccurrence {
			return candidate, nil
		}
	}

	return sections.Section{}, fmt.Errorf("文档中未找到 diff 预览对应章节：%s#%d", diff.SectionTitle, diff.SectionOccurrence)
}

// sectionKey 为 section 生成稳定比较键，便于比对和合并修订。
func sectionKey(title string, occurrence int) string {
	return title + "\n" + fmt.Sprintf("%d", occurrence)
}

// applySectionReplacementsDetailed 把 `sectionReplacementsDetailed` 应用到当前状态或内容上，集中处理变更合并逻辑。
func applySectionReplacementsDetailed(content string, sections []editor.DiffSection) (string, map[string]struct{}) {
	if len(sections) == 0 {
		return content, map[string]struct{}{}
	}

	lines := strings.Split(content, "\n")
	bounds := scanSectionBounds(lines)
	replacements := make(map[string]editor.DiffSection, len(sections))
	for _, section := range sections {
		replacements[strings.TrimSpace(section.SectionTitle)] = section
	}

	result := make([]string, 0, len(lines))
	matchedTitles := make(map[string]struct{}, len(sections))

	for index := 0; index < len(lines); {
		title := extractSectionTitle(lines[index])
		if title == "" {
			result = append(result, lines[index])
			index++
			continue
		}

		bound, ok := bounds[title]
		if !ok {
			result = append(result, lines[index])
			index++
			continue
		}

		replacement, ok := replacements[title]
		if !ok {
			result = append(result, lines[index:bound.end]...)
			index = bound.end
			continue
		}

		matchedTitles[title] = struct{}{}
		result = append(result, lines[bound.heading])

		revisedLines := normalizeRevisedLines(title, replacement.Revised)
		result = append(result, revisedLines...)
		for blankLine := 0; blankLine < bound.trailingBlankLines; blankLine++ {
			result = append(result, "")
		}

		index = bound.end
	}

	return strings.Join(result, "\n"), matchedTitles
}

// sectionBound 表示section的边界信息，供定位和裁剪逻辑复用。
type sectionBound struct {
	heading            int
	end                int
	trailingBlankLines int
}

// scanSectionBounds 把当前数据库行扫描成 `sectionBounds`，统一查询结果到领域结构的映射。
func scanSectionBounds(lines []string) map[string]sectionBound {
	bounds := make(map[string]sectionBound)

	currentTitle := ""
	currentHeading := -1
	for index, line := range lines {
		title := extractSectionTitle(line)
		if title == "" {
			continue
		}

		if currentTitle != "" {
			bounds[currentTitle] = sectionBound{
				heading:            currentHeading,
				end:                index,
				trailingBlankLines: countTrailingBlankLines(lines[currentHeading+1 : index]),
			}
		}

		currentTitle = title
		currentHeading = index
	}

	if currentTitle != "" {
		bounds[currentTitle] = sectionBound{
			heading:            currentHeading,
			end:                len(lines),
			trailingBlankLines: countTrailingBlankLines(lines[currentHeading+1:]),
		}
	}

	return bounds
}

// extractSectionTitle 从现有内容里提取 `section标题`，避免调用方重复解析同一份数据。
func extractSectionTitle(line string) string {
	if !strings.HasPrefix(line, "## ") {
		return ""
	}

	return strings.TrimSpace(strings.TrimPrefix(line, "## "))
}

// normalizeRevisedLines 归一化 `Revised行`，避免后续流程重复处理边界输入。
func normalizeRevisedLines(title string, revised string) []string {
	normalized := strings.ReplaceAll(revised, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")

	if len(lines) > 0 && extractSectionTitle(strings.TrimSpace(lines[0])) == title {
		lines = lines[1:]
	}

	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	return lines
}

// normalizeWholeDocumentRevisedLines 归一化 `整体文档Revised行`，避免后续流程重复处理边界输入。
func normalizeWholeDocumentRevisedLines(revised string) []string {
	normalized := strings.TrimSpace(strings.ReplaceAll(revised, "\r\n", "\n"))
	if normalized == "" {
		return nil
	}

	return strings.Split(normalized, "\n")
}

// countTrailingBlankLines 统计 `TrailingBlank行`，把数量计算逻辑集中在单点。
func countTrailingBlankLines(lines []string) int {
	count := 0
	for index := len(lines) - 1; index >= 0; index-- {
		if strings.TrimSpace(lines[index]) != "" {
			break
		}

		count++
	}

	return count
}

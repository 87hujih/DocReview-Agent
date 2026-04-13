package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agent_project/apps/server/internal/agent/editor"
	"agent_project/apps/server/internal/knowledge/indexer"
	"agent_project/apps/server/internal/storage/postgres"
)

type taskReader interface {
	GetArtifacts(ctx context.Context, taskID string) ([]postgres.TaskArtifact, error)
	GetByID(ctx context.Context, id string) (*postgres.Task, error)
}

type resourceVersionStore interface {
	GetByID(ctx context.Context, id string) (*postgres.Resource, error)
	GetCurrentVersion(ctx context.Context, resourceID string) (*postgres.ResourceVersion, error)
	CreateVersion(ctx context.Context, resourceID string, versionNumber int, content string, source string) (*postgres.ResourceVersion, error)
}

type versionIndexer interface {
	ReindexVersion(ctx context.Context, input indexer.Input) error
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

func applySectionReplacements(content string, sections []editor.DiffSection) string {
	updated, _ := applySectionReplacementsDetailed(content, sections)
	return updated
}

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

type sectionBound struct {
	heading            int
	end                int
	trailingBlankLines int
}

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

func extractSectionTitle(line string) string {
	if !strings.HasPrefix(line, "## ") {
		return ""
	}

	return strings.TrimSpace(strings.TrimPrefix(line, "## "))
}

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

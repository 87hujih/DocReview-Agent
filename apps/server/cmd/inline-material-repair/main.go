package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	appconfig "agent_project/apps/server/internal/config"
	"agent_project/apps/server/internal/datarepair"
	"agent_project/apps/server/internal/knowledge/embedder"
	"agent_project/apps/server/internal/knowledge/indexer"
	"agent_project/apps/server/internal/storage/postgres"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const dryRunPreviewLimit = 20

// repairMode 承载一次性历史修复命令的运行模式；默认 dry-run，只有显式 `--apply` 才会真正写库。
type repairMode struct {
	Apply      bool
	ResourceID string
}

// resourceVersionCandidate 保存待修复的内联正文版本及其修复后内容。
type resourceVersionCandidate struct {
	VersionID          string
	ResourceID         string
	ResourceTitle      string
	ResourceSourceType string
	VersionNumber      int
	VersionSource      string
	OriginalContent    string
	RepairedContent    string
}

// diffPreviewArtifactCandidate 保存待修复的 diff_preview artifact 及其修复后 JSON。
type diffPreviewArtifactCandidate struct {
	ArtifactID      string
	TaskID          string
	ResourceID      string
	OriginalContent []byte
	RepairedContent []byte
}

// repairScanResult 汇总一次扫描中发现的全部候选。
type repairScanResult struct {
	VersionCandidates  []resourceVersionCandidate
	ArtifactCandidates []diffPreviewArtifactCandidate
}

// main 负责串起参数解析、候选扫描、dry-run 输出和显式 apply 写回。
func main() {
	mode, err := parseRepairMode(os.Args[1:])
	if err != nil {
		log.Fatalf("参数无效：%v", err)
	}

	cfg := appconfig.Load()
	if err := validateForRepair(cfg); err != nil {
		log.Fatalf("配置无效：%v", err)
	}

	ctx := context.Background()
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("数据库连接失败：%v", err)
	}
	defer pool.Close()

	if err := postgres.RunMigrations(ctx, pool); err != nil {
		log.Fatalf("数据库迁移失败：%v", err)
	}

	scanResult, err := scanRepairCandidates(ctx, pool, mode)
	if err != nil {
		log.Fatalf("扫描候选失败：%v", err)
	}

	logRepairPlan(scanResult, mode)
	if !mode.Apply {
		log.Printf("dry-run 结束；如需真正写库，请追加 --apply")
		return
	}

	appliedVersions, appliedArtifacts, err := applyRepairCandidates(ctx, pool, cfg, scanResult)
	if err != nil {
		log.Fatalf("写回修复失败：%v", err)
	}

	log.Printf("修复完成：resource_versions=%d，task_artifacts=%d", appliedVersions, appliedArtifacts)
}

// parseRepairMode 解析命令参数；默认 dry-run，可按资源收敛范围，并要求资源过滤必须是合法 UUID。
func parseRepairMode(args []string) (repairMode, error) {
	fs := flag.NewFlagSet("inline-material-repair", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var mode repairMode
	fs.BoolVar(&mode.Apply, "apply", false, "显式执行写库修复，默认仅 dry-run")
	fs.StringVar(&mode.ResourceID, "resource-id", "", "只扫描并修复指定资源")

	if err := fs.Parse(args); err != nil {
		return repairMode{}, err
	}

	mode.ResourceID = strings.TrimSpace(mode.ResourceID)
	if mode.ResourceID != "" {
		if _, err := uuid.Parse(mode.ResourceID); err != nil {
			return repairMode{}, fmt.Errorf("resource-id 非法：%s", mode.ResourceID)
		}
	}

	return mode, nil
}

// validateForRepair 校验历史修复命令最小所需配置；dry-run 只要求数据库连接即可。
func validateForRepair(cfg appconfig.Config) error {
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		return fmt.Errorf("缺少必填配置：DATABASE_URL")
	}

	return nil
}

// validateForReindexing 校验需要重建 chunk 时的 embedding 配置，避免修到一半才因模型配置缺失失败。
func validateForReindexing(cfg appconfig.Config) error {
	var missing []string
	if strings.TrimSpace(cfg.SiliconFlowAPIKey) == "" {
		missing = append(missing, "SILICONFLOW_API_KEY")
	}
	if strings.TrimSpace(cfg.EmbeddingModel) == "" {
		missing = append(missing, "EMBEDDING_MODEL")
	}
	if len(missing) > 0 {
		return fmt.Errorf("缺少必填配置：%s", strings.Join(missing, ", "))
	}
	if cfg.EmbeddingDim <= 0 {
		return fmt.Errorf("EMBEDDING_DIM 无效：%d", cfg.EmbeddingDim)
	}

	return nil
}

// scanRepairCandidates 扫描内联正文版本和历史 diff_preview，找出真正需要修复的候选。
func scanRepairCandidates(ctx context.Context, pool *pgxpool.Pool, mode repairMode) (*repairScanResult, error) {
	versionCandidates, err := listResourceVersionCandidates(ctx, pool, mode.ResourceID)
	if err != nil {
		return nil, err
	}

	artifactCandidates, err := listDiffPreviewArtifactCandidates(ctx, pool, mode.ResourceID)
	if err != nil {
		return nil, err
	}

	return &repairScanResult{
		VersionCandidates:  versionCandidates,
		ArtifactCandidates: artifactCandidates,
	}, nil
}

// listResourceVersionCandidates 返回真正命中“历史内联正文尾巴污染”规则的版本集合。
func listResourceVersionCandidates(ctx context.Context, pool *pgxpool.Pool, resourceID string) ([]resourceVersionCandidate, error) {
	query := `
		SELECT rv.id,
		       rv.resource_id,
		       r.title,
		       r.source_type,
		       rv.version_number,
		       rv.source,
		       rv.content
		FROM resource_versions rv
		JOIN resources r ON r.id = rv.resource_id
		WHERE r.source_type = 'inline_text'
		  AND rv.source = 'assistant_inline_text'
	`
	args := make([]any, 0, 1)
	if strings.TrimSpace(resourceID) != "" {
		query += ` AND rv.resource_id = $1`
		args = append(args, resourceID)
	}
	query += `
		ORDER BY rv.created_at ASC, rv.id ASC
	`

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	candidates := make([]resourceVersionCandidate, 0)
	for rows.Next() {
		var (
			candidate resourceVersionCandidate
			content   string
		)
		if err := rows.Scan(
			&candidate.VersionID,
			&candidate.ResourceID,
			&candidate.ResourceTitle,
			&candidate.ResourceSourceType,
			&candidate.VersionNumber,
			&candidate.VersionSource,
			&content,
		); err != nil {
			return nil, err
		}

		repairedContent, changed := datarepair.RepairInlineMaterialBody(content)
		if !changed {
			continue
		}

		candidate.OriginalContent = content
		candidate.RepairedContent = repairedContent
		candidates = append(candidates, candidate)
	}

	return candidates, rows.Err()
}

// listDiffPreviewArtifactCandidates 返回真正命中“history diff_preview.original 被污染”规则的任务产物集合。
func listDiffPreviewArtifactCandidates(ctx context.Context, pool *pgxpool.Pool, resourceID string) ([]diffPreviewArtifactCandidate, error) {
	query := `
		SELECT ta.id,
		       ta.task_id,
		       t.resource_id,
		       ta.content
		FROM task_artifacts ta
		JOIN tasks t ON t.id = ta.task_id
		JOIN resources r ON r.id = t.resource_id
		WHERE ta.artifact_type = 'diff_preview'
		  AND r.source_type = 'inline_text'
	`
	args := make([]any, 0, 1)
	if strings.TrimSpace(resourceID) != "" {
		query += ` AND t.resource_id = $1`
		args = append(args, resourceID)
	}
	query += `
		ORDER BY ta.created_at ASC, ta.id ASC
	`

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	candidates := make([]diffPreviewArtifactCandidate, 0)
	for rows.Next() {
		var candidate diffPreviewArtifactCandidate
		if err := rows.Scan(
			&candidate.ArtifactID,
			&candidate.TaskID,
			&candidate.ResourceID,
			&candidate.OriginalContent,
		); err != nil {
			return nil, err
		}

		repairedContent, changed, err := datarepair.RepairDiffPreviewArtifact(candidate.OriginalContent)
		if err != nil {
			return nil, err
		}
		if !changed {
			continue
		}

		candidate.RepairedContent = repairedContent
		candidates = append(candidates, candidate)
	}

	return candidates, rows.Err()
}

// applyRepairCandidates 显式把候选写回数据库，并为被修复版本重建 chunk，保证检索和原文基线一致。
func applyRepairCandidates(
	ctx context.Context,
	pool *pgxpool.Pool,
	cfg appconfig.Config,
	scanResult *repairScanResult,
) (int, int, error) {
	if scanResult == nil {
		return 0, 0, nil
	}

	resourceRepo := postgres.NewResourceRepo(pool)
	structureRepo := postgres.NewResourceStructureRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)

	var versionIndexer *indexer.Service
	if len(scanResult.VersionCandidates) > 0 {
		if err := validateForReindexing(cfg); err != nil {
			return 0, 0, err
		}

		emb, err := embedder.New(ctx, cfg.SiliconFlowBaseURL, cfg.SiliconFlowAPIKey, cfg.EmbeddingModel, cfg.EmbeddingDim)
		if err != nil {
			return 0, 0, err
		}
		versionIndexer = indexer.NewService(resourceRepo, emb)
	}

	appliedVersions := 0
	for _, candidate := range scanResult.VersionCandidates {
		sections, err := structureRepo.ListSectionsByVersion(ctx, candidate.VersionID)
		if err != nil {
			return appliedVersions, 0, err
		}

		chunks, err := versionIndexer.BuildVersionChunks(ctx, indexer.Input{
			Resource: postgres.Resource{
				ID:         candidate.ResourceID,
				Title:      candidate.ResourceTitle,
				SourceType: candidate.ResourceSourceType,
			},
			Version: postgres.ResourceVersion{
				ID:            candidate.VersionID,
				ResourceID:    candidate.ResourceID,
				VersionNumber: candidate.VersionNumber,
				Content:       candidate.RepairedContent,
				Source:        candidate.VersionSource,
			},
			Sections: sections,
		})
		if err != nil {
			return appliedVersions, 0, err
		}

		if err := resourceRepo.ReplaceVersionContentAndChunks(ctx, candidate.VersionID, candidate.ResourceID, candidate.RepairedContent, chunks); err != nil {
			return appliedVersions, 0, err
		}
		appliedVersions++
	}

	appliedArtifacts := 0
	for _, candidate := range scanResult.ArtifactCandidates {
		if err := taskRepo.UpdateArtifactContent(ctx, candidate.ArtifactID, candidate.RepairedContent); err != nil {
			return appliedVersions, appliedArtifacts, err
		}
		appliedArtifacts++
	}

	return appliedVersions, appliedArtifacts, nil
}

// logRepairPlan 打印 dry-run 或 apply 前的候选摘要，便于先确认 blast radius。
func logRepairPlan(scanResult *repairScanResult, mode repairMode) {
	if scanResult == nil {
		log.Printf("未发现待处理候选")
		return
	}

	scope := "全量资源"
	if strings.TrimSpace(mode.ResourceID) != "" {
		scope = mode.ResourceID
	}
	log.Printf(
		"扫描完成：scope=%s apply=%t resource_versions=%d task_artifacts=%d",
		scope,
		mode.Apply,
		len(scanResult.VersionCandidates),
		len(scanResult.ArtifactCandidates),
	)

	for index, candidate := range scanResult.VersionCandidates {
		if index >= dryRunPreviewLimit {
			log.Printf("resource_versions 预览已截断：剩余 %d 条", len(scanResult.VersionCandidates)-dryRunPreviewLimit)
			break
		}

		log.Printf(
			"[resource_version] resource=%s version=%s title=%q before=%q after=%q",
			candidate.ResourceID,
			candidate.VersionID,
			candidate.ResourceTitle,
			tailPreview(candidate.OriginalContent),
			tailPreview(candidate.RepairedContent),
		)
	}

	for index, candidate := range scanResult.ArtifactCandidates {
		if index >= dryRunPreviewLimit {
			log.Printf("task_artifacts 预览已截断：剩余 %d 条", len(scanResult.ArtifactCandidates)-dryRunPreviewLimit)
			break
		}

		log.Printf(
			"[task_artifact] task=%s artifact=%s resource=%s",
			candidate.TaskID,
			candidate.ArtifactID,
			candidate.ResourceID,
		)
	}
}

// tailPreview 返回压缩空白后的尾部预览，便于 dry-run 时快速确认脏尾巴是否被准确切掉。
func tailPreview(value string) string {
	compact := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if compact == "" {
		return ""
	}

	runes := []rune(compact)
	if len(runes) <= 96 {
		return compact
	}

	return "..." + string(runes[len(runes)-96:])
}

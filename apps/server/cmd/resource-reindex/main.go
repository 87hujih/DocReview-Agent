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
	"agent_project/apps/server/internal/knowledge/embedder"
	"agent_project/apps/server/internal/knowledge/indexer"
	"agent_project/apps/server/internal/storage/postgres"

	"github.com/google/uuid"
)

// reindexMode 承载资源重建索引命令的目标选择结果，明确本次运行是按资源重建还是补齐缺失 current version。
type reindexMode struct {
	ResourceID     string
	MissingCurrent bool
}

// main 作为当前命令的入口，负责串起参数读取、依赖初始化和主流程执行。
func main() {
	mode, err := parseReindexMode(os.Args[1:])
	if err != nil {
		log.Fatalf("参数无效：%v", err)
	}

	cfg := appconfig.Load()
	if err := validateForReindex(cfg); err != nil {
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

	resourceRepo := postgres.NewResourceRepo(pool)
	resourceStructureRepo := postgres.NewResourceStructureRepo(pool)
	emb, err := embedder.New(ctx, cfg.SiliconFlowBaseURL, cfg.SiliconFlowAPIKey, cfg.EmbeddingModel, cfg.EmbeddingDim)
	if err != nil {
		log.Fatalf("向量嵌入器初始化失败：%v", err)
	}
	versionIndexer := indexer.NewService(resourceRepo, emb)

	if mode.ResourceID != "" {
		if err := reindexSingleCurrentVersion(ctx, resourceRepo, resourceStructureRepo, versionIndexer, mode.ResourceID); err != nil {
			log.Fatalf("重建资源索引失败：%v", err)
		}
		log.Printf("资源当前版本索引已重建：%s", mode.ResourceID)
		return
	}

	count, err := reindexMissingCurrentVersions(ctx, resourceRepo, resourceStructureRepo, versionIndexer)
	if err != nil {
		log.Fatalf("重建缺失索引失败：%v", err)
	}
	log.Printf("缺失索引修复完成：%d 个资源", count)
}

// parseReindexMode 解析命令行里的重建目标参数，并保证 `--resource-id` 与 `--missing-current` 只能二选一。
func parseReindexMode(args []string) (reindexMode, error) {
	fs := flag.NewFlagSet("resource-reindex", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var mode reindexMode
	fs.StringVar(&mode.ResourceID, "resource-id", "", "重建指定资源当前版本索引")
	fs.BoolVar(&mode.MissingCurrent, "missing-current", false, "重建当前版本缺少 chunk 的资源索引")

	if err := fs.Parse(args); err != nil {
		return reindexMode{}, err
	}

	mode.ResourceID = strings.TrimSpace(mode.ResourceID)
	hasResourceID := mode.ResourceID != ""
	if hasResourceID == mode.MissingCurrent {
		return reindexMode{}, fmt.Errorf("必须且只能指定 --resource-id 或 --missing-current")
	}

	if hasResourceID {
		if _, err := uuid.Parse(mode.ResourceID); err != nil {
			return reindexMode{}, fmt.Errorf("resource-id 非法：%s", mode.ResourceID)
		}
	}

	return mode, nil
}

// validateForReindex 校验运行 reindex 所需的数据库和向量检索配置，避免命令启动后才在深层链路失败。
func validateForReindex(cfg appconfig.Config) error {
	var missing []string
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		missing = append(missing, "DATABASE_URL")
	}
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

// reindexSingleCurrentVersion 按资源 ID 读取当前版本并重建该资源的 section 与 chunk 索引。
func reindexSingleCurrentVersion(ctx context.Context, repo *postgres.ResourceRepo, structureRepo *postgres.ResourceStructureRepo, versionIndexer *indexer.Service, resourceID string) error {
	resource, err := repo.GetByID(ctx, resourceID)
	if err != nil {
		return err
	}
	if resource == nil {
		return fmt.Errorf("资源不存在：%s", resourceID)
	}

	version, err := repo.GetCurrentVersion(ctx, resourceID)
	if err != nil {
		return err
	}
	if version == nil {
		return fmt.Errorf("资源当前版本不存在：%s", resourceID)
	}

	structuredSections, err := structureRepo.ListSectionsByVersion(ctx, version.ID)
	if err != nil {
		return err
	}

	return versionIndexer.ReindexVersion(ctx, indexer.Input{
		Resource: *resource,
		Version:  *version,
		Sections: structuredSections,
	})
}

// reindexMissingCurrentVersions 扫描缺失 current version 结果的资源，并为它们补做索引初始化。
func reindexMissingCurrentVersions(ctx context.Context, repo *postgres.ResourceRepo, structureRepo *postgres.ResourceStructureRepo, versionIndexer *indexer.Service) (int, error) {
	resources, err := repo.List(ctx)
	if err != nil {
		return 0, err
	}

	reindexed := 0
	for _, resource := range resources {
		version, err := repo.GetCurrentVersion(ctx, resource.ID)
		if err != nil {
			return reindexed, err
		}
		if version == nil {
			log.Printf("跳过当前版本不存在的资源：%s", resource.Title)
			continue
		}

		chunkCount, err := repo.CountChunksByVersion(ctx, version.ID)
		if err != nil {
			return reindexed, err
		}
		if chunkCount > 0 {
			continue
		}

		structuredSections, err := structureRepo.ListSectionsByVersion(ctx, version.ID)
		if err != nil {
			return reindexed, err
		}

		if err := versionIndexer.ReindexVersion(ctx, indexer.Input{
			Resource: resource,
			Version:  *version,
			Sections: structuredSections,
		}); err != nil {
			return reindexed, err
		}
		reindexed++
	}

	return reindexed, nil
}

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
	"agent_project/apps/server/internal/knowledge/searchindex"
	"agent_project/apps/server/internal/storage/postgres"

	"github.com/google/uuid"
)

type resourceRepo interface {
	List(ctx context.Context) ([]postgres.Resource, error)
	GetByID(ctx context.Context, id string) (*postgres.Resource, error)
	GetCurrentVersion(ctx context.Context, resourceID string) (*postgres.ResourceVersion, error)
	CountChunksByVersion(ctx context.Context, versionID string) (int, error)
	GetVersionStructureByVersionID(ctx context.Context, versionID string) (*postgres.ResourceVersionStructure, error)
	ListSectionsByVersion(ctx context.Context, versionID string) ([]postgres.ResourceSection, error)
}

type versionReindexer interface {
	ReindexVersion(ctx context.Context, input indexer.Input) error
}

type reindexMode struct {
	ResourceID     string
	MissingCurrent bool
}

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
	emb, err := embedder.New(ctx, cfg.SiliconFlowBaseURL, cfg.SiliconFlowAPIKey, cfg.EmbeddingModel, cfg.EmbeddingDim)
	if err != nil {
		log.Fatalf("向量嵌入器初始化失败：%v", err)
	}
	searchClient := searchindex.NewClient(searchindex.ClientOptions{
		BaseURL:     cfg.OpenSearchURL,
		IndexChunks: cfg.OpenSearchIndexChunks,
		Username:    cfg.OpenSearchUsername,
		Password:    cfg.OpenSearchPassword,
		TLSInsecure: cfg.OpenSearchTLSInsecure,
	})
	versionSync := searchindex.NewSyncService(resourceRepo, searchClient, cfg.SearchBackend)
	versionIndexer := indexer.NewService(resourceRepo, emb, indexer.WithVersionSync(versionSync))

	// 当前 CLI 会重建 PostgreSQL chunks，并在启用 OpenSearch 时同步刷新搜索投影。
	if mode.ResourceID != "" {
		if err := reindexSingleCurrentVersion(ctx, resourceRepo, versionIndexer, mode.ResourceID); err != nil {
			log.Fatalf("重建资源索引失败：%v", err)
		}
		log.Printf("资源当前版本索引已重建：%s", mode.ResourceID)
		return
	}

	count, err := reindexMissingCurrentVersions(ctx, resourceRepo, versionIndexer)
	if err != nil {
		log.Fatalf("重建缺失索引失败：%v", err)
	}
	log.Printf("缺失索引修复完成：%d 个资源", count)
}

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
	if strings.EqualFold(strings.TrimSpace(cfg.SearchBackend), "opensearch_bm25") && strings.TrimSpace(cfg.OpenSearchURL) == "" {
		return fmt.Errorf("缺少必填配置：OPENSEARCH_URL")
	}
	if cfg.EmbeddingDim <= 0 {
		return fmt.Errorf("EMBEDDING_DIM 无效：%d", cfg.EmbeddingDim)
	}

	return nil
}

func reindexSingleCurrentVersion(ctx context.Context, repo resourceRepo, versionIndexer versionReindexer, resourceID string) error {
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

	structure, err := repo.GetVersionStructureByVersionID(ctx, version.ID)
	if err != nil {
		return err
	}

	return versionIndexer.ReindexVersion(ctx, indexer.Input{
		Resource: *resource,
		Version:  *version,
		Sections: loadStructuredSections(ctx, repo, version.ID, structure != nil),
	})
}

func reindexMissingCurrentVersions(ctx context.Context, repo resourceRepo, versionIndexer versionReindexer) (int, error) {
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

		structure, err := repo.GetVersionStructureByVersionID(ctx, version.ID)
		if err != nil {
			return reindexed, err
		}

		if err := versionIndexer.ReindexVersion(ctx, indexer.Input{
			Resource: resource,
			Version:  *version,
			Sections: loadStructuredSections(ctx, repo, version.ID, structure != nil),
		}); err != nil {
			return reindexed, err
		}
		reindexed++
	}

	return reindexed, nil
}

func loadStructuredSections(ctx context.Context, repo resourceRepo, versionID string, structured bool) []postgres.ResourceSectionInput {
	if !structured {
		return nil
	}

	sections, err := repo.ListSectionsByVersion(ctx, versionID)
	if err != nil {
		return nil
	}

	inputs := make([]postgres.ResourceSectionInput, 0, len(sections))
	for _, section := range sections {
		inputs = append(inputs, postgres.ResourceSectionInput{
			SectionID:   section.ID,
			SectionKey:  section.SectionKey,
			SectionType: section.SectionType,
			Title:       section.Title,
			Summary:     section.Summary,
			Content:     section.Content,
			PageStart:   section.PageStart,
			PageEnd:     section.PageEnd,
			Metadata:    section.Metadata,
		})
	}

	return inputs
}

package handlers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agent_project/apps/server/internal/knowledge/citation"
	"agent_project/apps/server/internal/storage/postgres"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"
)

type resourceSearchService interface {
	SearchByResource(ctx context.Context, resourceID string, query string, limit int) ([]citation.Citation, error)
}

// ResourceHandler 暴露由资源存储和语义检索支撑的 HTTP 接口。
type ResourceHandler struct {
	resourceRepo *postgres.ResourceRepo
	retriever    resourceSearchService
}

// resourceSummary 是资源列表和详情接口共享的精简资源视图。
type resourceSummary struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	SourceType string    `json:"source_type"`
	CreatedAt  time.Time `json:"created_at"`
}

// resourceVersionResponse 描述资源详情接口中的当前版本信息。
type resourceVersionResponse struct {
	ID            string    `json:"id"`
	VersionNumber int       `json:"version_number"`
	Content       string    `json:"content"`
	Source        string    `json:"source"`
	CreatedAt     time.Time `json:"created_at"`
}

// listResourcesResponse 是资源列表接口的响应体。
type listResourcesResponse struct {
	Resources []resourceSummary `json:"resources"`
}

// getResourceResponse 是资源详情接口的响应体。
type getResourceResponse struct {
	Resource       resourceSummary          `json:"resource"`
	CurrentVersion *resourceVersionResponse `json:"current_version"`
}

// searchResourcesResponse 是资源内检索接口的响应体。
type searchResourcesResponse struct {
	Query     string              `json:"query"`
	Citations []citation.Citation `json:"citations"`
}

// NewResourceHandler 把资源相关 HTTP handler 依赖的存储层和检索服务接起来。
func NewResourceHandler(repo *postgres.ResourceRepo, ret resourceSearchService) *ResourceHandler {
	return &ResourceHandler{
		resourceRepo: repo,
		retriever:    ret,
	}
}

// List 返回资源浏览页需要的资源摘要列表。
func (h *ResourceHandler) List(requestCtx context.Context, ctx *app.RequestContext) {
	resources, err := h.resourceRepo.List(requestCtx)
	if err != nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "查询资源列表失败"})
		return
	}

	response := listResourcesResponse{
		Resources: make([]resourceSummary, 0, len(resources)),
	}
	for _, resource := range resources {
		response.Resources = append(response.Resources, resourceSummary{
			ID:         resource.ID,
			Title:      resource.Title,
			SourceType: resource.SourceType,
			CreatedAt:  resource.CreatedAt,
		})
	}

	ctx.JSON(consts.StatusOK, response)
}

// GetByID 返回资源详情页需要的资源信息和最新版本。
func (h *ResourceHandler) GetByID(requestCtx context.Context, ctx *app.RequestContext) {
	if h.resourceRepo == nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "资源存储未配置"})
		return
	}

	resourceID, ok := parseResourceIDParam(ctx)
	if !ok {
		return
	}

	resource, err := h.resourceRepo.GetByID(requestCtx, resourceID)
	if err != nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "查询资源失败"})
		return
	}
	if resource == nil {
		ctx.JSON(consts.StatusNotFound, map[string]string{"error": "资源不存在"})
		return
	}

	version, err := h.resourceRepo.GetCurrentVersion(requestCtx, resourceID)
	if err != nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "查询资源版本失败"})
		return
	}

	response := getResourceResponse{
		Resource: resourceSummary{
			ID:         resource.ID,
			Title:      resource.Title,
			SourceType: resource.SourceType,
			CreatedAt:  resource.CreatedAt,
		},
	}
	if version != nil {
		response.CurrentVersion = &resourceVersionResponse{
			ID:            version.ID,
			VersionNumber: version.VersionNumber,
			Content:       version.Content,
			Source:        version.Source,
			CreatedAt:     version.CreatedAt,
		}
	}

	ctx.JSON(consts.StatusOK, response)
}

// ExportCurrentVersion 把资源当前版本作为 Markdown 附件返回。
func (h *ResourceHandler) ExportCurrentVersion(requestCtx context.Context, ctx *app.RequestContext) {
	if h.resourceRepo == nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "资源存储未配置"})
		return
	}

	resourceID, ok := parseResourceIDParam(ctx)
	if !ok {
		return
	}

	resource, err := h.resourceRepo.GetByID(requestCtx, resourceID)
	if err != nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "查询资源失败"})
		return
	}
	if resource == nil {
		ctx.JSON(consts.StatusNotFound, map[string]string{"error": "资源不存在"})
		return
	}

	version, err := h.resourceRepo.GetCurrentVersion(requestCtx, resourceID)
	if err != nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "查询资源版本失败"})
		return
	}
	if version == nil {
		ctx.JSON(consts.StatusNotFound, map[string]string{"error": "资源没有可导出的当前版本"})
		return
	}

	ctx.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, exportFileName(*resource)))
	ctx.Data(consts.StatusOK, "text/markdown; charset=utf-8", []byte(version.Content))
}

// Search 使用已配置的 retriever 返回单个资源下的引用结果。
func (h *ResourceHandler) Search(requestCtx context.Context, ctx *app.RequestContext) {
	query := strings.TrimSpace(ctx.Query("q"))
	if query == "" {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "查询参数 q 不能为空"})
		return
	}
	if h.resourceRepo == nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "资源存储未配置"})
		return
	}
	resourceID, ok := parseResourceIDParam(ctx)
	if !ok {
		return
	}

	resource, err := h.resourceRepo.GetByID(requestCtx, resourceID)
	if err != nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "查询资源失败"})
		return
	}
	if resource == nil {
		ctx.JSON(consts.StatusNotFound, map[string]string{"error": "资源不存在"})
		return
	}

	version, err := h.resourceRepo.GetCurrentVersion(requestCtx, resourceID)
	if err != nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "查询资源版本失败"})
		return
	}
	if version == nil {
		ctx.JSON(consts.StatusConflict, map[string]string{"error": "资源当前版本不存在，无法检索"})
		return
	}

	if h.retriever == nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "检索服务未配置"})
		return
	}

	citations, err := h.retriever.SearchByResource(requestCtx, resourceID, query, 5)
	if err != nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "检索资源失败"})
		return
	}

	ctx.JSON(consts.StatusOK, searchResourcesResponse{
		Query:     query,
		Citations: citations,
	})
}

func parseResourceIDParam(ctx *app.RequestContext) (string, bool) {
	resourceID := strings.TrimSpace(ctx.Param("id"))
	if _, err := uuid.Parse(resourceID); err != nil {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "资源 ID 非法"})
		return "", false
	}

	return resourceID, true
}

// exportFileName 为下载响应生成稳定的 Markdown 文件名，避免路径分隔符进入响应头。
func exportFileName(resource postgres.Resource) string {
	base := strings.TrimSpace(resource.Title)
	if base == "" {
		base = "resource-" + resource.ID
	}

	replacer := strings.NewReplacer(
		"\\", "-",
		"/", "-",
		":", "-",
		"*", "-",
		"?", "-",
		`"`, "-",
		"<", "-",
		">", "-",
		"|", "-",
	)
	base = strings.TrimSpace(replacer.Replace(base))
	if base == "" {
		base = "resource-" + resource.ID
	}

	return base + ".md"
}

package handlers

import (
	"context"
	"strings"
	"time"

	"agent_project/apps/server/internal/knowledge/citation"
	"agent_project/apps/server/internal/knowledge/retriever"
	"agent_project/apps/server/internal/storage/postgres"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

type ResourceHandler struct {
	resourceRepo *postgres.ResourceRepo
	retriever    *retriever.Service
}

type resourceSummary struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	SourceType string    `json:"source_type"`
	CreatedAt  time.Time `json:"created_at"`
}

type resourceVersionResponse struct {
	ID            string    `json:"id"`
	VersionNumber int       `json:"version_number"`
	Content       string    `json:"content"`
	Source        string    `json:"source"`
	CreatedAt     time.Time `json:"created_at"`
}

type listResourcesResponse struct {
	Resources []resourceSummary `json:"resources"`
}

type getResourceResponse struct {
	Resource       resourceSummary          `json:"resource"`
	CurrentVersion *resourceVersionResponse `json:"current_version"`
}

type searchResourcesResponse struct {
	Query     string              `json:"query"`
	Citations []citation.Citation `json:"citations"`
}

func NewResourceHandler(repo *postgres.ResourceRepo, ret *retriever.Service) *ResourceHandler {
	return &ResourceHandler{
		resourceRepo: repo,
		retriever:    ret,
	}
}

func (h *ResourceHandler) List(requestCtx context.Context, ctx *app.RequestContext) {
	resources, err := h.resourceRepo.List(requestCtx)
	if err != nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "failed to list resources"})
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

func (h *ResourceHandler) GetByID(requestCtx context.Context, ctx *app.RequestContext) {
	resourceID := ctx.Param("id")

	resource, err := h.resourceRepo.GetByID(requestCtx, resourceID)
	if err != nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "failed to get resource"})
		return
	}
	if resource == nil {
		ctx.JSON(consts.StatusNotFound, map[string]string{"error": "resource not found"})
		return
	}

	version, err := h.resourceRepo.GetCurrentVersion(requestCtx, resourceID)
	if err != nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "failed to get resource version"})
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

func (h *ResourceHandler) Search(requestCtx context.Context, ctx *app.RequestContext) {
	query := strings.TrimSpace(ctx.Query("q"))
	if query == "" {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "query parameter 'q' is required"})
		return
	}
	if h.retriever == nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "retriever not configured"})
		return
	}

	citations, err := h.retriever.SearchByResource(requestCtx, ctx.Param("id"), query, 5)
	if err != nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "failed to search resource"})
		return
	}

	ctx.JSON(consts.StatusOK, searchResourcesResponse{
		Query:     query,
		Citations: citations,
	})
}

package builtin

import (
	"context"
	"encoding/json"
	"time"

	agentevidence "agent_project/apps/server/internal/agent/evidence"
	agenttools "agent_project/apps/server/internal/agent/tools"
	documentcommit "agent_project/apps/server/internal/document/commit"
	documentpatch "agent_project/apps/server/internal/document/patch"
	documentvalidation "agent_project/apps/server/internal/document/validation"
)

type DocumentVersion struct {
	ID            string    `json:"id"`
	ResourceID    string    `json:"resource_id"`
	VersionNumber int       `json:"version_number"`
	Source        string    `json:"source"`
	CreatedAt     time.Time `json:"created_at"`
}

type DocumentNode struct {
	NodeID      string         `json:"node_id"`
	ResourceID  string         `json:"resource_id"`
	VersionID   string         `json:"version_id"`
	Type        string         `json:"type"`
	Content     string         `json:"content"`
	ContentHash string         `json:"content_hash"`
	PageStart   *int           `json:"page_start,omitempty"`
	PageEnd     *int           `json:"page_end,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type ReadNodesInput struct {
	ResourceID string   `json:"resource_id"`
	VersionID  string   `json:"version_id,omitempty"`
	NodeIDs    []string `json:"node_ids"`
}

type SearchNodesInput struct {
	ResourceID string `json:"resource_id"`
	VersionID  string `json:"version_id,omitempty"`
	Query      string `json:"query"`
	Limit      int    `json:"limit"`
}

type Evidence = agentevidence.Evidence
type EvidenceSet = agentevidence.EvidenceSet

type RetrievalInput struct {
	ResourceID     string `json:"resource_id"`
	VersionID      string `json:"version_id,omitempty"`
	IncludeHistory bool   `json:"include_history,omitempty"`
	Query          string `json:"query"`
	Limit          int    `json:"limit"`
}

type WebSearchInput struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

type WebResult struct {
	Title       string     `json:"title"`
	URL         string     `json:"url"`
	Snippet     string     `json:"snippet"`
	Publisher   string     `json:"publisher"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
}

type WebSearchOutput struct {
	Provider string      `json:"provider"`
	Results  []WebResult `json:"results"`
}

type Artifact struct {
	ID                 string          `json:"id"`
	URI                string          `json:"uri"`
	WorkspaceID        string          `json:"workspace_id"`
	DataClassification string          `json:"data_classification"`
	Content            json.RawMessage `json:"content"`
	ContentHash        string          `json:"content_hash"`
	CreatedAt          time.Time       `json:"created_at"`
}

type ArtifactReadInput struct {
	ArtifactID string `json:"artifact_id"`
}

type ArtifactWriteInput struct {
	Content            json.RawMessage `json:"content"`
	DataClassification string          `json:"data_classification"`
}

type PatchInput = documentpatch.Set
type PatchValidation = documentvalidation.Result
type PatchCommit = documentcommit.Result

type ApprovalInput struct {
	RunID          string                   `json:"run_id"`
	StepID         string                   `json:"step_id"`
	ToolName       string                   `json:"tool_name"`
	ToolVersion    string                   `json:"tool_version"`
	IdempotencyKey string                   `json:"idempotency_key"`
	Reason         string                   `json:"reason"`
	Payload        json.RawMessage          `json:"payload"`
	Resources      []agenttools.ResourceRef `json:"resources"`
}

type Approval struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type DocumentBackend interface {
	GetCurrentVersion(ctx context.Context, resourceID string) (*DocumentVersion, error)
	ReadNodes(ctx context.Context, input ReadNodesInput) ([]DocumentNode, error)
	SearchNodes(ctx context.Context, input SearchNodesInput) ([]DocumentNode, error)
}

type RetrievalBackend interface {
	Search(ctx context.Context, security agenttools.SecurityContext, input RetrievalInput) (EvidenceSet, error)
}

type WebBackend interface {
	Search(ctx context.Context, input WebSearchInput, traceID string) (WebSearchOutput, error)
}

type ArtifactBackend interface {
	Read(ctx context.Context, workspaceID string, input ArtifactReadInput) (*Artifact, error)
	Write(ctx context.Context, workspaceID string, input ArtifactWriteInput, idempotencyKey string) (*Artifact, error)
}

type PatchBackend interface {
	documentcommit.CanonicalToolBackend
}

type ApprovalBackend interface {
	RequestApproval(ctx context.Context, security agenttools.SecurityContext, input ApprovalInput, idempotencyKey string) (Approval, error)
}

type Backends struct {
	Documents DocumentBackend
	Retrieval RetrievalBackend
	Web       WebBackend
	Artifacts ArtifactBackend
	Patches   PatchBackend
	Approvals ApprovalBackend
}

type WebProviderKind string

const (
	WebProviderMock       WebProviderKind = "mock"
	WebProviderProduction WebProviderKind = "production"
)

type WebConfig struct {
	ProviderKind   WebProviderKind
	ProviderName   string
	AllowedDomains []string
}

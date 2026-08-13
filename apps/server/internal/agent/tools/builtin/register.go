package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	agentevidence "agent_project/apps/server/internal/agent/evidence"
	agenttools "agent_project/apps/server/internal/agent/tools"
	assistantweb "agent_project/apps/server/internal/assistant/websearch"
	documentcommit "agent_project/apps/server/internal/document/commit"
	documentpatch "agent_project/apps/server/internal/document/patch"
	documentvalidation "agent_project/apps/server/internal/document/validation"
)

type typedTool struct {
	descriptor agenttools.Descriptor
	execute    func(context.Context, agenttools.Call) (agenttools.Result, error)
}

// Descriptor 执行该函数负责的核心处理逻辑。
func (t typedTool) Descriptor() agenttools.Descriptor { return t.descriptor }

// Execute 执行该函数负责的核心处理逻辑。
func (t typedTool) Execute(ctx context.Context, call agenttools.Call) (agenttools.Result, error) {
	return t.execute(ctx, call)
}

// RegisterCore 执行该函数负责的核心处理逻辑。
func RegisterCore(registry *agenttools.Registry, backends Backends, webConfig WebConfig) error {
	if registry == nil || backends.Documents == nil || backends.Retrieval == nil || backends.Web == nil ||
		backends.Artifacts == nil || backends.Patches == nil || backends.Approvals == nil {
		return fmt.Errorf("核心工具 registry 和全部类型化的 backends 不能为空")
	}
	webTool, err := newWebSearchTool(backends.Web, webConfig)
	if err != nil {
		return err
	}
	tools := []agenttools.Tool{
		newArtifactReadTool(backends.Artifacts), newArtifactWriteTool(backends.Artifacts),
		newCurrentVersionTool(backends.Documents), newReadNodesTool(backends.Documents), newSearchNodesTool(backends.Documents),
		newPatchCommitTool(backends.Patches), newPatchValidateTool(backends.Patches),
		newRetrievalTool(backends.Retrieval), webTool, newApprovalTool(backends.Approvals),
	}
	for _, tool := range tools {
		if err := registry.Register(tool); err != nil {
			return err
		}
	}
	return nil
}

// descriptor 执行该函数负责的核心处理逻辑。
func descriptor(name, description string, input, output string, permissions []string, risk agenttools.RiskLevel, mode agenttools.IdempotencyMode, maxTokens int, selectors ...agenttools.ResourceSelector) agenttools.Descriptor {
	retry := agenttools.RetryPolicy{MaxAttempts: 1}
	if risk == agenttools.RiskLow {
		retry = agenttools.RetryPolicy{MaxAttempts: 2, BaseBackoff: 100 * time.Millisecond, MaxBackoff: time.Second}
	}
	return agenttools.Descriptor{
		Name: name, Version: "1.0.0", Description: description,
		InputSchema: json.RawMessage(input), OutputSchema: json.RawMessage(output),
		RequiredPermissions: permissions, ResourceSelectors: selectors, RiskLevel: risk,
		Timeout: 10 * time.Second, RetryPolicy: retry, IdempotencyMode: mode,
		MaxResultTokens: maxTokens, DataClassification: agenttools.DataInternal,
	}
}

// newCurrentVersionTool 执行该函数负责的核心处理逻辑。
func newCurrentVersionTool(backend DocumentBackend) agenttools.Tool {
	d := descriptor("document.get_current_version", "Get document version metadata without inlining document content",
		`{"type":"object","properties":{"resource_id":{"type":"string","minLength":1}},"required":["resource_id"],"additionalProperties":false}`,
		`{"type":"object","properties":{"version":{"type":"object"}},"required":["version"],"additionalProperties":false}`,
		[]string{"document.read"}, agenttools.RiskLow, agenttools.IdempotencyNone, 300,
		agenttools.ResourceSelector{Type: "document", InputField: "resource_id", Access: agenttools.AccessRead})
	return typedTool{descriptor: d, execute: func(ctx context.Context, call agenttools.Call) (agenttools.Result, error) {
		var input struct {
			ResourceID string `json:"resource_id"`
		}
		if err := json.Unmarshal(call.Input, &input); err != nil {
			return agenttools.Result{}, invalid(err)
		}
		version, err := backend.GetCurrentVersion(ctx, input.ResourceID)
		if err != nil {
			return agenttools.Result{}, err
		}
		if version == nil {
			return agenttools.Result{}, notFound("current document version")
		}
		output, _ := json.Marshal(map[string]any{"version": version})
		return agenttools.Result{Output: output, Provenance: []agenttools.Provenance{{SourceType: "document", SourceID: input.ResourceID, ResourceID: input.ResourceID, VersionID: version.ID, TrustLevel: "untrusted"}}}, nil
	}}
}

// newReadNodesTool 执行该函数负责的核心处理逻辑。
func newReadNodesTool(backend DocumentBackend) agenttools.Tool {
	d := descriptor("document.read_nodes", "Read bounded canonical document nodes by stable node ID",
		`{"type":"object","properties":{"resource_id":{"type":"string","minLength":1},"version_id":{"type":"string"},"node_ids":{"type":"array","items":{"type":"string"},"minItems":1,"maxItems":50}},"required":["resource_id","node_ids"],"additionalProperties":false}`,
		`{"type":"object","properties":{"nodes":{"type":"array"}},"required":["nodes"],"additionalProperties":false}`,
		[]string{"document.read"}, agenttools.RiskLow, agenttools.IdempotencyNone, 1800,
		agenttools.ResourceSelector{Type: "document", InputField: "resource_id", Access: agenttools.AccessRead})
	return typedTool{descriptor: d, execute: func(ctx context.Context, call agenttools.Call) (agenttools.Result, error) {
		var input ReadNodesInput
		if err := json.Unmarshal(call.Input, &input); err != nil {
			return agenttools.Result{}, invalid(err)
		}
		nodes, err := backend.ReadNodes(ctx, input)
		if err != nil {
			return agenttools.Result{}, err
		}
		output, _ := json.Marshal(map[string]any{"nodes": nodes})
		return agenttools.Result{Output: output, Provenance: documentProvenance(input.ResourceID, input.VersionID, nodes)}, nil
	}}
}

// newSearchNodesTool 执行该函数负责的核心处理逻辑。
func newSearchNodesTool(backend DocumentBackend) agenttools.Tool {
	d := descriptor("document.search_nodes", "Search nodes within one authorized document",
		`{"type":"object","properties":{"resource_id":{"type":"string","minLength":1},"version_id":{"type":"string"},"query":{"type":"string","minLength":1,"maxLength":500},"limit":{"type":"integer","minimum":1,"maximum":50}},"required":["resource_id","query","limit"],"additionalProperties":false}`,
		`{"type":"object","properties":{"nodes":{"type":"array"}},"required":["nodes"],"additionalProperties":false}`,
		[]string{"document.read"}, agenttools.RiskLow, agenttools.IdempotencyNone, 1800,
		agenttools.ResourceSelector{Type: "document", InputField: "resource_id", Access: agenttools.AccessRead})
	return typedTool{descriptor: d, execute: func(ctx context.Context, call agenttools.Call) (agenttools.Result, error) {
		var input SearchNodesInput
		if err := json.Unmarshal(call.Input, &input); err != nil {
			return agenttools.Result{}, invalid(err)
		}
		nodes, err := backend.SearchNodes(ctx, input)
		if err != nil {
			return agenttools.Result{}, err
		}
		output, _ := json.Marshal(map[string]any{"nodes": nodes})
		return agenttools.Result{Output: output, Provenance: documentProvenance(input.ResourceID, input.VersionID, nodes)}, nil
	}}
}

// newRetrievalTool 执行该函数负责的核心处理逻辑。
func newRetrievalTool(backend RetrievalBackend) agenttools.Tool {
	d := descriptor("retrieval.search", "Retrieve a versioned evidence set",
		`{"type":"object","properties":{"resource_id":{"type":"string","minLength":1},"version_id":{"type":"string"},"include_history":{"type":"boolean"},"query":{"type":"string","minLength":1,"maxLength":500},"limit":{"type":"integer","minimum":1,"maximum":50}},"required":["resource_id","query","limit"],"additionalProperties":false}`,
		evidenceSetOutputSchema,
		[]string{"retrieval.search", "document.read"}, agenttools.RiskLow, agenttools.IdempotencyNone, 2000,
		agenttools.ResourceSelector{Type: "document", InputField: "resource_id", Access: agenttools.AccessRead})
	d.Version = "2.0.0"
	return typedTool{descriptor: d, execute: func(ctx context.Context, call agenttools.Call) (agenttools.Result, error) {
		var input RetrievalInput
		if err := json.Unmarshal(call.Input, &input); err != nil {
			return agenttools.Result{}, invalid(err)
		}
		set, err := backend.Search(ctx, call.Security, input)
		if err != nil {
			return agenttools.Result{}, err
		}
		if err := set.Validate(); err != nil {
			return agenttools.Result{}, &agenttools.ToolError{Category: agenttools.ErrorTerminalUpstream, Message: "检索后端返回了无效的 EvidenceSet", Cause: err}
		}
		if set.WorkspaceID != call.Security.WorkspaceID || set.ResourceID != input.ResourceID ||
			(input.IncludeHistory && set.VersionID != input.VersionID) {
			return agenttools.Result{}, &agenttools.ToolError{Category: agenttools.ErrorTerminalUpstream, Message: "检索 EvidenceSet 的作用域不匹配"}
		}
		output, _ := json.Marshal(map[string]any{"evidence_set": set})
		provenance := make([]agenttools.Provenance, 0, max(1, len(set.Evidence)))
		for _, item := range set.Evidence {
			provenance = append(provenance, agenttools.Provenance{SourceType: item.SourceType, SourceID: item.EvidenceID, ResourceID: item.ResourceID, VersionID: item.VersionID, ContentHash: item.ContentHash, TrustLevel: "untrusted"})
		}
		if len(provenance) == 0 {
			provenance = append(provenance, agenttools.Provenance{SourceType: "retrieval", SourceID: set.SetID, ResourceID: set.ResourceID, VersionID: set.VersionID, TrustLevel: "untrusted"})
		}
		return agenttools.Result{Output: output, Provenance: provenance, OversizeSummary: retrievalOversizeSummary(set)}, nil
	}}
}

// retrievalOversizeSummary 执行该函数负责的核心处理逻辑。
func retrievalOversizeSummary(set EvidenceSet) json.RawMessage {
	type citationSummary struct {
		EvidenceID  string  `json:"evidence_id"`
		ResourceID  string  `json:"resource_id"`
		VersionID   string  `json:"version_id"`
		NodeID      string  `json:"node_id"`
		ContentHash string  `json:"content_hash"`
		FusedScore  float64 `json:"fused_score"`
		TrustLevel  string  `json:"trust_level"`
	}
	citations := make([]citationSummary, 0, min(12, len(set.Evidence)))
	for _, item := range set.Evidence {
		citations = append(citations, citationSummary{
			EvidenceID: item.EvidenceID, ResourceID: item.ResourceID, VersionID: item.VersionID,
			NodeID: item.NodeID, ContentHash: item.ContentHash, FusedScore: item.FusedScore,
			TrustLevel: string(item.TrustLevel),
		})
		if len(citations) == 12 {
			break
		}
	}
	degradations := make([]string, 0)
	seenDegradations := make(map[string]struct{})
	for _, process := range set.Process {
		if process.Stage != agentevidence.StageDegradation || process.Status != agentevidence.ProcessDegraded {
			continue
		}
		channel := string(process.Channel)
		if _, exists := seenDegradations[channel]; channel == "" || exists {
			continue
		}
		seenDegradations[channel] = struct{}{}
		degradations = append(degradations, channel)
	}
	encoded, _ := json.Marshal(map[string]any{
		"kind": "evidence_set", "schema_version": set.SchemaVersion, "set_id": set.SetID,
		"resource_id": set.ResourceID, "version_id": set.VersionID,
		"profile_version": set.ProfileVersion, "evidence_count": len(set.Evidence), "citations": citations,
		"degradations": degradations,
	})
	return encoded
}

const evidenceSetOutputSchema = `{
  "type":"object",
  "properties":{
    "evidence_set":{
      "type":"object",
      "properties":{
        "schema_version":{"type":"string","const":"1.0"},
        "set_id":{"type":"string","minLength":1},
        "workspace_id":{"type":"string","minLength":1},
        "resource_id":{"type":"string","minLength":1},
        "version_id":{"type":"string","minLength":1},
        "query":{"type":"string","minLength":1},
        "query_hash":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"},
        "profile_version":{"type":"string","minLength":1},
        "created_at":{"type":"string","minLength":1},
        "evidence":{
          "type":"array","maxItems":50,
          "items":{
            "type":"object",
            "properties":{
              "evidence_id":{"type":"string","minLength":1},
              "resource_id":{"type":"string","minLength":1},
              "version_id":{"type":"string","minLength":1},
              "node_id":{"type":"string","minLength":1},
              "source_type":{"type":"string","minLength":1},
              "content":{"type":"string","minLength":1},
              "content_hash":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"},
              "lexical_score":{"type":"number","minimum":0,"maximum":1},
              "vector_score":{"type":"number","minimum":0,"maximum":1},
              "fused_score":{"type":"number","minimum":0,"maximum":1},
              "trust_level":{"type":"string","const":"untrusted"},
              "created_at":{"type":"string","minLength":1},
              "provenance":{
                "type":"object",
                "properties":{
                  "retrieval":{"type":"array","minItems":1,"maxItems":2,"items":{"type":"object","properties":{"channel":{"type":"string","enum":["lexical","semantic"]},"rank":{"type":"integer","minimum":1},"score":{"type":"number","minimum":0,"maximum":1},"index_version":{"type":"string","minLength":1}},"required":["channel","rank","score","index_version"],"additionalProperties":false}},
                  "filtering":{"type":"array","minItems":1,"items":{"type":"object","properties":{"stage":{"type":"string","minLength":1},"decision":{"type":"string","enum":["included","excluded"]},"reason":{"type":"string","minLength":1}},"required":["stage","decision","reason"],"additionalProperties":false}},
                  "fusion":{"type":"object","properties":{"algorithm":{"type":"string","enum":["weighted_sum","reciprocal_rank_fusion"]},"profile_version":{"type":"string","minLength":1},"pre_rerank_rank":{"type":"integer","minimum":1},"threshold":{"type":"number","minimum":0,"maximum":1}},"required":["algorithm","profile_version","pre_rerank_rank","threshold"],"additionalProperties":false},
                  "rerank":{"type":"object","properties":{"enabled":{"type":"boolean"},"applied":{"type":"boolean"},"profile_version":{"type":"string","minLength":1},"model":{"type":"string"},"before_rank":{"type":"integer","minimum":1},"after_rank":{"type":"integer","minimum":1},"score":{"type":"number","minimum":0,"maximum":1},"degraded_reason":{"type":"string"}},"required":["enabled","applied","profile_version","before_rank","after_rank","score"],"additionalProperties":false}
                },
                "required":["retrieval","filtering","fusion","rerank"],"additionalProperties":false
              }
            },
            "required":["evidence_id","resource_id","version_id","node_id","source_type","content","content_hash","lexical_score","vector_score","fused_score","trust_level","created_at","provenance"],
            "additionalProperties":false
          }
        },
        "process":{"type":"array","minItems":1,"items":{"type":"object","properties":{"stage":{"type":"string","enum":["recall","filter","fusion","rerank","degradation"]},"status":{"type":"string","enum":["succeeded","degraded","skipped"]},"channel":{"type":"string","enum":["lexical","semantic"]},"input_count":{"type":"integer","minimum":0},"output_count":{"type":"integer","minimum":0},"reason":{"type":"string"}},"required":["stage","status","input_count","output_count"],"additionalProperties":false}}
      },
      "required":["schema_version","set_id","workspace_id","resource_id","version_id","query","query_hash","profile_version","created_at","evidence","process"],
      "additionalProperties":false
    }
  },
  "required":["evidence_set"],
  "additionalProperties":false
}`

// newArtifactReadTool 执行该函数负责的核心处理逻辑。
func newArtifactReadTool(backend ArtifactBackend) agenttools.Tool {
	d := descriptor("artifact.read", "Read a bounded artifact by immutable ID",
		`{"type":"object","properties":{"artifact_id":{"type":"string","minLength":1}},"required":["artifact_id"],"additionalProperties":false}`,
		`{"type":"object","properties":{"artifact":{"type":"object"}},"required":["artifact"],"additionalProperties":false}`,
		[]string{"artifact.read"}, agenttools.RiskLow, agenttools.IdempotencyNone, 1200,
		agenttools.ResourceSelector{Type: "artifact", InputField: "artifact_id", Access: agenttools.AccessRead})
	return typedTool{descriptor: d, execute: func(ctx context.Context, call agenttools.Call) (agenttools.Result, error) {
		var input ArtifactReadInput
		if err := json.Unmarshal(call.Input, &input); err != nil {
			return agenttools.Result{}, invalid(err)
		}
		artifact, err := backend.Read(ctx, call.Security.WorkspaceID, input)
		if err != nil {
			return agenttools.Result{}, err
		}
		if artifact == nil {
			return agenttools.Result{}, notFound("artifact")
		}
		output, _ := json.Marshal(map[string]any{"artifact": artifact})
		return agenttools.Result{Output: output, Provenance: []agenttools.Provenance{{SourceType: "artifact", SourceID: artifact.ID, ContentHash: artifact.ContentHash, TrustLevel: "untrusted"}}}, nil
	}}
}

// newArtifactWriteTool 执行该函数负责的核心处理逻辑。
func newArtifactWriteTool(backend ArtifactBackend) agenttools.Tool {
	d := descriptor("artifact.write", "Persist a model-external artifact with classification and hash",
		`{"type":"object","properties":{"content":{"type":"object"},"data_classification":{"type":"string","enum":["internal","confidential","restricted"]}},"required":["content","data_classification"],"additionalProperties":false}`,
		`{"type":"object","properties":{"artifact":{"type":"object"}},"required":["artifact"],"additionalProperties":false}`,
		[]string{"artifact.write"}, agenttools.RiskMedium, agenttools.IdempotencyRequired, 300)
	return typedTool{descriptor: d, execute: func(ctx context.Context, call agenttools.Call) (agenttools.Result, error) {
		var input ArtifactWriteInput
		if err := json.Unmarshal(call.Input, &input); err != nil {
			return agenttools.Result{}, invalid(err)
		}
		artifact, err := backend.Write(ctx, call.Security.WorkspaceID, input, call.IdempotencyKey)
		if err != nil {
			return agenttools.Result{}, err
		}
		if artifact == nil {
			return agenttools.Result{}, &agenttools.ToolError{Category: agenttools.ErrorTerminalUpstream, Message: "制品后端未返回记录"}
		}
		output, _ := json.Marshal(map[string]any{"artifact": artifact})
		return trustedMutation(call, output, "artifact", artifact.ID), nil
	}}
}

// newPatchValidateTool 执行该函数负责的核心处理逻辑。
func newPatchValidateTool(backend PatchBackend) agenttools.Tool {
	d := descriptor("patch.validate", "Deterministically validate a node-ID PatchSet",
		patchInputSchema, `{"type":"object","properties":{"validation":{"type":"object"}},"required":["validation"],"additionalProperties":false}`,
		[]string{"document.write"}, agenttools.RiskMedium, agenttools.IdempotencyNone, 600,
		agenttools.ResourceSelector{Type: "document", InputField: "resource_id", Access: agenttools.AccessWrite})
	// Validation 仅允许读取 even though it checks 写入 作用域, so retries remain disabled 和 没有 idempotency 键 为 needed.
	d.ResourceSelectors[0].Access = agenttools.AccessRead
	return typedTool{descriptor: d, execute: func(ctx context.Context, call agenttools.Call) (agenttools.Result, error) {
		input, err := documentpatch.ParseStrict(call.Input, documentpatch.DefaultLimits())
		if err != nil {
			return agenttools.Result{}, invalid(err)
		}
		validation, err := backend.Validate(ctx, call.Security, input)
		if err != nil {
			return agenttools.Result{}, classifyPatchError(err)
		}
		output, _ := json.Marshal(map[string]any{"validation": validation})
		return trustedMutation(call, output, "patch_validation", input.ResourceID), nil
	}}
}

// newPatchCommitTool 执行该函数负责的核心处理逻辑。
func newPatchCommitTool(backend PatchBackend) agenttools.Tool {
	d := descriptor("patch.commit", "Atomically commit a validated node-ID PatchSet",
		patchInputSchema, `{"type":"object","properties":{"commit":{"type":"object"}},"required":["commit"],"additionalProperties":false}`,
		[]string{"document.write"}, agenttools.RiskHigh, agenttools.IdempotencyRequired, 600,
		agenttools.ResourceSelector{Type: "document", InputField: "resource_id", Access: agenttools.AccessWrite})
	return typedTool{descriptor: d, execute: func(ctx context.Context, call agenttools.Call) (agenttools.Result, error) {
		input, err := documentpatch.ParseStrict(call.Input, documentpatch.DefaultLimits())
		if err != nil {
			return agenttools.Result{}, invalid(err)
		}
		commit, err := backend.Commit(ctx, call.Security, input, call.IdempotencyKey)
		if err != nil {
			return agenttools.Result{}, classifyPatchError(err)
		}
		output, _ := json.Marshal(map[string]any{"commit": commit})
		return trustedMutation(call, output, "document_version", commit.VersionID), nil
	}}
}

// newApprovalTool 执行该函数负责的核心处理逻辑。
func newApprovalTool(backend ApprovalBackend) agenttools.Tool {
	d := descriptor("workflow.request_approval", "Create a persisted approval request without approving it",
		`{"type":"object","properties":{"run_id":{"type":"string"},"step_id":{"type":"string"},"tool_name":{"type":"string"},"tool_version":{"type":"string"},"idempotency_key":{"type":"string"},"reason":{"type":"string","minLength":1},"payload":{"type":"object"},"resources":{"type":"array","items":{"type":"object","properties":{"type":{"type":"string"},"id":{"type":"string"},"access":{"type":"string","enum":["read","write"]}},"required":["type","id","access"],"additionalProperties":false}}},"required":["run_id","step_id","tool_name","tool_version","idempotency_key","reason","payload","resources"],"additionalProperties":false}`,
		`{"type":"object","properties":{"approval":{"type":"object"}},"required":["approval"],"additionalProperties":false}`,
		[]string{"workflow.request_approval"}, agenttools.RiskMedium, agenttools.IdempotencyRequired, 300)
	return typedTool{descriptor: d, execute: func(ctx context.Context, call agenttools.Call) (agenttools.Result, error) {
		var input ApprovalInput
		if err := json.Unmarshal(call.Input, &input); err != nil {
			return agenttools.Result{}, invalid(err)
		}
		if strings.TrimSpace(input.RunID) != call.RunID || strings.TrimSpace(input.StepID) != call.StepID {
			return agenttools.Result{}, invalid(fmt.Errorf("审批目标 run_id 和 step_id 必须 match the current 工具调用"))
		}
		approval, err := backend.RequestApproval(ctx, call.Security, input, call.IdempotencyKey)
		if err != nil {
			return agenttools.Result{}, err
		}
		output, _ := json.Marshal(map[string]any{"approval": approval})
		return trustedMutation(call, output, "approval", approval.ID), nil
	}}
}

// newWebSearchTool 执行该函数负责的核心处理逻辑。
func newWebSearchTool(backend WebBackend, config WebConfig) (agenttools.Tool, error) {
	if config.ProviderKind != WebProviderMock && config.ProviderKind != WebProviderProduction {
		return nil, fmt.Errorf("处理失败：web 提供方 kind 必须为明确的")
	}
	config.ProviderName = strings.TrimSpace(config.ProviderName)
	if config.ProviderName == "" {
		return nil, fmt.Errorf("处理失败：web 提供方 name 必须为明确的")
	}
	allowed := make(map[string]struct{}, len(config.AllowedDomains))
	for _, domain := range config.AllowedDomains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain != "" {
			allowed[domain] = struct{}{}
		}
	}
	if config.ProviderKind == WebProviderProduction && len(allowed) == 0 {
		return nil, fmt.Errorf("production web 提供方需要一个允许的 domain policy")
	}
	d := descriptor("web.search", "Search an explicitly identified mock or production web provider",
		`{"type":"object","properties":{"query":{"type":"string","minLength":1,"maxLength":200},"limit":{"type":"integer","minimum":1,"maximum":10}},"required":["query","limit"],"additionalProperties":false}`,
		`{"type":"object","properties":{"provider_kind":{"type":"string"},"provider":{"type":"string"},"results":{"type":"array"},"filter":{"type":"object"}},"required":["provider_kind","provider","results","filter"],"additionalProperties":false}`,
		[]string{"web.search"}, agenttools.RiskMedium, agenttools.IdempotencyOptional, 1600)
	d.DataClassification = agenttools.DataConfidential
	return typedTool{descriptor: d, execute: func(ctx context.Context, call agenttools.Call) (agenttools.Result, error) {
		var input WebSearchInput
		if err := json.Unmarshal(call.Input, &input); err != nil {
			return agenttools.Result{}, invalid(err)
		}
		privacy := assistantweb.CheckQueryPrivacy([]string{input.Query})
		if !privacy.Safe {
			return agenttools.Result{}, &agenttools.ToolError{Category: agenttools.ErrorPolicyBlocked, Message: "Web 查询被隐私策略阻止：" + privacy.Reason}
		}
		response, err := backend.Search(ctx, input, call.TraceID)
		if err != nil {
			return agenttools.Result{}, err
		}
		if response.Provider != config.ProviderName {
			return agenttools.Result{}, &agenttools.ToolError{Category: agenttools.ErrorTerminalUpstream, Message: "Web 提供方标识与配置不匹配"}
		}
		received := len(response.Results)
		filtered := response.Results[:0]
		for _, result := range response.Results {
			if domainAllowed(result.URL, allowed) {
				filtered = append(filtered, result)
			}
		}
		response.Results = filtered
		output, _ := json.Marshal(map[string]any{
			"provider_kind": config.ProviderKind, "provider": response.Provider, "results": response.Results,
			"filter": map[string]any{"received": received, "allowed": len(filtered), "blocked": received - len(filtered)},
		})
		provenance := []agenttools.Provenance{{SourceType: "web_search", SourceID: response.Provider, TrustLevel: "untrusted", Provider: string(config.ProviderKind)}}
		return agenttools.Result{Output: output, Provenance: provenance}, nil
	}}, nil
}

const patchInputSchema = `{"type":"object","properties":{"schema_version":{"type":"string","enum":["1.0"]},"resource_id":{"type":"string","minLength":1},"base_version_id":{"type":"string","minLength":1},"operations":{"type":"array","items":{"type":"object","properties":{"op":{"type":"string","enum":["replace_node","insert_before","insert_after","delete_node","update_attributes"]},"node_id":{"type":"string","minLength":1},"expected_hash":{"type":"string","minLength":71,"maxLength":71},"content":{"type":"string"},"attributes":{"type":"object"},"expected_parent_id":{"type":"string"},"expected_parent_hash":{"type":"string"},"node":{"type":"object","properties":{"node_id":{"type":"string"},"type":{"type":"string"},"attributes":{"type":"object"},"content":{"type":"string"},"children":{"type":"array"},"source_location":{"type":"object"},"page_mapping":{"type":"array"},"metadata":{"type":"object"},"content_hash":{"type":"string"}},"required":["node_id","type","attributes","content","children","source_location","page_mapping","metadata","content_hash"],"additionalProperties":false}},"required":["op","node_id","expected_hash"],"additionalProperties":false},"minItems":1,"maxItems":100},"evidence_refs":{"type":"array","items":{"type":"string"},"maxItems":100},"reason":{"type":"string","minLength":1}},"required":["schema_version","resource_id","base_version_id","operations","evidence_refs","reason"],"additionalProperties":false}`

// documentProvenance 执行该函数负责的核心处理逻辑。
func documentProvenance(resourceID, versionID string, nodes []DocumentNode) []agenttools.Provenance {
	result := make([]agenttools.Provenance, 0, max(1, len(nodes)))
	for _, node := range nodes {
		result = append(result, agenttools.Provenance{SourceType: "document_node", SourceID: node.NodeID, ResourceID: node.ResourceID, VersionID: node.VersionID, ContentHash: node.ContentHash, TrustLevel: "untrusted"})
	}
	if len(result) == 0 {
		result = append(result, agenttools.Provenance{SourceType: "document", SourceID: resourceID, ResourceID: resourceID, VersionID: versionID, TrustLevel: "untrusted"})
	}
	return result
}

// trustedMutation 执行该函数负责的核心处理逻辑。
func trustedMutation(call agenttools.Call, output json.RawMessage, sourceType, sourceID string) agenttools.Result {
	if strings.TrimSpace(sourceID) == "" {
		sourceID = call.StepID
	}
	return agenttools.Result{Output: output, Provenance: []agenttools.Provenance{{SourceType: sourceType, SourceID: sourceID, TrustLevel: "trusted"}}}
}

// 无效的 执行该函数负责的核心处理逻辑。
func invalid(err error) error {
	return &agenttools.ToolError{Category: agenttools.ErrorInvalidInput, Message: err.Error(), Cause: err}
}

// classifyPatchError 执行该函数负责的核心处理逻辑。
func classifyPatchError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, documentcommit.ErrVersionConflict) || errors.Is(err, documentcommit.ErrHashConflict) || errors.Is(err, documentcommit.ErrIdempotencyConflict) {
		return &agenttools.ToolError{Category: agenttools.ErrorConflict, Message: err.Error(), Cause: err}
	}
	if errors.Is(err, documentcommit.ErrRetryableCommit) {
		return &agenttools.ToolError{Category: agenttools.ErrorRetryableUpstream, Message: err.Error(), Cause: err}
	}
	var validationErr *documentcommit.ValidationError
	if errors.As(err, &validationErr) && len(validationErr.Result.Errors) > 0 {
		// 根据当前状态或类型选择对应的处理分支。
		switch validationErr.Result.Errors[0].Category {
		case documentvalidation.InvalidPatch:
			return &agenttools.ToolError{Category: agenttools.ErrorInvalidInput, Message: err.Error(), Cause: err}
		case documentvalidation.UnauthorizedNode, documentvalidation.ResourceScopeDenied:
			return &agenttools.ToolError{Category: agenttools.ErrorPermissionDenied, Message: err.Error(), Cause: err}
		case documentvalidation.PolicyBlocked:
			return &agenttools.ToolError{Category: agenttools.ErrorPolicyBlocked, Message: err.Error(), Cause: err}
		case documentvalidation.ReferenceMissing:
			return &agenttools.ToolError{Category: agenttools.ErrorNotFound, Message: err.Error(), Cause: err}
		default:
			return &agenttools.ToolError{Category: agenttools.ErrorConflict, Message: err.Error(), Cause: err}
		}
	}
	return err
}

// notFound 执行该函数负责的核心处理逻辑。
func notFound(name string) error {
	return &agenttools.ToolError{Category: agenttools.ErrorNotFound, Message: name + " not found"}
}

// domainAllowed 执行该函数负责的核心处理逻辑。
func domainAllowed(rawURL string, allowed map[string]struct{}) bool {
	if len(allowed) == 0 {
		return true
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	for domain := range allowed {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

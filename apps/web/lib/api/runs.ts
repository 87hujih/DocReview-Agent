import { apiFetch } from "./client";

export type RunSummary = {
  id: string;
  workspace_id: string;
  resource_id?: string;
  session_id?: string;
  request_id?: string;
  status: string;
  objective: string;
  current_step?: string;
  step_count: number;
  completed_step_count: number;
  failed_step_count: number;
  pending_approval_id?: string;
  created_at: string;
  updated_at: string;
};

export type RunStep = {
  id: string;
  step_key: string;
  step_type: string;
  status: string;
  attempt_count: number;
  max_attempts: number;
  next_retry_at?: string | null;
  created_at: string;
  updated_at: string;
};

export type RunToolCall = {
  id: string;
  step_id: string;
  tool_name: string;
  tool_version: string;
  status: string;
  error_category?: string;
  started_at?: string | null;
  completed_at?: string | null;
};

export type RunApproval = {
  id: string;
  run_id: string;
  step_id: string;
  tool_name: string;
  status: string;
  created_at: string;
  decided_at?: string | null;
};

export type RunFinding = {
  severity: string;
  code: string;
  message: string;
};

export type RunDetail = {
  run: {
    id: string;
    resource_id?: string;
    session_id?: string;
    request_id?: string;
    status: string;
    objective: string;
    current_step?: string;
    deadline_at?: string | null;
    cancel_requested_at?: string | null;
    created_at: string;
    updated_at: string;
  };
  steps: RunStep[];
  tool_calls: RunToolCall[];
  approvals: RunApproval[];
  findings: RunFinding[];
};

export function getRuns(options: { status?: string; resourceId?: string; limit?: number } = {}) {
  const params = new URLSearchParams({ limit: String(options.limit ?? 50) });
  if (options.status) {
    params.set("status", options.status);
  }
  if (options.resourceId) {
    params.set("resource_id", options.resourceId);
  }
  return apiFetch<{ runs: RunSummary[] }>(`/api/agent/runs?${params.toString()}`).then(
    (response) => response.runs
  );
}

export function getRun(id: string): Promise<RunDetail> {
  return apiFetch<RunDetail>(`/api/agent/runs/${id}`);
}

import { apiFetch } from "./client";

export type Approval = {
  id: string;
  workspace_id: string;
  run_id: string;
  step_id: string;
  resource_id?: string;
  session_id?: string;
  objective: string;
  tool_name: string;
  tool_version: string;
  reason: string;
  status: string;
  resources: unknown;
  payload: unknown;
  decision_reason?: string;
  created_at: string;
  decided_at?: string | null;
};

export type ApprovalDecision = Pick<Approval, "id" | "status">;

export function getApprovals(status = "pending", limit = 50): Promise<Approval[]> {
  const params = new URLSearchParams({ limit: String(limit) });
  if (status) {
    params.set("status", status);
  }
  return apiFetch<{ approvals: Approval[] }>(`/api/agent/approvals?${params.toString()}`).then(
    (response) => response.approvals
  );
}

export function getApproval(id: string): Promise<Approval> {
  return apiFetch<{ approval: Approval }>(`/api/agent/approvals/${id}`).then(
    (response) => response.approval
  );
}

export function approveApproval(id: string, reason: string): Promise<ApprovalDecision> {
  return apiFetch<{ approval: ApprovalDecision }>(`/api/agent/approvals/${id}/approve`, {
    body: JSON.stringify({ reason }),
    method: "POST"
  }).then((response) => response.approval);
}

export function rejectApproval(id: string, reason: string): Promise<ApprovalDecision> {
  return apiFetch<{ approval: ApprovalDecision }>(`/api/agent/approvals/${id}/reject`, {
    body: JSON.stringify({ reason }),
    method: "POST"
  }).then((response) => response.approval);
}

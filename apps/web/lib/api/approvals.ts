import { apiFetch } from "./client";

export interface Approval {
  id: string;
  task_id: string;
  status: string;
  reject_reason?: string;
  decided_at?: string;
  created_at: string;
}

export function getApprovals(status?: string): Promise<Approval[]> {
  const suffix = status ? `?status=${encodeURIComponent(status)}` : "";
  return apiFetch<{ approvals: Approval[] }>(`/api/approvals${suffix}`).then(
    (response) => response.approvals
  );
}

export function approveApproval(id: string): Promise<Approval> {
  return apiFetch<{ approval: Approval }>(`/api/approvals/${id}/approve`, {
    method: "POST"
  }).then((response) => response.approval);
}

export function rejectApproval(id: string, reason: string): Promise<Approval> {
  return apiFetch<{ approval: Approval }>(`/api/approvals/${id}/reject`, {
    body: JSON.stringify({ reason }),
    method: "POST"
  }).then((response) => response.approval);
}

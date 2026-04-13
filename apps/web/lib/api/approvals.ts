import { apiFetch } from "./client";

export interface Approval {
  id: string;
  task_id: string;
  status: string;
  reject_reason?: string | null;
  decided_at?: string | null;
  created_at: string;
}

export interface ExecutionJob {
  id: string;
  task_id: string;
  approval_id: string;
  status: string;
  error_message?: string | null;
  new_version_id?: string | null;
  started_at?: string | null;
  completed_at?: string | null;
  created_at: string;
}

export function getApprovals(status?: string): Promise<Approval[]> {
  const suffix = status ? `?status=${encodeURIComponent(status)}` : "";
  return apiFetch<{ approvals: Approval[] }>(`/api/approvals${suffix}`).then(
    (response) => response.approvals
  );
}

export function getApproval(id: string): Promise<Approval> {
  return apiFetch<{ approval: Approval }>(`/api/approvals/${id}`).then(
    (response) => response.approval
  );
}

export function getJob(id: string): Promise<ExecutionJob> {
  return apiFetch<{ job: ExecutionJob }>(`/api/jobs/${id}`).then((response) => response.job);
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

import { apiFetch } from "./client";
import type { Citation } from "./resources";

export interface Task {
  id: string;
  resource_id: string;
  instruction: string;
  status: string;
  error_message?: string | null;
  created_at: string;
  updated_at?: string;
}

export interface TaskStep {
  id: string;
  step_name: string;
  status: string;
  error_message?: string | null;
  started_at?: string;
  completed_at?: string;
}

export interface DiffSection {
  section_title: string;
  original: string;
  revised: string;
  reason: string;
  citation_ids: string[];
}

export interface ReviewSummaryArtifactContent {
  summary: string;
}

export interface DiffPreviewArtifactContent {
  sections: DiffSection[];
}

export interface TaskArtifact<T = unknown> {
  id: string;
  artifact_type: string;
  content: T;
  created_at: string;
}

export interface TaskEvent {
  id: string;
  task_id: string;
  run_id?: string | null;
  step_name: string;
  source: string;
  level: string;
  event_type: string;
  message: string;
  payload: Record<string, unknown>;
  created_at: string;
}

export interface CreateTaskInput {
  resource_id: string;
  instruction: string;
}

export interface TaskDetailsResponse {
  task: Task;
  steps: TaskStep[];
}

export function createTask(input: CreateTaskInput): Promise<Task> {
  return apiFetch<{ task: Task }>("/api/tasks", {
    body: JSON.stringify(input),
    method: "POST"
  }).then((response) => response.task);
}

export function getTasks(): Promise<Task[]> {
  return apiFetch<{ tasks: Task[] }>("/api/tasks").then((response) => response.tasks);
}

export function getTask(id: string): Promise<TaskDetailsResponse> {
  return apiFetch<TaskDetailsResponse>(`/api/tasks/${id}`);
}

export function getTaskArtifacts(id: string): Promise<TaskArtifact[]> {
  return apiFetch<{ artifacts: TaskArtifact[] }>(`/api/tasks/${id}/artifacts`).then(
    (response) => response.artifacts
  );
}

export function getTaskEvents(id: string): Promise<TaskEvent[]> {
  return apiFetch<{ events: TaskEvent[] }>(`/api/tasks/${id}/events`).then(
    (response) => response.events
  );
}

export function getCitationsArtifact(artifacts: TaskArtifact[]): Citation[] {
  const artifact = artifacts.find((item) => item.artifact_type === "citations");
  return Array.isArray(artifact?.content) ? (artifact.content as Citation[]) : [];
}

export function getReviewSummaryArtifact(artifacts: TaskArtifact[]): ReviewSummaryArtifactContent | null {
  const artifact = artifacts.find((item) => item.artifact_type === "review_summary");
  if (!artifact || typeof artifact.content !== "object" || artifact.content === null) {
    return null;
  }

  const summary = (artifact.content as ReviewSummaryArtifactContent).summary;
  return typeof summary === "string" ? { summary } : null;
}

export function getDiffPreviewArtifact(artifacts: TaskArtifact[]): DiffPreviewArtifactContent | null {
  const artifact = artifacts.find((item) => item.artifact_type === "diff_preview");
  if (!artifact || typeof artifact.content !== "object" || artifact.content === null) {
    return null;
  }

  const sections = (artifact.content as DiffPreviewArtifactContent).sections;
  return Array.isArray(sections) ? { sections } : null;
}

export interface WebEvidenceSource {
  title: string;
  url: string;
  snippet?: string;
  reliability_hint?: string;
}

export interface WebEvidenceArtifactContent {
  queries: string[];
  provider: string;
  sources: WebEvidenceSource[];
}

export function getWebEvidenceArtifact(artifacts: TaskArtifact[]): WebEvidenceArtifactContent | null {
  const artifact = artifacts.find((item) => item.artifact_type === "web_evidence");
  if (!artifact || typeof artifact.content !== "object" || artifact.content === null) {
    return null;
  }
  const content = artifact.content as WebEvidenceArtifactContent;
  return Array.isArray(content.sources) && content.sources.length > 0 ? content : null;
}

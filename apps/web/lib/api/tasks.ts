import { apiFetch } from "./client";
import type { Citation } from "./resources";

export interface Task {
  id: string;
  resource_id: string;
  instruction: string;
  source_session_id?: string | null;
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
  section_occurrence?: number;
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
  if (!Array.isArray(sections)) {
    return null;
  }

  const normalizedSections = sections
    .map((section) => normalizeDiffSection(section))
    .filter((section): section is DiffSection => section !== null);

  return { sections: normalizedSections };
}

function normalizeDiffSection(input: unknown): DiffSection | null {
  if (typeof input !== "object" || input === null) {
    return null;
  }

  const section = input as Record<string, unknown>;
  const sectionTitle = typeof section.section_title === "string" ? section.section_title.trim() : "";
  if (sectionTitle === "") {
    return null;
  }

  return {
    citation_ids: Array.isArray(section.citation_ids)
      ? section.citation_ids.filter((item): item is string => typeof item === "string")
      : [],
    original: typeof section.original === "string" ? section.original : "",
    reason: typeof section.reason === "string" ? section.reason : "",
    revised: typeof section.revised === "string" ? section.revised : "",
    section_occurrence:
      typeof section.section_occurrence === "number" && section.section_occurrence > 0
        ? section.section_occurrence
        : undefined,
    section_title: sectionTitle
  };
}

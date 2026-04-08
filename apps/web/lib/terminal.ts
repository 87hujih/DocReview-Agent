const SUCCESS_STATUSES = new Set(["approved", "completed"]);
const RUNNING_STATUSES = new Set(["drafting", "executing", "planning", "retrieving", "running"]);
const WARNING_STATUSES = new Set(["awaiting_approval", "pending", "rejected"]);
const ERROR_STATUSES = new Set(["error", "failed"]);

export function formatToken(value: string): string {
  return value
    .trim()
    .replace(/[^a-zA-Z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "")
    .toUpperCase();
}

export function formatStatusLabel(status?: string | null): string {
  return formatToken(status || "unknown");
}

export function getStatusTone(status?: string | null): "default" | "success" | "running" | "warning" | "error" {
  const normalized = (status || "").trim().toLowerCase();

  if (SUCCESS_STATUSES.has(normalized)) {
    return "success";
  }
  if (RUNNING_STATUSES.has(normalized)) {
    return "running";
  }
  if (WARNING_STATUSES.has(normalized)) {
    return "warning";
  }
  if (ERROR_STATUSES.has(normalized)) {
    return "error";
  }

  return "default";
}

export function isTerminalStatus(status?: string | null): boolean {
  return status === "completed" || status === "failed";
}

export function toIsoSeconds(value?: Date | string | null): string {
  if (!value) {
    return "N/A";
  }

  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) {
    return String(value);
  }

  return date.toISOString().replace(/\.\d{3}Z$/, "Z");
}

export function truncateId(value: string, head = 4, tail = 4): string {
  if (value.length <= head + tail + 1) {
    return value;
  }

  return `${value.slice(0, head)}...${value.slice(-tail)}`;
}

export function formatDuration(start?: string | null, end?: string | null): string | null {
  if (!start || !end) {
    return null;
  }

  const startedAt = new Date(start).getTime();
  const completedAt = new Date(end).getTime();
  if (Number.isNaN(startedAt) || Number.isNaN(completedAt) || completedAt < startedAt) {
    return null;
  }

  const diffMs = completedAt - startedAt;
  if (diffMs < 1000) {
    return `latency: ${diffMs}ms`;
  }

  const diffSeconds = Math.round(diffMs / 1000);
  if (diffSeconds < 60) {
    return `duration: ${diffSeconds}s`;
  }

  const minutes = Math.floor(diffSeconds / 60);
  const seconds = diffSeconds % 60;
  return `duration: ${minutes}m ${seconds}s`;
}

export function getErrorMessage(error: unknown): string {
  if (error instanceof Error) {
    return error.message;
  }

  return "unexpected error";
}

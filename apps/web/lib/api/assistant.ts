import type {
  AssistantCapabilities,
  AssistantConfirmTaskResult,
  AssistantConversation,
  AssistantSession,
  AssistantUploadResult
} from "../assistant/types";
import { apiFetch } from "./client";
export {
  streamAssistantConversation,
  streamAssistantMessage,
  toAssistantTurnError
} from "./assistant-stream";

export async function getAssistantSessions(): Promise<AssistantSession[]> {
  const response = await apiFetch<{ sessions: AssistantSession[] }>("/api/assistant/sessions");
  return response.sessions;
}

export async function getAssistantSession(sessionId: string): Promise<AssistantConversation> {
  return apiFetch<AssistantConversation>(`/api/assistant/sessions/${sessionId}`);
}

export async function getAssistantCapabilities(): Promise<AssistantCapabilities> {
  return apiFetch<AssistantCapabilities>("/api/assistant/capabilities");
}

export async function createAssistantConversation(message: string): Promise<AssistantConversation> {
  return apiFetch<AssistantConversation>("/api/assistant/conversations", {
    body: JSON.stringify({ message }),
    method: "POST"
  });
}

export async function appendAssistantMessage(
  sessionId: string,
  message: string
): Promise<AssistantConversation> {
  return apiFetch<AssistantConversation>(`/api/assistant/sessions/${sessionId}/messages`, {
    body: JSON.stringify({ message }),
    method: "POST"
  });
}

export async function uploadAssistantFile(
  sessionId: string,
  file: File
): Promise<AssistantUploadResult> {
  const formData = new FormData();
  formData.append("file", file);

  return apiFetch<AssistantUploadResult>(`/api/assistant/sessions/${sessionId}/files`, {
    body: formData,
    method: "POST"
  });
}

export async function confirmAssistantTaskSuggestion(
  messageId: string
): Promise<AssistantConfirmTaskResult> {
  return apiFetch<AssistantConfirmTaskResult>(`/api/assistant/task-suggestions/${messageId}/confirm`, {
    method: "POST"
  });
}

export async function deleteAssistantSession(sessionId: string): Promise<void> {
  await apiFetch<void>(`/api/assistant/sessions/${sessionId}`, {
    method: "DELETE"
  });
}

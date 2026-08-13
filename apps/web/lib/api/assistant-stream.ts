import type {
  AssistantMessage,
  AssistantSessionFileMessage,
  AssistantSession,
  AssistantStreamEvent,
  AssistantStreamRunResult,
  AssistantTaskSuggestionMessage,
  AssistantTurnError,
  AssistantTurnErrorCode
} from "../assistant/types";
import { ApiClientError, apiRequest } from "./client";

const STREAM_TIMEOUT_MS = 90_000;

type AssistantStreamRequestOptions = {
  maxResumeAttempts?: number;
  onEvent?: (event: AssistantStreamEvent) => void;
  requestId?: string;
  resourceId?: string;
  signal?: AbortSignal;
  timeoutMs?: number;
};

type ParsedSSEFrame = {
  data: string;
  event: string;
  id?: string;
};

type StreamErrorPayload = {
  code?: string;
  message?: string;
};

type StreamMessagePayload = {
  message: AssistantMessage;
};

type StreamSessionPayload = {
  session: AssistantSession;
};

type StreamDeltaPayload = {
  delta: string;
};

type StreamTurnStatePayload = {
  status?: string;
};

class AssistantStreamClientError extends Error {
  code: AssistantTurnErrorCode;

  constructor(code: AssistantTurnErrorCode, message: string) {
    super(message);
    this.code = code;
    this.name = "AssistantStreamClientError";
  }
}

const assistantTurnErrorCodes = new Set<AssistantTurnErrorCode>([
  "assistant_empty_reply",
  "backend_offline",
  "generation_stopped",
  "request_timeout",
  "service_error"
]);

export async function streamAssistantConversation(
  message: string,
  options: AssistantStreamRequestOptions = {}
): Promise<AssistantStreamRunResult> {
  return streamAssistant("/api/assistant/conversations/stream", message, options);
}

export async function streamAssistantMessage(
  sessionId: string,
  message: string,
  options: AssistantStreamRequestOptions = {}
): Promise<AssistantStreamRunResult> {
  return streamAssistant(`/api/assistant/sessions/${sessionId}/messages/stream`, message, options);
}

export async function parseSSEStream(
  stream: ReadableStream<Uint8Array> | null,
  onFrame: (frame: ParsedSSEFrame) => void
): Promise<void> {
  if (!stream) {
    throw new AssistantStreamClientError("service_error", "服务返回的流格式不正确。");
  }

  const reader = stream.getReader();
  const decoder = new TextDecoder();
  let buffer = "";

  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) {
        break;
      }

      buffer += decoder.decode(value, { stream: true });
      buffer = drainSSEBuffer(buffer, onFrame);
    }

    buffer += decoder.decode();
    drainSSEBuffer(buffer, onFrame, true);
  } finally {
    reader.releaseLock();
  }
}

export function toAssistantTurnError(error: unknown): AssistantTurnError {
  if (isAssistantTurnErrorLike(error)) {
    return error;
  }

  if (error instanceof AssistantStreamClientError) {
    return {
      code: error.code,
      message: error.message
    };
  }

  if (error instanceof ApiClientError) {
    return mapApiClientError(error);
  }

  if (isAbortError(error)) {
    return {
      code: "generation_stopped",
      message: "已停止生成。"
    };
  }

  if (error instanceof Error) {
    return {
      code: "service_error",
      message: error.message || "助手服务暂时不可用，请稍后重试。"
    };
  }

  return {
    code: "service_error",
    message: "助手服务暂时不可用，请稍后重试。"
  };
}

async function streamAssistant(
  path: string,
  message: string,
  options: AssistantStreamRequestOptions
): Promise<AssistantStreamRunResult> {
  const requestId = normalizeRequestId(options.requestId) || createRequestId();
  const maxResumeAttempts = Math.max(0, Math.min(options.maxResumeAttempts ?? 2, 5));
  let lastEventId = 0;

  // 复用同一请求标识和事件游标重连，确保服务端可以幂等地续传持久化事件。
  for (let attempt = 0; attempt <= maxResumeAttempts; attempt += 1) {
    let response: Response;
    const headers = new Headers({ "X-Request-ID": requestId });
    if (lastEventId > 0) {
      headers.set("Last-Event-ID", String(lastEventId));
    }

    try {
      response = await apiRequest(path, {
        body: JSON.stringify({
          message,
          ...(options.resourceId ? { resource_id: options.resourceId } : {})
        }),
        headers,
        method: "POST",
        signal: options.signal,
        timeoutMs: options.timeoutMs ?? STREAM_TIMEOUT_MS
      });
    } catch (error) {
      if (options.signal?.aborted && isAbortError(error)) {
        return stoppedResult();
      }
      if (attempt < maxResumeAttempts) {
        continue;
      }
      throw error;
    }

    if (!response.ok) {
      throw await buildHTTPStreamError(response);
    }
    if (!isEventStreamResponse(response)) {
      throw new AssistantStreamClientError("service_error", "服务返回的流格式不正确。");
    }

    let sawDone = false;
    let streamError: AssistantStreamClientError | null = null;
    try {
      await parseSSEStream(response.body, (frame) => {
        const sequence = Number.parseInt(frame.id || "", 10);
        if (Number.isSafeInteger(sequence) && sequence > lastEventId) {
          lastEventId = sequence;
        }
        const event = parseAssistantStreamEvent(frame);
        if (!event) {
          return;
        }
        options.onEvent?.(event);
        if (event.type === "done") {
          sawDone = true;
        }
        if (event.type === "error") {
          streamError = new AssistantStreamClientError(event.error.code, event.error.message);
        }
      });
    } catch (error) {
      if (options.signal?.aborted && isAbortError(error)) {
        return stoppedResult();
      }
      if (attempt < maxResumeAttempts) {
        continue;
      }
      throw error;
    }

    if (streamError) {
      throw streamError;
    }
    if (sawDone) {
      return { status: "completed" };
    }
    if (attempt === maxResumeAttempts) {
      throw new AssistantStreamClientError("service_error", "助手流式回复已中断，请使用同一请求继续。");
    }
  }

  throw new AssistantStreamClientError("service_error", "助手流式回复恢复失败。");
}

function drainSSEBuffer(
  input: string,
  onFrame: (frame: ParsedSSEFrame) => void,
  flushRemainder = false
): string {
  let buffer = input.replace(/\r\n/g, "\n");

  for (;;) {
    const boundary = buffer.indexOf("\n\n");
    if (boundary === -1) {
      break;
    }

    const block = buffer.slice(0, boundary).trim();
    buffer = buffer.slice(boundary + 2);
    if (block !== "") {
      onFrame(parseSSEBlock(block));
    }
  }

  if (flushRemainder && buffer.trim() !== "") {
    onFrame(parseSSEBlock(buffer.trim()));
    return "";
  }

  return buffer;
}

function parseSSEBlock(block: string): ParsedSSEFrame {
  let event = "message";
  let id: string | undefined;
  const dataLines: string[] = [];

  for (const line of block.split("\n")) {
    if (line.startsWith("id:")) {
      id = line.slice("id:".length).trim();
      continue;
    }
    if (line.startsWith("event:")) {
      event = line.slice("event:".length).trim();
      continue;
    }
    if (line.startsWith("data:")) {
      dataLines.push(line.slice("data:".length).trim());
    }
  }

  return {
    data: dataLines.join("\n"),
    event,
    id
  };
}

function parseAssistantStreamEvent(frame: ParsedSSEFrame): AssistantStreamEvent | null {
  switch (frame.event) {
    case "session_created": {
      const payload = parseJSON<StreamSessionPayload>(frame.data);
      return {
        session: payload.session,
        type: "session_created"
      };
    }
    case "message_started":
      return { type: "message_started" };
    case "session_file": {
      const payload = parseJSON<StreamMessagePayload>(frame.data);
      return {
        message: payload.message as AssistantSessionFileMessage,
        type: "session_file"
      };
    }
    case "message_delta": {
      const payload = parseJSON<StreamDeltaPayload>(frame.data);
      return {
        delta: payload.delta,
        type: "message_delta"
      };
    }
    case "message_completed": {
      const payload = parseJSON<StreamMessagePayload>(frame.data);
      return {
        message: payload.message,
        type: "message_completed"
      };
    }
    case "task_suggestion": {
      const payload = parseJSON<StreamMessagePayload>(frame.data);
      return {
        message: payload.message as AssistantTaskSuggestionMessage,
        type: "task_suggestion"
      };
    }
    case "error": {
      const payload = parseJSON<StreamErrorPayload>(frame.data);
      return {
        error: mapBackendStreamError(payload),
        type: "error"
      };
    }
    case "turn_state": {
      const payload = parseJSON<StreamTurnStatePayload>(frame.data);
      if (!isTurnState(payload.status)) {
        return null;
      }
      return { status: payload.status, type: "turn_state" };
    }
    case "done":
      return { type: "done" };
    default:
      return null;
  }
}

// isTurnState 判断服务端状态是否属于前端支持的持久化轮次状态。
function isTurnState(value: unknown): value is "running" | "waiting_input" | "waiting_approval" | "succeeded" | "failed" | "cancelled" {
  return typeof value === "string" && [
    "running", "waiting_input", "waiting_approval", "succeeded", "failed", "cancelled"
  ].includes(value);
}

// stoppedResult 构造用户主动停止生成时的统一返回结果。
function stoppedResult(): AssistantStreamRunResult {
  return {
    error: { code: "generation_stopped", message: "已停止生成。" },
    status: "stopped"
  };
}

// normalizeRequestId 清理调用方传入的请求标识，空值交由后续逻辑生成。
function normalizeRequestId(value: string | undefined): string {
  return typeof value === "string" ? value.trim() : "";
}

// createRequestId 优先生成随机 UUID，并为不支持该 API 的环境提供兼容标识。
function createRequestId(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `assistant-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function parseJSON<T>(value: string): T {
  try {
    return JSON.parse(value) as T;
  } catch {
    throw new AssistantStreamClientError("service_error", "服务返回的流格式不正确。");
  }
}

function mapBackendStreamError(payload: StreamErrorPayload): AssistantTurnError {
  switch (payload.code) {
    case "assistant_empty_reply":
      return {
        code: "assistant_empty_reply",
        message: payload.message || "本轮没有生成可展示内容。"
      };
    case "assistant_timeout":
      return {
        code: "request_timeout",
        message: payload.message || "助手响应超时，请稍后重试。"
      };
    default:
      return {
        code: "service_error",
        message: payload.message || "助手服务暂时不可用，请稍后重试。"
      };
  }
}

function mapApiClientError(error: ApiClientError): AssistantTurnError {
  switch (error.code) {
    case "backend_offline":
      return {
        code: "backend_offline",
        message: error.message
      };
    case "request_timeout":
      return {
        code: "request_timeout",
        message: error.message
      };
    default:
      return {
        code: "service_error",
        message: error.message
      };
  }
}

async function buildHTTPStreamError(response: Response): Promise<AssistantStreamClientError> {
  const raw = await response.text();
  const payload = tryParseJSON(raw);

  const message =
    typeof payload === "object" && payload && "error" in payload && typeof payload.error === "string"
      ? payload.error
      : `请求失败：${response.status}`;

  return new AssistantStreamClientError("service_error", message);
}

function isEventStreamResponse(response: Response): boolean {
  const contentType = response.headers.get("Content-Type") || "";
  return contentType.startsWith("text/event-stream");
}

function tryParseJSON(value: string): unknown {
  try {
    return JSON.parse(value);
  } catch {
    return value;
  }
}

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}

function isAssistantTurnErrorLike(error: unknown): error is AssistantTurnError {
  if (!error || typeof error !== "object" || error instanceof Error) {
    return false;
  }

  const candidate = error as Partial<AssistantTurnError>;
  return (
    typeof candidate.code === "string" &&
    assistantTurnErrorCodes.has(candidate.code as AssistantTurnErrorCode) &&
    typeof candidate.message === "string"
  );
}

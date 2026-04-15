import { buildPublicApiUrl } from "./public-url";

const DEFAULT_TIMEOUT_MS = 20_000;

export type ApiClientErrorCode = "backend_offline" | "request_timeout" | "service_error";

export class ApiClientError extends Error {
  code: ApiClientErrorCode;
  status?: number;

  constructor(code: ApiClientErrorCode, message: string, status?: number) {
    super(message);
    this.code = code;
    this.name = "ApiClientError";
    this.status = status;
  }
}

type ApiRequestInit = RequestInit & {
  timeoutMs?: number;
};

export async function apiFetch<T>(path: string, options: ApiRequestInit = {}): Promise<T> {
  const response = await apiRequest(path, options);
  const rawBody = await response.text();
  const body = rawBody ? tryParseJSON(rawBody) : null;

  if (!response.ok) {
    const errorMessage =
      typeof body === "object" && body && "error" in body && typeof body.error === "string"
        ? body.error
        : `请求失败：${response.status}`;
    throw new ApiClientError("service_error", errorMessage, response.status);
  }

  return body as T;
}

export async function apiRequest(path: string, options: ApiRequestInit = {}): Promise<Response> {
  const { timeoutMs = DEFAULT_TIMEOUT_MS, ...requestOptions } = options;
  const headers = new Headers(options.headers);
  const isFormData =
    typeof FormData !== "undefined" && options.body instanceof FormData;

  if (!headers.has("Content-Type") && options.body !== undefined && !isFormData) {
    headers.set("Content-Type", "application/json");
  }

  const timeoutController = new AbortController();
  const signal = mergeAbortSignals(requestOptions.signal, timeoutController.signal);
  let didTimeout = false;

  const timeoutId = globalThis.setTimeout(() => {
    didTimeout = true;
    timeoutController.abort();
  }, timeoutMs);

  try {
    return await fetch(buildPublicApiUrl(path), {
      ...requestOptions,
      cache: "no-store",
      headers,
      signal
    });
  } catch (error) {
    throw normalizeApiRequestError(error, {
      didTimeout,
      signal: requestOptions.signal
    });
  } finally {
    globalThis.clearTimeout(timeoutId);
  }
}

function tryParseJSON(value: string): unknown {
  try {
    return JSON.parse(value);
  } catch {
    return value;
  }
}

function mergeAbortSignals(...signals: Array<AbortSignal | null | undefined>): AbortSignal | undefined {
  const activeSignals = signals.filter(Boolean) as AbortSignal[];
  if (activeSignals.length === 0) {
    return undefined;
  }
  if (activeSignals.length === 1) {
    return activeSignals[0];
  }

  const controller = new AbortController();
  const abort = () => controller.abort();
  for (const signal of activeSignals) {
    if (signal.aborted) {
      controller.abort();
      break;
    }
    signal.addEventListener("abort", abort, { once: true });
  }

  return controller.signal;
}

function normalizeApiRequestError(
  error: unknown,
  options: {
    didTimeout: boolean;
    signal?: AbortSignal | null;
  }
): unknown {
  if (options.signal?.aborted && isAbortError(error) && !options.didTimeout) {
    return error;
  }

  if (options.didTimeout) {
    return new ApiClientError("request_timeout", "请求超时，请稍后重试。");
  }

  if (error instanceof ApiClientError) {
    return error;
  }

  if (error instanceof TypeError) {
    return new ApiClientError("backend_offline", "后端未连接，请确认本地 server 已启动。");
  }

  if (error instanceof Error) {
    return new ApiClientError("service_error", error.message);
  }

  return new ApiClientError("service_error", "请求失败，请稍后重试。");
}

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}

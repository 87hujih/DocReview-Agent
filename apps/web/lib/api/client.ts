const BASE_URL = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080";

export async function apiFetch<T>(path: string, options: RequestInit = {}): Promise<T> {
  const headers = new Headers(options.headers);
  const isFormData =
    typeof FormData !== "undefined" && options.body instanceof FormData;

  if (!headers.has("Content-Type") && options.body !== undefined && !isFormData) {
    headers.set("Content-Type", "application/json");
  }

  const response = await fetch(`${BASE_URL}${path}`, {
    ...options,
    cache: "no-store",
    headers
  });

  const rawBody = await response.text();
  const body = rawBody ? tryParseJSON(rawBody) : null;

  if (!response.ok) {
    const errorMessage =
      typeof body === "object" && body && "error" in body && typeof body.error === "string"
        ? body.error
        : `请求失败：${response.status}`;
    throw new Error(errorMessage);
  }

  return body as T;
}

function tryParseJSON(value: string): unknown {
  try {
    return JSON.parse(value);
  } catch {
    return value;
  }
}

const DEFAULT_PUBLIC_API_URL = "http://127.0.0.1:18080";

export function getPublicApiBaseURL(): string {
  return process.env.NEXT_PUBLIC_API_URL || DEFAULT_PUBLIC_API_URL;
}

export function buildPublicApiUrl(path: string): string {
  return `${getPublicApiBaseURL()}${path}`;
}

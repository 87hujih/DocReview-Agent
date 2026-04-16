import { buildPublicApiUrl } from "./public-url";

export function getFileDownloadURL(fileId: string): string {
  return buildPublicApiUrl(`/api/files/${encodeURIComponent(fileId)}/download`);
}

export function getFileDownloadURL(fileId: string): string {
  const baseURL = process.env.NEXT_PUBLIC_API_URL ?? "http://127.0.0.1:18080";
  return `${baseURL}/api/files/${encodeURIComponent(fileId)}/download`;
}

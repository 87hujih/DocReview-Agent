export function getFileDownloadURL(fileId: string): string {
  return `/api/files/${fileId}/download`;
}

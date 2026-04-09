import { apiFetch } from "./client";

export interface Resource {
  id: string;
  title: string;
  source_type: string;
  created_at: string;
}

export interface ResourceVersion {
  id: string;
  version_number: number;
  content: string;
  source: string;
  created_at: string;
}

export interface Citation {
  citation_id: string;
  resource_id: string;
  section_title: string;
  snippet: string;
}

export interface ResourceDetailsResponse {
  resource: Resource;
  current_version: ResourceVersion | null;
}

export function getResources(): Promise<Resource[]> {
  return apiFetch<{ resources: Resource[] }>("/api/resources").then((response) => response.resources);
}

export function getResource(id: string): Promise<ResourceDetailsResponse> {
  return apiFetch<ResourceDetailsResponse>(`/api/resources/${id}`);
}

export function searchResource(id: string, query: string): Promise<Citation[]> {
  return apiFetch<{ citations: Citation[] }>(
    `/api/resources/${id}/search?q=${encodeURIComponent(query)}`
  ).then((response) => response.citations);
}

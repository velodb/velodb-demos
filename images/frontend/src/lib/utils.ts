import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

/**
 * API base URL for the RAG backend
 * Uses nginx proxy /rag-api in production, localhost:8000 in development
 */
export const RAG_API_BASE_URL = import.meta.env.VITE_RAG_API_URL || "/rag-api";

/**
 * Multimodal data structure from API responses
 * Contains asset_id (Lance) and/or path (legacy) for image/table/formula content
 */
export interface MultimodalData {
  asset_id?: string;  // SHA256 hash: "sha256:abc123..."
  path?: string;      // Original file path: "images/diagram.png"
  caption?: string;   // Alt text or description
}

/**
 * Get the URL for an asset based on multimodal data
 *
 * Priority:
 * 1. If asset_id exists, use new Lance endpoint: /api/v1/assets/{tenant}/{corpus}/{asset_id}
 * 2. Otherwise, use legacy path endpoint: /api/v1/images/{path}
 *
 * @param multimodalData - Object containing asset_id and/or path
 * @param tenantId - Tenant identifier (default: "VeloDB Sample")
 * @param corpusId - Corpus identifier (default: "velodb_docs")
 * @returns Full URL to fetch the asset
 */
export function getAssetUrl(
  multimodalData: MultimodalData | string | null | undefined,
  tenantId: string = "VeloDB Sample",
  corpusId: string = "velodb_docs"
): string {
  // Handle null/undefined
  if (!multimodalData) {
    return "";
  }

  // Handle legacy string path
  if (typeof multimodalData === "string") {
    // If it's already a full URL or API path, use it directly
    if (multimodalData.startsWith("/api/") || multimodalData.startsWith("http")) {
      return multimodalData.startsWith("http")
        ? multimodalData
        : `${RAG_API_BASE_URL}${multimodalData}`;
    }
    // Legacy path format
    return `${RAG_API_BASE_URL}/api/v1/images/${encodeURIComponent(multimodalData)}`;
  }

  // Handle object with asset_id or path
  const { asset_id, path } = multimodalData;

  // Prefer asset_id (new Lance storage)
  if (asset_id) {
    return `${RAG_API_BASE_URL}/api/v1/assets/${encodeURIComponent(tenantId)}/${encodeURIComponent(corpusId)}/${encodeURIComponent(asset_id)}`;
  }

  // Fallback to path (legacy storage)
  if (path) {
    return `${RAG_API_BASE_URL}/api/v1/images/${encodeURIComponent(path)}`;
  }

  return "";
}

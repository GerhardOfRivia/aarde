import type { Polygon, MultiPolygon } from "geojson";
export type Area = Polygon | MultiPolygon;
export type Basemap = "osm" | "offline" | "none";
export interface Imagery {
  format: "GTiff" | "NITF" | null;
  id: string;
  catalog_id: string;
  image_id: string;
  display_name: string;
  acquired_at: string | null;
  cloud_cover: number | null;
  imported_at: string;
  created_at: string;
  footprint: Area;
  checksum: string;
  asset_location: string;
  segments?: ImageSegment[];
  width: number;
  height: number;
  band_count: number;
  source_crs: string;
  metadata: Record<string, unknown>;
}
export interface ImageSegment {
  index: number;
  width: number;
  height: number;
  band_count: number;
  source_crs: string;
  acquired_at: string | null;
  cloud_cover: number | null;
  footprint: Area;
  metadata: Record<string, unknown>;
}
export interface Page {
  items: Imagery[];
  limit: number;
  offset: number;
  has_more: boolean;
}
export interface Search {
  catalog: string;
  ids?: string[];
  geometry?: Area;
  cloudCoverLT?: number;
  cloudCoverLTE?: number;
  cloudCoverUnknown?: "include" | "exclude" | "only";
  acquiredFrom?: string;
  acquiredBefore?: string;
}

export interface AccessInfo {
  version: string;
  public_read: boolean;
  authenticated: boolean;
  read_only: boolean;
  basemap: Basemap;
}

export class APIError extends Error {
  constructor(public status: number, message: string) {
    super(message);
    this.name = "APIError";
  }
}

export async function request<T>(
  url: string,
  token: string,
  options?: RequestInit,
): Promise<T> {
  const headers = new Headers(options?.headers);
  headers.set("Accept", "application/json");
  if (token) headers.set("Authorization", `Bearer ${token}`);
  const response = await fetch(url, { ...options, headers, cache: "no-store" });
  if (!response.ok) {
    let message = `Request failed (${response.status})`;
    try {
      const body = await response.json();
      message = body.error?.message ?? message;
    } catch {
      // Preserve the HTTP status when a proxy sends a non-JSON response.
    }
    throw new APIError(response.status, message);
  }
  return response.json();
}
// Empty input is an omitted filter; an explicit zero remains a threshold.
export function parseCloudCoverLT(raw: string): number | undefined {
  if (raw.trim() === "") return undefined;
  const value = Number(raw);
  if (!Number.isFinite(value) || value < 0 || value > 100) {
    throw new Error("Scene cloud cover must be a finite percentage between 0 and 100.");
  }
  return value;
}

export function search(
  token: string,
  criteria: Search,
  offset: number,
  signal: AbortSignal,
): Promise<Page> {
  if (criteria.geometry)
    return request("/api/v1/imagery/search", token, {
      method: "POST",
      signal,
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        catalog_id: criteria.catalog,
        geometry: criteria.geometry,
        image_ids: criteria.ids,
        acquired_from: criteria.acquiredFrom,
        acquired_before: criteria.acquiredBefore,
        cloud_cover_lte: criteria.cloudCoverLTE,
        cloud_cover_unknown: criteria.cloudCoverUnknown,
        cloud_cover_lt: criteria.cloudCoverLT,
        limit: 50,
        offset,
      }),
    });
  const query = new URLSearchParams({ limit: "50", offset: String(offset) });
  if (criteria.cloudCoverLT !== undefined) query.set("cloud_cover_lt", String(criteria.cloudCoverLT));
  if (criteria.cloudCoverLTE !== undefined) query.set("cloud_cover_lte", String(criteria.cloudCoverLTE));
  if (criteria.cloudCoverUnknown) query.set("cloud_cover_unknown", criteria.cloudCoverUnknown);
  if (criteria.acquiredFrom) query.set("acquired_from", criteria.acquiredFrom);
  if (criteria.acquiredBefore) query.set("acquired_before", criteria.acquiredBefore);
  if (criteria.catalog) query.set("catalog_id", criteria.catalog);
  criteria.ids?.forEach((id) => query.append("image_id", id));
  return request(`/api/v1/imagery?${query}`, token, { signal });
}

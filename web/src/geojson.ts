import type { Area } from "./types";
import type { Polygon } from "geojson";

const maxBytes = 1 << 20;
const maxVertices = 10_000;

function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new Error("Paste a GeoJSON geometry, Feature, or FeatureCollection.");
  }
  const result = value as Record<string, unknown>;
  if (result.crs != null) {
    throw new Error("Use WGS84 longitude/latitude GeoJSON without a crs member. Convert projected coordinates before loading.");
  }
  return result;
}

// Normalize common GeoJSON wrappers to the API's geometry-only search body.
// Structural limits match internal/geo; PostGIS validates topology at search time.
export function parseSearchArea(text: string): Area {
  if (!text.trim()) throw new Error("Paste GeoJSON to load a search area.");
  if (new TextEncoder().encode(text).byteLength > maxBytes) {
    throw new Error("GeoJSON must be no larger than 1 MiB.");
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    throw new Error("Invalid JSON. Check the quotes, commas, and brackets.");
  }

  let vertices = 0;
  function polygon(value: unknown): Polygon["coordinates"] {
    if (!Array.isArray(value) || !value.length) {
      throw new Error("Each polygon must have an exterior ring.");
    }
    return value.map((ring: unknown) => {
      if (!Array.isArray(ring) || ring.length < 4) {
        throw new Error("Each polygon ring needs at least four positions, including the closing position.");
      }
      vertices += ring.length;
      if (vertices > maxVertices) {
        throw new Error("The search area must contain at most 10,000 positions.");
      }
      const positions = ring.map((position: unknown) => {
        if (!Array.isArray(position) || position.length !== 2 ||
            !position.every((n) => typeof n === "number" && Number.isFinite(n)) ||
            position[0] < -180 || position[0] > 180 ||
            position[1] < -90 || position[1] > 90) {
          throw new Error("Positions must be [longitude, latitude], within −180 to 180 and −90 to 90 degrees.");
        }
        return [position[0], position[1]];
      });
      for (let i = 1; i < positions.length; i++) {
        if (Math.abs(positions[i][0] - positions[i - 1][0]) > 180) {
          throw new Error("Rings crossing the antimeridian must be split into a MultiPolygon.");
        }
      }
      const first = positions[0], last = positions[positions.length - 1];
      if (first[0] !== last[0] || first[1] !== last[1]) {
        throw new Error("Polygon rings must be closed: the last position must match the first.");
      }
      return positions;
    });
  }
  function geometry(value: unknown): Area {
    const input = object(value);
    if (input.type === "Polygon") {
      return { type: "Polygon", coordinates: polygon(input.coordinates) };
    }
    if (input.type === "MultiPolygon") {
      if (!Array.isArray(input.coordinates) || !input.coordinates.length) {
        throw new Error("A MultiPolygon must contain at least one polygon.");
      }
      return { type: "MultiPolygon", coordinates: input.coordinates.map(polygon) };
    }
    throw new Error("Only Polygon and MultiPolygon geometries can be used as search areas.");
  }
  function feature(value: unknown): Area {
    const input = object(value);
    if (input.type !== "Feature") {
      throw new Error("A FeatureCollection must contain only polygon Features.");
    }
    return geometry(input.geometry);
  }

  const input = object(parsed);
  if (input.type === "Feature") return feature(input);
  if (input.type === "FeatureCollection") {
    if (!Array.isArray(input.features) || !input.features.length) {
      throw new Error("A FeatureCollection must contain at least one polygon Feature.");
    }
    const areas = input.features.map(feature);
    if (areas.length === 1) return areas[0];
    return {
      type: "MultiPolygon",
      coordinates: areas.flatMap((area) =>
        area.type === "Polygon" ? [area.coordinates] : area.coordinates,
      ),
    };
  }
  return geometry(input);
}

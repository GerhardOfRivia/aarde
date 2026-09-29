import type { Area, Search } from "./types";

export interface FilterDraft {
  catalog: string;
  scope: "anywhere" | "area";
  geometry?: Area;
  from: string;
  through: string;
  cloudMode: "any" | "max" | "unknown";
  maximum: string;
  includeUnknown: boolean;
  ids: string;
}
export type FilterErrors = Partial<Record<"area" | "from" | "through" | "maximum" | "ids", string>>;
export const defaultFilters: FilterDraft = {
  catalog: "", scope: "anywhere", from: "", through: "",
  cloudMode: "any", maximum: "20", includeUnknown: false, ids: "",
};
const imageIDs = (raw: string) => [...new Set(raw.split(/[\n,]+/).map((id) => id.trim()).filter(Boolean))].sort();

// Ignore inactive controls and cosmetic input differences when detecting pending changes.
export function filterKey(draft: FilterDraft): string {
  const maximum = draft.maximum.trim() && Number.isFinite(Number(draft.maximum)) ? Number(draft.maximum) : draft.maximum;
  return JSON.stringify([
    draft.catalog, draft.scope, draft.scope === "area" ? draft.geometry : null,
    draft.from, draft.through, draft.cloudMode,
    draft.cloudMode === "max" ? maximum : null,
    draft.cloudMode === "max" && draft.includeUnknown, imageIDs(draft.ids),
  ]);
}
export function resetFilters(draft: FilterDraft): FilterDraft {
  return { ...defaultFilters, scope: draft.scope, geometry: draft.geometry };
}

// Calendar dates are parsed and advanced in UTC, including leap days and year boundaries.
function midnight(raw: string): Date | undefined {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(raw) || raw.startsWith("0000")) return undefined;
  const value = new Date(raw + "T00:00:00Z");
  return Number.isFinite(value.getTime()) && value.toISOString().slice(0, 10) === raw ? value : undefined;
}
export function buildSearch(draft: FilterDraft): { criteria: Search; errors: FilterErrors } {
  const errors: FilterErrors = {};
  const criteria: Search = { catalog: draft.catalog };
  if (draft.scope === "area") {
    if (!draft.geometry) errors.area = "Draw or load a completed polygon first.";
    else criteria.geometry = structuredClone(draft.geometry);
  }
  const from = midnight(draft.from), through = midnight(draft.through);
  if (draft.from && !from) errors.from = "Enter a valid From date.";
  if (draft.through && !through) errors.through = "Enter a valid Through date.";
  if (from) criteria.acquiredFrom = from.toISOString();
  if (through) {
    through.setUTCDate(through.getUTCDate() + 1);
    if (through.getUTCFullYear() > 9999) errors.through = "Choose a date before December 31, 9999.";
    else criteria.acquiredBefore = through.toISOString();
  }
  if (from && through && from >= through) errors.through = "Through must be on or after From.";
  if (draft.cloudMode === "unknown") criteria.cloudCoverUnknown = "only";
  if (draft.cloudMode === "max") {
    const value = Number(draft.maximum);
    if (!draft.maximum.trim() || !Number.isFinite(value) || value < 0 || value > 100) {
      errors.maximum = "Enter a maximum from 0 to 100%.";
    } else criteria.cloudCoverLTE = value;
    criteria.cloudCoverUnknown = draft.includeUnknown ? "include" : "exclude";
  }
  const ids = imageIDs(draft.ids);
  if (ids.length) criteria.ids = ids;
  if (ids.length > 100) errors.ids = "Enter at most 100 exact image IDs.";
  else if (ids.some((id) => new TextEncoder().encode(id).length > 255 || /[/\\\x00-\x1f\x7f]/.test(id))) {
    errors.ids = "IDs must be at most 255 bytes, without slashes or control characters.";
  }
  return { criteria, errors };
}

export type FilterGroup = "catalog" | "area" | "dates" | "cloud" | "ids";
export function removeFilter(draft: FilterDraft, group: FilterGroup): FilterDraft {
  switch (group) {
    case "catalog": return { ...draft, catalog: "" };
    case "area": return { ...draft, scope: "anywhere" };
    case "dates": return { ...draft, from: "", through: "" };
    case "cloud": return { ...draft, cloudMode: "any" };
    case "ids": return { ...draft, ids: "" };
  }
}
export function appliedFilters(draft: FilterDraft): { group: FilterGroup; label: string }[] {
  const chips: { group: FilterGroup; label: string }[] = [];
  if (draft.catalog) chips.push({ group: "catalog", label: `Catalog: ${draft.catalog}` });
  if (draft.scope === "area") chips.push({ group: "area", label: "Drawn / loaded area" });
  if (draft.from || draft.through) chips.push({ group: "dates", label: `${draft.from || "Any start"} – ${draft.through || "Any end"} · UTC` });
  if (draft.cloudMode === "max") chips.push({ group: "cloud", label: `Cloud ≤ ${Number(draft.maximum)}%${draft.includeUnknown ? " + unknown" : ""}` });
  if (draft.cloudMode === "unknown") chips.push({ group: "cloud", label: "Cloud: unknown only" });
  const ids = imageIDs(draft.ids);
  if (ids.length) chips.push({ group: "ids", label: ids.length > 2 ? `IDs: ${ids.length} exact IDs` : `IDs: ${ids.join(", ")}` });
  return chips;
}

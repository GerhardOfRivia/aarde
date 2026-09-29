import { useState } from "react";
import { Button, Checkbox, FormControlLabel, MenuItem, TextField } from "@mui/material";
import type { FilterDraft, FilterErrors } from "./filters";
import { resetFilters } from "./filters";

interface Props {
  draft: FilterDraft;
  catalogs: string[];
  errors: FilterErrors;
  dirty: boolean;
  loading: boolean;
  drawing: boolean;
  onChange(draft: FilterDraft): void;
  onSearch(): void;
  onClearArea(): void;
}
export function CatalogFilters({ draft, catalogs, errors, dirty, loading, drawing, onChange, onSearch, onClearArea }: Props) {
  const [expanded, setExpanded] = useState(() => !window.matchMedia("(max-width: 760px)").matches);
  const [more, setMore] = useState(false);
  const change = (patch: Partial<FilterDraft>) => onChange({ ...draft, ...patch });
  const hasErrors = Object.keys(errors).length > 0;
  const open = expanded || hasErrors;
  return <form className="catalog-filters" noValidate onSubmit={(event) => {
    event.preventDefault();
    // A partially typed native date has an empty value but is not a cleared filter.
    const incompleteDate = event.currentTarget.querySelector<HTMLInputElement>('input[type="date"]:invalid');
    if (incompleteDate) { incompleteDate.reportValidity(); return; }
    onSearch();
  }}>
    <div className="filter-heading">
      <div><span className="eyebrow">YOUR COLLECTION</span><h1>Explore imagery</h1></div>
      <div className="filter-actions">
        <TextField select label="Catalog" size="small" value={draft.catalog} onChange={(event) => change({ catalog: event.target.value })} className="catalog-input" slotProps={{ select: { displayEmpty: true }, inputLabel: { shrink: true } }}>
          <MenuItem value="">All catalogs</MenuItem>
          {catalogs.map((catalog) => <MenuItem key={catalog} value={catalog}>{catalog}</MenuItem>)}
        </TextField>
        <Button aria-expanded={open} aria-controls="filter-editor" onClick={() => setExpanded(!open)}>{open ? "Hide filters" : "Show filters"}</Button>
        <Button type="submit" variant="contained" disabled={drawing}>{loading ? "Searching…" : "Search catalog"}</Button>
      </div>
    </div>
    <div id="filter-editor" hidden={!open}>
      <div className="filter-columns">
        <fieldset><legend>Area</legend>
          <TextField select fullWidth size="small" label="Search within" value={draft.scope} onChange={(event) => change({ scope: event.target.value as FilterDraft["scope"] })} error={!!errors.area} helperText={errors.area}>
            <MenuItem value="anywhere">Anywhere</MenuItem>
            <MenuItem value="area" disabled={!draft.geometry || drawing}>Use drawn or loaded area</MenuItem>
          </TextField>
          <p className="filter-hint">{draft.geometry ? draft.scope === "area" ? "Matches footprints that intersect your area, including partial overlap." : "Your polygon is retained. Searching all locations." : "Draw on the map or load GeoJSON to search an area."}</p>
          <Button size="small" disabled={!draft.geometry && !drawing} onClick={onClearArea}>Clear area</Button>
        </fieldset>
        <fieldset><legend>Acquired date</legend>
          <div className="filter-stack">
            <TextField label="From · UTC" type="date" size="small" value={draft.from} onChange={(event) => change({ from: event.target.value })} error={!!errors.from} helperText={errors.from} slotProps={{ inputLabel: { shrink: true } }} />
            <TextField label="Through · UTC" type="date" size="small" value={draft.through} onChange={(event) => change({ through: event.target.value })} error={!!errors.through} helperText={errors.through} slotProps={{ inputLabel: { shrink: true } }} />
          </div>
          <p className="filter-hint">Whole UTC days included. Undated imagery is excluded when dates are set.</p>
        </fieldset>
        <fieldset><legend>Scene cloud cover</legend>
          <div className="filter-stack">
            <TextField select fullWidth size="small" label="Cloud condition" value={draft.cloudMode} onChange={(event) => change({ cloudMode: event.target.value as FilterDraft["cloudMode"] })}>
              <MenuItem value="any">Any cloud cover</MenuItem><MenuItem value="max">At most</MenuItem><MenuItem value="unknown">Unknown only</MenuItem>
            </TextField>
            {draft.cloudMode === "max" && <TextField label="Maximum (%)" type="number" size="small" value={draft.maximum} onChange={(event) => change({ maximum: event.target.value })} error={!!errors.maximum} helperText={errors.maximum} slotProps={{ htmlInput: { min: 0, max: 100, step: "any" } }} />}
          </div>
          <div className="filter-presets" aria-label="Cloud cover presets">
            <Button size="small" variant="outlined" aria-pressed={draft.cloudMode === "any"} onClick={() => change({ cloudMode: "any" })}>Any</Button>
            {[0, 10, 20, 50].map((value) => <Button key={value} size="small" variant="outlined" aria-pressed={draft.cloudMode === "max" && draft.maximum.trim() !== "" && Number(draft.maximum) === value} onClick={() => change({ cloudMode: "max", maximum: String(value) })}>{value}%</Button>)}
          </div>
          {draft.cloudMode === "max" && <FormControlLabel className="filter-checkbox" control={<Checkbox size="small" checked={draft.includeUnknown} onChange={(event) => change({ includeUnknown: event.target.checked })} />} label="Include unknown cloud cover" />}
          <p className="filter-hint">Reported for the whole scene, not just your selected area.</p>
        </fieldset>
      </div>
      <details className="more-filters" open={more || !!errors.ids} onToggle={(event) => setMore(event.currentTarget.open)}>
        <summary>More filters</summary>
        <TextField label="Exact image IDs" placeholder="ABC123, IMG002" size="small" multiline maxRows={4} value={draft.ids} onChange={(event) => change({ ids: event.target.value })} error={!!errors.ids} helperText={errors.ids || "Separate IDs with commas or new lines. Matches any listed ID and all other filters."} fullWidth />
      </details>
    </div>
    <div className="filter-footer">
      <Button size="small" onClick={() => onChange(resetFilters(draft))}>Reset filters</Button>
      <span role="status" className={dirty ? "filter-pending" : "filter-hint"}>{dirty ? "Changes not applied" : "Filters applied"}</span>
      {drawing && <span className="filter-hint">Finish drawing before searching.</span>}
    </div>
  </form>;
}

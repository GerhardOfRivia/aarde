import assert from "node:assert/strict";
import test from "node:test";
import { defaultFilters, buildSearch, filterKey, resetFilters, removeFilter, appliedFilters } from "../src/filters.ts";

const area = { type: "Polygon", coordinates: [[[0,0],[2,0],[2,2],[0,0]]] };
test("UTC inclusive days handle leap years, DST dates, open bounds and year rollover", () => {
  for (const [day, next] of [["2024-02-29","2024-03-01"], ["2026-03-08","2026-03-09"], ["2026-12-31","2027-01-01"]]) {
    const { criteria, errors } = buildSearch({ ...defaultFilters, from: day, through: day });
    assert.deepEqual(errors, {});
    assert.equal(criteria.acquiredFrom, day + "T00:00:00.000Z");
    assert.equal(criteria.acquiredBefore, next + "T00:00:00.000Z");
  }
  assert.equal(buildSearch({ ...defaultFilters, through: "2026-09-29" }).criteria.acquiredFrom, undefined);
  assert.equal(buildSearch({ ...defaultFilters, from: "2026-06-01" }).criteria.acquiredBefore, undefined);
  for (const from of ["2026-02-29", "2026-13-01", "0000-01-01", "bad"]) assert.ok(buildSearch({ ...defaultFilters, from }).errors.from);
  assert.ok(buildSearch({ ...defaultFilters, from: "2026-09-30", through: "2026-09-29" }).errors.through);
  assert.ok(buildSearch({ ...defaultFilters, through: "9999-12-31" }).errors.through);
});
test("cloud modes preserve inclusive boundaries and ignore inactive fields", () => {
  for (const maximum of ["0","19.9","20","100"]) {
    for (const includeUnknown of [false,true]) {
      const { criteria, errors } = buildSearch({ ...defaultFilters, cloudMode: "max", maximum, includeUnknown });
      assert.deepEqual(errors, {});
      assert.equal(criteria.cloudCoverLTE, Number(maximum));
      assert.equal(criteria.cloudCoverUnknown, includeUnknown ? "include" : "exclude");
      assert.equal(criteria.cloudCoverLT, undefined);
    }
  }
  for (const maximum of ["", " ", "-1", "100.1", "NaN", "Infinity", "1e400"]) assert.ok(buildSearch({ ...defaultFilters, cloudMode: "max", maximum }).errors.maximum);
  assert.deepEqual(buildSearch({ ...defaultFilters, maximum: "bad" }), { criteria: { catalog: "" }, errors: {} });
  assert.equal(buildSearch({ ...defaultFilters, cloudMode: "unknown" }).criteria.cloudCoverUnknown, "only");
});
test("IDs combine with geometry and snapshots do not share mutable geometry", () => {
  const draft = { ...defaultFilters, catalog: "demo", scope: "area", geometry: structuredClone(area), ids: "two, one\none, " };
  const { criteria, errors } = buildSearch(draft);
  assert.deepEqual(errors, {});
  assert.deepEqual(criteria.ids, ["one", "two"]);
  assert.deepEqual(criteria.geometry, area);
  draft.geometry.coordinates[0][0][0] = 5;
  assert.deepEqual(criteria.geometry, area);
  assert.ok(buildSearch({ ...defaultFilters, scope: "area" }).errors.area);
  assert.ok(buildSearch({ ...defaultFilters, ids: "bad/id" }).errors.ids);
  assert.ok(buildSearch({ ...defaultFilters, ids: "é".repeat(128) }).errors.ids);
  assert.ok(buildSearch({ ...defaultFilters, ids: Array.from({ length: 101 }, (_, i) => String(i)).join(",") }).errors.ids);
});
test("reset preserves spatial scope; chip removal retains geometry and applied snapshot", () => {
  const applied = { ...defaultFilters, catalog: "demo", scope: "area", geometry: area, from: "2026-06-01", cloudMode: "max", ids: "one" };
  const key = filterKey(applied);
  assert.deepEqual(resetFilters(applied), { ...defaultFilters, scope: "area", geometry: area });
  const removed = removeFilter(applied, "area");
  assert.equal(removed.scope, "anywhere");
  assert.deepEqual(removed.geometry, area);
  assert.equal(removed.from, applied.from);
  assert.equal(filterKey(applied), key);
  assert.notEqual(filterKey(removed), key);
  assert.equal(appliedFilters(applied).length, 5);
  assert.equal(appliedFilters(removed).some((chip) => chip.group === "area"), false);
  assert.equal(filterKey(defaultFilters), filterKey({ ...defaultFilters, geometry: area, maximum: "80", includeUnknown: true }));
  assert.equal(filterKey({ ...applied, ids: "one,two" }), filterKey({ ...applied, ids: " two, one, one " }));
});

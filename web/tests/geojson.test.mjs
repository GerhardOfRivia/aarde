import assert from "node:assert/strict";
import test from "node:test";
import GeoJSON from "ol/format/GeoJSON.js";
import { parseSearchArea } from "../src/geojson.ts";

const ring = [[-106, 39], [-105, 39], [-105, 40], [-106, 39]];
const hole = [[-105.6, 39.2], [-105.4, 39.2], [-105.4, 39.3], [-105.6, 39.2]];
const polygon = { type: "Polygon", coordinates: [ring, hole] };
const feature = (geometry) => ({ type: "Feature", properties: { name: "Boundary" }, geometry });
const parse = (value) => parseSearchArea(JSON.stringify(value));

test("loads a polygon with holes and strips metadata from the API geometry", () => {
  assert.deepEqual(parse({ ...polygon, bbox: [-106, 39, -105, 40] }), polygon);
  assert.deepEqual(parse(feature(polygon)), polygon);
  assert.deepEqual(parse({ type: "FeatureCollection", features: [feature(polygon)] }), polygon);
});

test("combines every polygon feature, including MultiPolygons, into one search area", () => {
  const multi = { type: "MultiPolygon", coordinates: [[ring], [ring, hole]] };
  assert.deepEqual(parse(multi), multi);
  assert.deepEqual(parse({ type: "FeatureCollection", features: [feature(polygon), feature(multi)] }), {
    type: "MultiPolygon", coordinates: [[ring, hole], [ring], [ring, hole]],
  });
});

test("loaded areas survive the map projection and remain valid search bodies", () => {
  const format = new GeoJSON();
  const projection = { dataProjection: "EPSG:4326", featureProjection: "EPSG:3857" };
  for (const input of [polygon, { type: "MultiPolygon", coordinates: [[ring], [ring, hole]] }]) {
    const area = parse(input);
    const mapGeometry = format.readGeometry(area, projection);
    const roundTrip = parse(format.writeGeometryObject(mapGeometry, projection));
    assert.equal(roundTrip.type, area.type);
    const before = area.coordinates.flat(Infinity), after = roundTrip.coordinates.flat(Infinity);
    assert.equal(after.length, before.length);
    after.forEach((value, index) => assert.ok(Math.abs(value - before[index]) < 1e-8));
  }
});

test("rejects bad JSON and unsupported or missing geometry without silently dropping features", () => {
  for (const text of ["", "   ", "{", "{} {}", "null", "[]"]) {
    assert.throws(() => parseSearchArea(text), Error);
  }
  for (const value of [
    {}, { type: "Point", coordinates: [1, 2] }, { type: "GeometryCollection", geometries: [polygon] },
    feature(null), feature({ type: "Point", coordinates: [1, 2] }),
    { type: "FeatureCollection", features: [] },
    { type: "FeatureCollection", features: [polygon] },
    { type: "FeatureCollection", features: [feature(polygon), feature(null)] },
    { type: "FeatureCollection", features: [feature(polygon), feature({ type: "Point", coordinates: [1, 2] })] },
    { ...polygon, crs: { type: "name", properties: { name: "EPSG:3857" } } },
  ]) assert.throws(() => parse(value), Error);
});

test("rejects empty, unclosed, nonnumeric, 3D, out-of-range, and dateline-crossing rings", () => {
  for (const coordinates of [
    [], [[]], [ring.slice(0, 3)], [[...ring.slice(0, 3), [-104, 40]]],
    [[null, ...ring.slice(1)]], [[["-106", 39], ...ring.slice(1)]],
    [[[181, 39], ...ring.slice(1)]], [[[-106, 91], ...ring.slice(1)]],
    [[[...ring[0], 10], ...ring.slice(1)]],
    [[[179, 0], [-179, 0], [-179, 1], [179, 0]]],
  ]) assert.throws(() => parse({ type: "Polygon", coordinates }), Error);
  assert.throws(() => parse({ type: "MultiPolygon", coordinates: [] }), Error);
  assert.throws(() => parseSearchArea('{"type":"Polygon","coordinates":[[[1e400,0],[1,0],[1,1],[1e400,0]]]}'), Error);
});

test("enforces total vertex and byte limits across the whole pasted collection", () => {
  const many = { type: "Polygon", coordinates: [Array.from({ length: 10_000 }, (_, i) => ring[i % 4])] };
  assert.equal(parse(many).coordinates[0].length, 10_000);
  assert.throws(() => parse({ type: "FeatureCollection", features: [feature(many), feature(polygon)] }), /10,000/);
  assert.throws(() => parseSearchArea(" ".repeat(1 << 20) + JSON.stringify(polygon)), /1 MiB/);
});

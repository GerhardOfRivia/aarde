import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";
import { createRequire } from "node:module";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import ts from "typescript";
import { commercialNITF, commercialSummary } from "../src/commercialNITF.ts";

const source = await readFile(new URL("../src/CommercialNITF.tsx", import.meta.url), "utf8");
const { outputText } = ts.transpileModule(source, { compilerOptions: {
  target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX,
} });
const require = createRequire(import.meta.url);
const exports = {};
new Function("require", "exports", outputText)((name) => name === "./commercialNITF" ? { commercialNITF, commercialSummary } : require(name), exports);
const render = metadata => renderToStaticMarkup(React.createElement(exports.CommercialNITF, { metadata }));
const annotation = {
  version: 1, role: "dataset", profile: { candidate: "NCDRD", status: "possible", conformance: "not_evaluated" },
  values: [{ name: "sensor_name", value: '<img src=x onerror="alert(1)">', units: "code/text", semantic_scope: "segment", source_path: "metadata/xml:TRE" },
    { name: "geo_mean_gsd", value: 0, units: "m", semantic_scope: "sensor_sub_image", source_path: "metadata/xml:TRE" }],
  cloud: { value: 1, selected_source: "PIAIMC.CLOUDCVR", conflicting: true },
  diagnostics: [{ code: "cloud_source_conflict", message: "<script>alert(1)</script>" }],
};
test("old records and unsupported enrichment are unavailable, not classified", () => {
  for (const metadata of [{}, { "": { NITF_ISORCE: "DigitalGlobe NCDRD" } }, { _aarde: { format: "NITF" } }, { _aarde: { commercial_nitf: { version: 2 } } }]) {
    assert.equal(commercialNITF(metadata), null);
    assert.match(render(metadata), /Enrichment unavailable/);
  }
});
test("stored evidence and warning markup is escaped and legitimate zero is displayed", () => {
  const metadata = { _aarde: { commercial_nitf: annotation } };
  const html = render(metadata);
  assert.match(html, /NCDRD — possible/);
  assert.match(html, /Conformance not evaluated/);
  assert.match(html, /1%/);
  assert.match(html, /PIAIMC.CLOUDCVR/);
  assert.match(html, /0 m/);
  assert.match(html, /&lt;script&gt;/);
  assert.match(html, /&lt;img/);
  assert.doesNotMatch(html, /<script|<img/);
  assert.match(html, /<details>/);
});
test("unknown is explicit and an auxiliary role is shown only when stored", () => {
  const html = render({ _aarde: { commercial_nitf: { ...annotation, role: "cloud_grid", profile: { ...annotation.profile, status: "unknown" } } } });
  assert.match(html, /Profile unknown/);
  assert.match(html, /Reported role: cloud grid/);
  assert.doesNotMatch(html, /NCDRD — likely/);
});

test("segment acquisition is displayed separately from dataset collection/processing fields", () => {
  const html = renderToStaticMarkup(React.createElement(exports.CommercialNITF, {
    metadata: { _aarde: { commercial_nitf: annotation } }, acquiredAt: "2026-09-10T12:00:01Z",
  }));
  assert.match(html, /Acquisition/);
  assert.match(html, /2026-09-10T12:00:01Z/);
});

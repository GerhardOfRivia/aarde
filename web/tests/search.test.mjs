import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import ts from "typescript";

// Use the existing TypeScript compiler because APIError has a parameter property.
const source = await readFile(new URL("../src/types.ts", import.meta.url), "utf8");
const { outputText } = ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext },
});
const { parseCloudCoverLT, search } = await import(`data:text/javascript;base64,${Buffer.from(outputText).toString("base64")}`);
const geometry = { type: "Polygon", coordinates: [[[0, 0], [2, 0], [2, 2], [0, 0]]] };

test("optional cloud-cover input preserves zero and decimal percentages", () => {
  for (const raw of ["", "   "]) assert.equal(parseCloudCoverLT(raw), undefined);
  for (const raw of ["0", "19.9", "20", "100"]) assert.equal(parseCloudCoverLT(raw), Number(raw));
  for (const raw of ["-1", "100.01", "NaN", "Infinity", "-Infinity", "1e400", "bad", "20%"])
    assert.throws(() => parseCloudCoverLT(raw), /finite percentage between 0 and 100/);
});

test("GET and spatial requests retain cloud cover with other filters and pagination", async (t) => {
  const calls = [];
  t.mock.method(globalThis, "fetch", async (url, options) => {
    calls.push({ url: new URL(url, "http://aarde.test"), options });
    return new Response(JSON.stringify({ items: [], limit: 50, offset: 50, has_more: false }));
  });
  const signal = new AbortController().signal;
  for (const spatial of [false, true]) {
    for (const cloudCoverLT of [undefined, 0, 19.9, 20, 100]) {
      const criteria = { catalog: "example", cloudCoverLT, ...(spatial ? { geometry } : { ids: ["one", "two"] }) };
      for (const offset of [0, 50, 100]) {
        await search("test-token", criteria, offset, signal);
        const { url, options } = calls.at(-1);
        assert.equal(options.signal, signal);
        assert.equal(options.headers.get("Authorization"), "Bearer test-token");
        if (spatial) {
          assert.equal(url.pathname, "/api/v1/imagery/search");
          assert.equal(options.method, "POST");
          const body = JSON.parse(options.body);
          assert.equal(Object.hasOwn(body, "cloud_cover_lt"), cloudCoverLT !== undefined);
          assert.equal(body.cloud_cover_lt, cloudCoverLT);
          assert.deepEqual(body.geometry, geometry);
          assert.equal(body.catalog_id, "example");
          assert.equal(body.offset, offset);
          assert.equal(body.limit, 50);
        } else {
          assert.equal(url.pathname, "/api/v1/imagery");
          assert.equal(url.searchParams.get("cloud_cover_lt"), cloudCoverLT === undefined ? null : String(cloudCoverLT));
          assert.deepEqual(url.searchParams.getAll("image_id"), ["one", "two"]);
          assert.equal(url.searchParams.get("catalog_id"), "example");
          assert.equal(url.searchParams.get("offset"), String(offset));
          assert.equal(url.searchParams.get("limit"), "50");
        }
      }
    }
  }
});

test("cleared input omits the filter in the next GET or spatial search", async (t) => {
  const calls = [];
  t.mock.method(globalThis, "fetch", async (url, options) => {
    calls.push({ url: new URL(url, "http://aarde.test"), options });
    return new Response(JSON.stringify({ items: [], limit: 50, offset: 0, has_more: false }));
  });
  for (const spatial of [false, true]) {
    const criteria = { catalog: "example", ...(spatial ? { geometry } : {}) };
    for (const raw of ["20", "0", ""]) {
      await search("", { ...criteria, cloudCoverLT: parseCloudCoverLT(raw) }, 0, new AbortController().signal);
    }
    const { url, options } = calls.at(-1);
    assert.equal(url.searchParams.has("cloud_cover_lt"), false);
    if (spatial) assert.equal(Object.hasOwn(JSON.parse(options.body), "cloud_cover_lt"), false);
  }
});

test("new metadata filters and exact IDs are identical across GET and spatial POST pages", async (t) => {
  const calls = [];
  t.mock.method(globalThis, "fetch", async (url, options) => {
    calls.push({ url: new URL(url, "http://aarde.test"), options });
    return new Response(JSON.stringify({ items: [], limit: 50, offset: 50, has_more: false }));
  });
  for (const cloudCoverUnknown of [undefined, "include", "exclude", "only"]) {
    for (const spatial of [false, true]) {
      for (const offset of [0, 50, 100]) {
        const criteria = { catalog: "demo", ids: ["one", "two"], acquiredFrom: "2026-06-01T00:00:00Z", acquiredBefore: "2026-09-30T00:00:00Z", cloudCoverLTE: cloudCoverUnknown === "only" ? undefined : 19.9, cloudCoverUnknown, ...(spatial ? { geometry } : {}) };
        await search("", criteria, offset, new AbortController().signal);
        const { url, options } = calls.at(-1);
        const wire = spatial ? JSON.parse(options.body) : Object.fromEntries(url.searchParams);
        assert.equal(wire.acquired_from, criteria.acquiredFrom);
        assert.equal(wire.acquired_before, criteria.acquiredBefore);
        assert.equal(wire.cloud_cover_unknown, cloudCoverUnknown);
        assert.equal(wire.cloud_cover_lte === undefined ? undefined : Number(wire.cloud_cover_lte), criteria.cloudCoverLTE);
        assert.equal(Number(wire.offset), offset);
        assert.deepEqual(spatial ? wire.image_ids : url.searchParams.getAll("image_id"), criteria.ids);
      }
    }
  }
});

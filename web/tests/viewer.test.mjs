import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import ts from 'typescript';
const require = createRequire(import.meta.url);
async function module(path, dependencies = {}) {
  const source = await readFile(new URL(path, import.meta.url), 'utf8');
  const exports = {};
  const { outputText } = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX } });
  new Function('require', 'exports', outputText)(name => dependencies[name] ?? require(name), exports);
  return exports;
}
const viewer = await module('../src/viewer.ts');
const { ViewerLinks } = await module('../src/ViewerLinks.tsx', { './viewer': viewer });
const { viewerURL, viewerReference, ViewerLoader } = viewer;
const tick = () => new Promise(resolve => setTimeout(resolve, 0));
const layers = Array.from({ length: 5 }, (_, i) => ({ id: `segment-${i}`, label: `Segment ${i}`, role: i === 4 ? 'cloud_grid' : 'imagery', order: i, opacity: i === 4 ? .4 : 1 }));
test('selected catalog imagery has safely encoded real navigation and new-tab links', async () => {
  for (const catalog of ['default', 'a & b']) for (const image of ['same id', 'a?#é']) {
    const html = renderToStaticMarkup(React.createElement(ViewerLinks, { catalog, image }));
    const href = viewerURL(catalog, image).replaceAll('&', '&amp;');
    assert.equal(html.split(`href="${href}"`).length - 1, 2);
    assert.match(html, /Open image viewer/); assert.match(html, /target="_blank"/);
    assert.deepEqual(viewerReference(new URL(viewerURL(catalog, image), 'http://aarde').search), { catalog, image });
  }
  const app = await readFile(new URL('../src/App.tsx', import.meta.url), 'utf8');
  assert.match(app, /<ViewerLinks catalog=\{selected.catalog_id\} image=\{selected.image_id\}/);
  assert.notEqual(viewerURL('one', 'same'), viewerURL('two', 'same'));
});
test('direct URL resolution rejects missing, repeated, path, URL and malformed references', () => {
  assert.deepEqual(viewerReference('?image=scene'), { catalog: 'default', image: 'scene' });
  for (const q of ['', '?catalog=default', '?image=', '?image=x&image=y', '?catalog=x&catalog=y&image=z', '?image=%zz', '?image=%FF', '?image=%2Ftmp%2Fa.ntf', '?image=https%3A%2F%2Fx', '?image=NITF_IM%3A0%3A%2Ftmp%2Fa']) assert.throws(() => viewerReference(q));
});
test('all layers load with bounded concurrency; opacity/visibility do not reload pixels', async () => {
  let active = 0, peak = 0, calls = 0; const pending = [];
  const manager = new ViewerLoader(layers, async layer => { calls++; active++; peak = Math.max(active, peak); await new Promise(resolve => pending.push(() => { active--; resolve(); })); return { id: layer.id, close() {} }; }, () => {});
  manager.start(); assert.equal(calls, 2);
  while ([...manager.entries.values()].some(e => e.state !== 'loaded')) { pending.splice(0).forEach(f => f()); await tick(); }
  assert.equal(calls, 5); assert.equal(peak, 2);
  manager.opacity('segment-2', .25); manager.visibility('segment-3', false);
  assert.equal(manager.entries.get('segment-2').opacity, .25); assert.equal(manager.entries.get('segment-1').opacity, 1);
  assert.equal(manager.entries.get('segment-4').opacity, .4); assert.equal(manager.entries.get('segment-0').visible, true); assert.equal(calls, 5);
  manager.dispose();
});
test('cloud failure preserves imagery, retry loads only failed layer, disposal aborts and closes stale results', async () => {
  const calls = []; let fail = true, closed = 0;
  const manager = new ViewerLoader(layers, async layer => { calls.push(layer.id); if (fail && layer.role === 'cloud_grid') throw new Error('Cloud failed'); return { close() { closed++; } }; }, () => {});
  manager.start(); await tick(); await tick();
  assert.equal(manager.entries.get('segment-4').state, 'error'); assert.equal(manager.entries.get('segment-0').state, 'loaded');
  fail = false; manager.retry('segment-4'); await tick(); assert.equal(calls.length, 6); manager.dispose(); assert.equal(closed, 5);
  let finish, signal; const late = new ViewerLoader(layers, async (_, s) => { signal = s; await new Promise(r => { finish = r; }); return { close() { closed++; } }; }, () => {}, 1);
  late.start(); late.dispose(); assert.equal(signal.aborted, true); finish(); await tick(); assert.equal(closed, 6); assert.equal(late.entries.get('segment-1').state, 'queued');
});
test('viewer uses one non-tiled compositing canvas and does not fit on asynchronous load completion', async () => {
  const canvas = await readFile(new URL('../src/ViewerCanvas.ts', import.meta.url), 'utf8');
  const page = await readFile(new URL('../src/ImageViewer.tsx', import.meta.url), 'utf8');
  assert.doesNotMatch(canvas, /ol\/source\/(XYZ|OSM|WMTS|Tile)|TileLayer|fetch\(/);
  assert.match(canvas, /imageSmoothingEnabled = false/); assert.match(canvas, /fill\('evenodd'\)/);
  assert.match(page, /if \(!fitted.current\)/); assert.match(page, /Authorization: `Bearer/);
  assert.match(page, /bitmap\.close\(\)/); assert.doesNotMatch(page, /createObjectURL|setInterval/);
});

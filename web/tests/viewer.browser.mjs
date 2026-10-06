// Optional real-browser regression suite: npm run test:browser. Install
// Playwright/Chromium, or set PLAYWRIGHT_MODULE to an existing Playwright module.
// HTTP pixels are deterministic browser fixtures; native GDAL/HTTP decoding is
// independently exercised by internal/raster and internal/api integration tests.
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { join, extname } from 'node:path';
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
const root = fileURLToPath(new URL('../dist', import.meta.url));
const server = createServer(async (req, res) => {
  const path = new URL(req.url, 'http://local').pathname;
  try { const data = await readFile(join(root, path === '/' || path === '/image-viewer' ? 'index.html' : path));
    res.setHeader('Content-Type', ({ '.js': 'text/javascript', '.css': 'text/css', '.html': 'text/html' })[extname(path)] || (path === '/' || path === '/image-viewer' ? 'text/html' : 'application/octet-stream')); res.end(data);
  } catch { res.writeHead(404); res.end(); }
});
await new Promise(r => server.listen(0, '127.0.0.1', r));
const base = `http://127.0.0.1:${server.address().port}`;
const browser = await chromium.launch({ headless: true, args: ['--no-sandbox'] });
try {
  const context = await browser.newContext({ viewport: { width: 1280, height: 850 } });
  const page = await context.newPage(), failures = [], requests = [];
  page.on('pageerror', e => failures.push(e.message));
  const pngs = await page.evaluate(() => {
    return ['#ff0000', '#00ff00', '#0000ff', '#1ed7ff'].map(fill => { const c = document.createElement('canvas'); c.width = 8; c.height = 8; const ctx = c.getContext('2d'); ctx.fillStyle = fill; ctx.fillRect(0, 0, 8, 8); return c.toDataURL().split(',')[1]; });
  });
  const extent = [0, -8, 8, 0], mesh = [[0, 0], [8, 0], [0, -8], [8, -8]], revision = 'a'.repeat(64);
  const items = ['default', 'other'].map((catalog_id, index) => ({ id: String(index), catalog_id, image_id: 'same ? & image', display_name: 'fixture.ntf', format: 'NITF', footprint: { type: 'Polygon', coordinates: [[[0,0],[1,0],[1,1],[0,0]]] }, metadata: {}, width: 8, height: 8, band_count: 1, source_crs: '', checksum: revision, asset_location: '/test/fixture.ntf', acquired_at: null, cloud_cover: null, imported_at: '2026-01-01', created_at: '2026-01-01' }));
  let cloudFail = false;
  await context.route('**/api/v1/**', async route => {
    const req = route.request(), url = new URL(req.url()); requests.push(url.pathname);
    const auth = req.headers().authorization === 'Bearer test-token';
    const json = (value, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(value) });
    if (url.pathname === '/api/v1/info') return json({ version: 'browser-test', public_read: true, authenticated: auth, read_only: true, basemap: 'none' });
    if (url.pathname === '/api/v1/catalogs') return json({ items: ['default', 'other'] });
    if (url.pathname === '/api/v1/imagery') return json({ items, limit: 50, offset: 0, has_more: false });
    if (!auth) return json({ error: { message: 'Token required' } }, 401);
    if (url.pathname.endsWith('/viewer')) {
      const cat = decodeURIComponent(url.pathname.split('/')[4]);
      if (cat === 'missing') return json({ error: { message: 'imagery not found' } }, 404);
      const layers = pngs.map((_, i) => ({ id: `segment-${i}`, label: i === 3 ? 'Cloud grid' : `Segment ${i}`, role: i === 3 ? 'cloud_grid' : 'imagery', segment_index: i, width: 8, height: 8, display_width: 8, display_height: 8, extent, mesh, mesh_size: 1, registration: 'Registered', placement: 'Fixture registration', content_url: `${url.pathname}/layers/segment-${i}/image.png?revision=${revision}`, content_type: 'image/png', opacity: i === 3 ? .4 : 1, order: i, rendering: 'nearest', warnings: [] }));
      return json({ catalog_id: cat, image_id: items[0].image_id, name: `fixture-${cat}.ntf`, revision, format: 'NITF', coordinate_system: 'image pixels', extent, layers, resolution: 'auto', native_available: true, display_pixels: 256, estimated_memory_bytes: 4096, cloud_status: 'present', warnings: [] });
    }
    const m = url.pathname.match(/segment-(\d)\/image.png$/);
    if (m) { if (cloudFail && m[1] === '3') return json({ error: { message: 'Cloud decode failed' } }, 415);
      return route.fulfill({ contentType: 'image/png', body: Buffer.from(pngs[Number(m[1])], 'base64') }); }
    return json({ error: { message: 'not found' } }, 404);
  });
  await page.goto(base);
  await page.getByRole('link', { name: 'Open image viewer', exact: true }).first().waitFor();
  assert.equal(await page.getByRole('link', { name: 'Open image viewer', exact: true }).count(), 2);
  assert.equal(await page.locator('a[target="_blank"][href^="/image-viewer"]').count(), 2);
  const url = await page.getByRole('link', { name: 'Open image viewer', exact: true }).first().getAttribute('href');
  await page.getByRole('link', { name: 'Open image viewer', exact: true }).first().click();
  await page.getByRole('button', { name: 'Sign in to open Image Viewer' }).click();
  await page.getByLabel('Web access token').fill('test-token'); await page.getByRole('button', { name: 'Open catalog', exact: true }).click();
  await page.waitForFunction(() => [...document.querySelectorAll('.viewer-layer')].filter(e => e.textContent.includes('· loaded')).length === 4);
  assert.equal(new URL(page.url()).search, new URL(url, base).search, 'authentication lost requested URL');
  const count = () => requests.filter(r => r.endsWith('/image.png')).length;
  const initial = count(); assert.equal(initial, 4);
  const pixel = () => page.locator('.viewer-canvas canvas').evaluate(c => Array.from(c.getContext('2d').getImageData(c.width / 2, c.height / 2, 1, 1).data));
  await page.getByRole('checkbox', { name: 'Show Cloud grid', exact: true }).uncheck();
  await page.getByRole('slider', { name: 'Segment 2 opacity', exact: true }).fill('50');
  await page.waitForTimeout(100);
  let color = await pixel(); assert.ok(Math.abs(color[1] - 128) < 3 && Math.abs(color[2] - 128) < 3, `independent opacity composite: ${color}`);
  assert.equal(await page.getByRole('slider', { name: 'Segment 1 opacity', exact: true }).inputValue(), '100');
  await page.getByRole('button', { name: 'Zoom in', exact: true }).click();
  const box = await page.locator('.viewer-canvas').boundingBox();
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2); await page.mouse.wheel(0, -100);
  await page.mouse.down(); await page.mouse.move(box.x + box.width / 2 + 30, box.y + box.height / 2 + 30, { steps: 8 }); await page.mouse.up();
  await page.getByRole('button', { name: 'Fit all', exact: true }).click(); await page.waitForTimeout(200);
  assert.equal(count(), initial, 'pan/zoom/opacity/visibility requested pixels');
  assert.ok(!requests.some(r => /tile|\.pbf|wmts/i.test(r)), 'viewer used tile requests');
  await page.getByLabel('Display resolution', { exact: true }).selectOption('preview');
  await page.waitForFunction(() => [...document.querySelectorAll('.viewer-layer')].filter(e => e.textContent.includes('· loaded')).length === 4);
  assert.equal(count(), initial + 4, 'resolution change must reload whole layers exactly once');
  assert.equal(await page.getByRole('slider', { name: 'Segment 2 opacity', exact: true }).inputValue(), '50');
  assert.equal(await page.getByRole('checkbox', { name: 'Show Cloud grid', exact: true }).isChecked(), false);
  // Direct copied URL, refresh and history resolution retain both identity parts.
  await page.goto(base + url.replace('catalog=default', 'catalog=other'));
  await page.getByText('fixture-other.ntf · other · NITF').waitFor();
  await page.reload(); await page.getByText('fixture-other.ntf · other · NITF').waitFor();
  await page.goBack(); await page.getByText('fixture-default.ntf · default · NITF').waitFor();
  await page.goForward(); await page.getByText('fixture-other.ntf · other · NITF').waitFor();
  cloudFail = true; await page.reload();
  await page.getByText('Cloud decode failed', { exact: true }).waitFor();
  assert.equal(await page.locator('.viewer-layer').filter({ hasText: '· loaded' }).count(), 3);
  const beforeRetry = count(); cloudFail = false; await page.getByRole('button', { name: 'Reload layer', exact: true }).click();
  await page.waitForFunction(() => [...document.querySelectorAll('.viewer-layer')].every(e => e.textContent.includes('· loaded')));
  assert.equal(count(), beforeRetry + 1);
  await page.screenshot({ path: process.env.AARDE_VIEWER_SCREENSHOT || '/tmp/aarde-image-viewer.png' });
  await page.goto(`${base}/image-viewer?catalog=missing&image=unknown`); await page.getByText('imagery not found', { exact: false }).waitFor();
  await page.goto(`${base}/image-viewer`); await page.getByText('The image query parameter is required exactly once.', { exact: false }).waitFor();
  assert.deepEqual(failures, []);
  console.log('PASS browser: links, copied URLs, auth redirect preservation, refresh/history, independent opacity pixels, pan/zoom request counts, cloud failure/retry, missing references.');
} finally { await browser.close(); await new Promise(r => server.close(r)); }

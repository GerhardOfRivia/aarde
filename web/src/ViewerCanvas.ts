import Map from 'ol/Map';
import View from 'ol/View';
import Layer from 'ol/layer/Layer';
import Projection from 'ol/proj/Projection';
import type { FrameState } from 'ol/Map';
import type { FeatureCollection, Polygon, MultiPolygon } from 'geojson';
import type { Extent, LayerEntry, ViewerLayer } from './viewer';

export interface ViewerPixels {
  bitmap?: ImageBitmap;
  geometry?: FeatureCollection<Polygon | MultiPolygon>;
  close(): void;
}
function point(frame: FrameState, xy: number[]): [number, number] {
  const t = frame.coordinateToPixelTransform;
  return [t[0] * xy[0] + t[2] * xy[1] + t[4], t[1] * xy[0] + t[3] * xy[1] + t[5]];
}
// Draw a source triangle through its affine map to the screen. No pixel fetch,
// crop service, intermediate mosaic, or data-dependent view request is involved.
function triangle(ctx: CanvasRenderingContext2D, bitmap: ImageBitmap, source: number[][], target: number[][]) {
  const [s0, s1, s2] = source, [p0, p1, p2] = target;
  const sx1 = s1[0] - s0[0], sy1 = s1[1] - s0[1], sx2 = s2[0] - s0[0], sy2 = s2[1] - s0[1];
  const det = sx1 * sy2 - sx2 * sy1;
  const a = ((p1[0] - p0[0]) * sy2 - (p2[0] - p0[0]) * sy1) / det;
  const c = (sx1 * (p2[0] - p0[0]) - sx2 * (p1[0] - p0[0])) / det;
  const b = ((p1[1] - p0[1]) * sy2 - (p2[1] - p0[1]) * sy1) / det;
  const d = (sx1 * (p2[1] - p0[1]) - sx2 * (p1[1] - p0[1])) / det;
  ctx.save(); ctx.beginPath(); ctx.moveTo(...p0 as [number, number]); ctx.lineTo(...p1 as [number, number]); ctx.lineTo(...p2 as [number, number]); ctx.closePath(); ctx.clip();
  ctx.transform(a, b, c, d, p0[0] - a * s0[0] - c * s0[1], p0[1] - b * s0[0] - d * s0[1]);
  ctx.drawImage(bitmap, 0, 0); ctx.restore();
}
function raster(ctx: CanvasRenderingContext2D, frame: FrameState, l: ViewerLayer, bitmap: ImageBitmap) {
  const n = l.mesh_size, pts = l.mesh.map(p => point(frame, p));
  ctx.imageSmoothingEnabled = false;
  // Affine registration (including rotation/shear) needs no clipped mesh seams.
  if (n === 1) {
    const [a, b, c] = pts;
    ctx.save(); ctx.transform((b[0] - a[0]) / bitmap.width, (b[1] - a[1]) / bitmap.width, (c[0] - a[0]) / bitmap.height, (c[1] - a[1]) / bitmap.height, a[0], a[1]); ctx.drawImage(bitmap, 0, 0); ctx.restore(); return;
  }
  for (let y = 0; y < n; y++) for (let x = 0; x < n; x++) {
    const a = y * (n + 1) + x, b = a + 1, c = a + n + 1, d = c + 1;
    const sx = x * bitmap.width / n, sy = y * bitmap.height / n, ex = (x + 1) * bitmap.width / n, ey = (y + 1) * bitmap.height / n;
    triangle(ctx, bitmap, [[sx, sy], [ex, sy], [sx, ey]], [pts[a], pts[b], pts[c]]);
    triangle(ctx, bitmap, [[ex, sy], [ex, ey], [sx, ey]], [pts[b], pts[d], pts[c]]);
  }
}
export class ViewerCanvas {
  readonly map: Map;
  private canvas = document.createElement('canvas');
  private layer: Layer;
  constructor(target: HTMLElement, entries: () => Iterable<LayerEntry<ViewerPixels>>) {
    const canvas = this.canvas;
    canvas.style.position = 'absolute'; canvas.style.inset = '0';
    this.layer = new Layer({ render(frame) {
      // One compositing surface for the entire scene, independent of layer count.
      const ratio = Math.min(window.devicePixelRatio || 1, 2, 4096 / Math.max(...frame.size));
      canvas.width = Math.ceil(frame.size[0] * ratio); canvas.height = Math.ceil(frame.size[1] * ratio);
      canvas.style.width = `${frame.size[0]}px`; canvas.style.height = `${frame.size[1]}px`;
      const ctx = canvas.getContext('2d')!; ctx.scale(ratio, ratio);
      for (const entry of entries()) {
        if (!entry.visible || !entry.data || entry.opacity === 0) continue;
        ctx.globalAlpha = entry.opacity;
        if (entry.data.bitmap) raster(ctx, frame, entry.layer, entry.data.bitmap);
        if (entry.data.geometry) {
          ctx.fillStyle = '#1ed7ff'; ctx.strokeStyle = '#15c9f7'; ctx.lineWidth = 2;
          for (const f of entry.data.geometry.features) {
            const polygons = f.geometry.type === 'Polygon' ? [f.geometry.coordinates] : f.geometry.coordinates;
            for (const poly of polygons) {
              ctx.beginPath();
              for (const ring of poly) { ring.forEach((v, i) => { const xy = point(frame, v); if (i === 0) ctx.moveTo(...xy); else ctx.lineTo(...xy); }); ctx.closePath(); }
              if (entry.layer.rendering !== 'outline') ctx.fill('evenodd'); ctx.stroke();
            }
          }
        }
      }
      return canvas;
    } });
    this.map = new Map({ target, layers: [this.layer], view: new View({ projection: new Projection({ code: 'AARDE_IMAGE', units: 'pixels' }), center: [0, 0], resolution: 1, maxResolution: 1e9, minResolution: 1 / 256, constrainResolution: false }), controls: [] });
  }
  changed() { this.layer.changed(); }
  fit(extent: Extent) { this.map.getView().fit(extent, { size: this.map.getSize(), padding: [30, 30, 30, 30] }); }
  zoom(delta: number) { const view = this.map.getView(); view.animate({ zoom: (view.getZoom() ?? 0) + delta, duration: 150 }); }
  dispose() { this.map.setTarget(undefined); this.map.dispose(); this.canvas.width = 0; this.canvas.height = 0; }
}

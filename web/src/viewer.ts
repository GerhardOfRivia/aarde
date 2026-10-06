export type Extent = [number, number, number, number];
export interface ViewerLayer {
  id: string; label: string; role: 'imagery' | 'cloud_grid' | 'cloud_shapes';
  segment_index?: number; des_index?: number;
  width: number; height: number; display_width: number; display_height: number;
  extent: Extent; mesh: [number, number][]; mesh_size: number;
  registration: string; placement: string; content_url: string; content_type: string;
  opacity: number; order: number; rendering: string;
  legend?: Record<string, string>; unsupported?: string; warnings: string[];
}
export interface ViewerManifest {
  catalog_id: string; image_id: string; name: string; revision: string; format: string;
  coordinate_system: string; extent: Extent; layers: ViewerLayer[];
  resolution: string; native_available: boolean; display_pixels: number; estimated_memory_bytes: number;
  cloud_status: string; warnings: string[];
  ncdrd_assessment?: { status: string; conformance: string };
}
export function viewerURL(catalog: string, image: string) {
  return `/image-viewer?${new URLSearchParams({ catalog, image })}`;
}
export function viewerReference(search: string): { catalog: string; image: string } {
  // URLSearchParams tolerates broken percent escapes, so reject them explicitly.
  if (/%(?![\da-f]{2})/i.test(search)) throw new Error('Malformed image URL.');
  const q = new URLSearchParams(search);
  if (q.getAll('image').length !== 1 || q.getAll('catalog').length > 1) throw new Error('The image query parameter is required exactly once.');
  const image = q.get('image') ?? '', catalog = q.get('catalog') ?? 'default';
  const valid = (s: string) => s.length > 0 && new TextEncoder().encode(s).length <= 255 && s.trim() === s && !/[\x00-\x1f\x7f/\\\ufffd]/u.test(s);
  if (!valid(image) || !valid(catalog)) throw new Error('Invalid catalog or image ID. Use catalog identifiers, not file paths or URLs.');
  return { catalog, image };
}
export function manifestURL(catalog: string, image: string, resolution: string) {
  return `/api/v1/imagery/${encodeURIComponent(catalog)}/${encodeURIComponent(image)}/viewer?${new URLSearchParams({ resolution })}`;
}

export type LayerState = 'queued' | 'loading' | 'loaded' | 'error';
export interface LoadedLayer { close(): void }
export interface LayerEntry<T extends LoadedLayer> {
  layer: ViewerLayer; state: LayerState; error: string; visible: boolean; opacity: number; data?: T;
}
// One request queue per manifest. UI controls never call load; stale results are
// closed even when decoding finishes after fetch was aborted.
export class ViewerLoader<T extends LoadedLayer> {
  readonly entries = new Map<string, LayerEntry<T>>();
  private queue: string[] = [];
  private active = 0;
  private controller = new AbortController();
  constructor(layers: ViewerLayer[], private load: (layer: ViewerLayer, signal: AbortSignal) => Promise<T>, private changed: () => void, private concurrency = 2) {
    for (const layer of [...layers].sort((a, b) => a.order - b.order || a.id.localeCompare(b.id))) {
      this.entries.set(layer.id, { layer, state: layer.unsupported ? 'error' : 'queued', error: layer.unsupported ?? '', visible: true, opacity: layer.opacity });
      if (!layer.unsupported) this.queue.push(layer.id);
    }
  }
  start() { this.pump(); }
  private pump() {
    while (!this.controller.signal.aborted && this.active < this.concurrency && this.queue.length) {
      const entry = this.entries.get(this.queue.shift()!)!;
      entry.state = 'loading'; this.active++; this.changed();
      void this.load(entry.layer, this.controller.signal).then(data => {
        if (this.controller.signal.aborted) { data.close(); return; }
        entry.data = data; entry.state = 'loaded'; entry.error = '';
      }).catch((error: unknown) => {
        if (this.controller.signal.aborted) return;
        entry.state = 'error'; entry.error = error instanceof Error ? error.message : 'Layer failed to load.';
      }).finally(() => {
        this.active--;
        if (!this.controller.signal.aborted) { this.changed(); this.pump(); }
      });
    }
  }
  retry(id: string) {
    const entry = this.entries.get(id);
    if (!entry || entry.state !== 'error' || entry.layer.unsupported || this.controller.signal.aborted) return;
    entry.error = ''; entry.state = 'queued'; this.queue.push(id); this.pump();
  }
  opacity(id: string, value: number) { const e = this.entries.get(id); if (e) { e.opacity = Math.min(1, Math.max(0, value)); this.changed(); } }
  visibility(id: string, value: boolean) { const e = this.entries.get(id); if (e) { e.visible = value; this.changed(); } }
  dispose() { this.controller.abort(); this.queue = []; for (const e of this.entries.values()) { e.data?.close(); e.data = undefined; } }
}

import { useEffect, useReducer, useRef, useState } from 'react';
import { Alert, Button } from '@mui/material';
import type { FeatureCollection, Polygon, MultiPolygon } from 'geojson';
import type { Session } from './Access';
import { ThemeControl } from './Access';
import { APIError, request } from './types';
import { manifestURL, viewerReference, ViewerLoader } from './viewer';
import type { ViewerManifest, ViewerLayer } from './viewer';
import { ViewerCanvas } from './ViewerCanvas';
import { ViewerProgress } from './ViewerProgress';
import type { ViewerPixels } from './ViewerCanvas';
import 'ol/ol.css';

export default function ImageViewer({ session, query }: { session: Session; query: string }) {
  const target = useRef<HTMLDivElement>(null), canvas = useRef<ViewerCanvas | null>(null);
  const loader = useRef<ViewerLoader<ViewerPixels> | null>(null), fitted = useRef(false);
  const settings = useRef(new Map<string, { opacity: number; visible: boolean }>());
  const [manifest, setManifest] = useState<ViewerManifest | null>(null);
  const [resolution, setResolution] = useState('auto'), [attempt, retry] = useReducer(n => n + 1, 0);
  const [error, setError] = useState(''), [loading, setLoading] = useState(session.info.authenticated);
  const [, redraw] = useReducer(n => n + 1, 0);
  const { token, info, unauthorized } = session;

  useEffect(() => {
    document.title = 'Image Viewer · Aarde';
    if (!target.current || !info.authenticated) return;
    const view = new ViewerCanvas(target.current, () => loader.current?.entries.values() ?? []);
    canvas.current = view;
    return () => { view.dispose(); canvas.current = null; document.title = 'Aarde'; };
  }, [info.authenticated]);

  useEffect(() => {
    if (!info.authenticated) return;
    const controller = new AbortController();
    loader.current?.dispose(); loader.current = null; canvas.current?.changed();
    setManifest(null); setError(''); setLoading(true);
    let ref: { catalog: string; image: string };
    try { ref = viewerReference(query); }
    catch (e) { setError((e as Error).message); setLoading(false); return () => controller.abort(); }
    async function load(layer: ViewerLayer, signal: AbortSignal): Promise<ViewerPixels> {
      const response = await fetch(layer.content_url, { signal, cache: 'no-store', headers: { Authorization: `Bearer ${token}` } });
      if (!response.ok) {
        if (response.status === 401) unauthorized();
        let message = `Layer request failed (${response.status}).`;
        try { message = (await response.json()).error?.message ?? message; } catch { /* Proxy response. */ }
        throw new Error(message);
      }
      if (layer.role === 'cloud_shapes') {
        const geometry = await response.json() as FeatureCollection<Polygon | MultiPolygon>;
        const data: ViewerPixels = { geometry, close() { data.geometry = undefined; } }; return data;
      }
      const bitmap = await createImageBitmap(await response.blob());
      if (bitmap.width !== layer.display_width || bitmap.height !== layer.display_height) { bitmap.close(); throw new Error('Display dimensions do not match the manifest. Reload the viewer.'); }
      const data: ViewerPixels = { bitmap, close() { data.bitmap?.close(); data.bitmap = undefined; } }; return data;
    }
    void request<ViewerManifest>(manifestURL(ref.catalog, ref.image, resolution), token, { signal: controller.signal }).then(m => {
      if (controller.signal.aborted) return;
      setManifest(m); setLoading(false);
      const manager = new ViewerLoader(m.layers, load, () => { canvas.current?.changed(); redraw(); });
      for (const [id, control] of settings.current) { const entry = manager.entries.get(id); if (entry) { entry.opacity = control.opacity; entry.visible = control.visible; } }
      loader.current = manager;
      if (!fitted.current) { canvas.current?.fit(m.extent); fitted.current = true; }
      manager.start();
    }).catch((e: unknown) => {
      if (controller.signal.aborted) return;
      if (e instanceof APIError && e.status === 401) unauthorized();
      setError(e instanceof Error ? e.message : 'Unable to open this image.'); setLoading(false);
    });
    return () => {
      controller.abort();
      if (loader.current) settings.current = new Map([...loader.current.entries.values()].map(e => [e.layer.id, { opacity: e.opacity, visible: e.visible }]));
      loader.current?.dispose(); loader.current = null;
    };
  }, [query, resolution, token, info.authenticated, unauthorized, attempt]);

  const entries = [...(loader.current?.entries.values() ?? [])];
  const loadable = entries.filter(entry => !entry.layer.unsupported);
  const loaded = loadable.filter(entry => entry.state === 'loaded').length;
  const failed = loadable.filter(entry => entry.state === 'error').length;
  const loadingLayers = loadable.some(entry => entry.state === 'queued' || entry.state === 'loading');
  const busy = info.authenticated && (loading || loadingLayers);
  return <div className="app image-viewer">
    <header className="header">
      <a className="brand" href="/"><img className="brand-mark" src="/icon.png" alt="" width={42} height={42} />aarde</a>
      <div className="header-actions"><Button href="/">Back to catalog</Button><ThemeControl />
        {info.authenticated ? <Button onClick={() => session.signOut()}>Sign out</Button> : <Button onClick={session.signIn}>Sign in</Button>}
      </div>
    </header>
    <section className="viewer-toolbar" aria-label="Image viewer toolbar">
      <div><h1>Image Viewer</h1><p>{manifest ? `${manifest.name || manifest.image_id} · ${manifest.catalog_id} · ${manifest.format}` : 'Whole-image segment viewer'}</p></div>
      <div className="viewer-tools"><Button onClick={() => canvas.current?.zoom(1)} disabled={!manifest} aria-label="Zoom in">＋</Button>
        <Button onClick={() => canvas.current?.zoom(-1)} disabled={!manifest} aria-label="Zoom out">−</Button>
        <Button onClick={() => manifest && canvas.current?.fit(manifest.extent)} disabled={!manifest}>Fit all</Button>
        <Button onClick={retry} disabled={loading || !info.authenticated}>Reload viewer</Button>
        <label>Display resolution <select aria-label="Display resolution" value={resolution} disabled={loading || !info.authenticated} onChange={e => setResolution(e.target.value)}>
          <option value="auto">Auto · bounded whole images</option><option value="preview">Preview · up to 1024 px</option>
          <option value="native" disabled={!manifest?.native_available}>Native dimensions{manifest && !manifest.native_available ? ' · exceeds budget' : ''}</option>
        </select></label>
      </div>
    </section>
    {!info.authenticated && <Alert severity="info">Source imagery requires authentication, including in public catalogs. <Button onClick={session.signIn}>Sign in to open Image Viewer</Button></Alert>}
    {error && <Alert severity="error">{error} <Button onClick={retry}>Reload viewer</Button></Alert>}
    <main className="viewer-workspace">
      <div className="viewer-stage"><div ref={target} className="viewer-canvas" tabIndex={0} aria-label="Image layers: drag to pan, wheel or pinch to zoom" aria-busy={busy} />
        {busy && <ViewerProgress label={loading ? 'Loading image manifest…' : 'Loading image layers…'}
          detail={loading ? undefined : `${loaded} of ${loadable.length} layers loaded${failed ? ` · ${failed} failed` : ''}`}
          value={loading ? undefined : (loaded + failed) / loadable.length * 100} />}
        <div className="viewer-hint">Drag to pan · wheel or pinch to zoom · zoom uses the loaded display pixels</div>
      </div>
      <aside className="viewer-panel" aria-label="Image layers">
        <h2>Layers {manifest && `(${manifest.layers.length})`}</h2>
        {manifest && <><p className="viewer-note">{manifest.coordinate_system}. Approx. {Math.ceil(manifest.estimated_memory_bytes / 1048576)} MiB display budget, including replacement overhead.</p>
          <p className="viewer-note">Cloud data: {manifest.cloud_status.replaceAll('_', ' ')}. Cloud opacity starts at 40%.</p>
          {manifest.ncdrd_assessment && <p className="viewer-note">NCDRD: {manifest.ncdrd_assessment.status}; conformance {manifest.ncdrd_assessment.conformance.replaceAll('_', ' ')}.</p>}
          {manifest.warnings.map(w => <Alert key={w} severity="warning">{w}</Alert>)}</>}
        {entries.map(entry => {
          const l = entry.layer, downsampled = l.width !== l.display_width || l.height !== l.display_height;
          return <section key={l.id} className="viewer-layer" data-layer-id={l.id}>
            <label className="viewer-layer-title"><input type="checkbox" checked={entry.visible} onChange={e => loader.current?.visibility(l.id, e.target.checked)} aria-label={`Show ${l.label}`} />{l.label}</label>
            <p className="viewer-note">{l.registration} · {entry.state}</p>
            {l.role !== 'cloud_shapes' && <p className="viewer-note">Original {l.width.toLocaleString()} × {l.height.toLocaleString()} · Display {l.display_width.toLocaleString()} × {l.display_height.toLocaleString()}<br />{downsampled ? 'Downsampled display — not native-resolution inspection' : 'Native display dimensions'}</p>}
            <label className="viewer-opacity">Opacity <input type="range" min="0" max="100" step="1" value={Math.round(entry.opacity * 100)} aria-label={`${l.label} opacity`} onChange={e => loader.current?.opacity(l.id, Number(e.target.value) / 100)} /><output>{Math.round(entry.opacity * 100)}%</output></label>
            {entry.error && <Alert severity="error">{entry.error}</Alert>}
            <div><Button size="small" disabled={!!l.unsupported} onClick={() => canvas.current?.fit(l.extent)}>Fit layer</Button>
              {entry.state === 'error' && !l.unsupported && <Button size="small" onClick={() => loader.current?.retry(l.id)}>Reload layer</Button>}</div>
            {l.legend && <ul className="viewer-legend">{Object.entries(l.legend).map(([value, label]) => <li key={value}>{value}: {label}</li>)}</ul>}
            <details><summary>Placement and rendering</summary><p>{l.placement}</p><p>{l.rendering}</p>{l.warnings.map(w => <p key={w}>{w}</p>)}</details>
          </section>;
        })}
      </aside>
    </main>
  </div>;
}

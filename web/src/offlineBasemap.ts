import GeoJSON from "ol/format/GeoJSON.js";
import VectorLayer from "ol/layer/Vector.js";
import VectorSource from "ol/source/Vector.js";
import { Circle, Fill, Stroke, Style, Text } from "ol/style.js";
import type { StyleFunction } from "ol/style/Style.js";

export function createOfflineBasemap() {
  return new VectorLayer({
    visible: false,
    declutter: true,
    source: new VectorSource({
      url: "/basemaps/natural-earth-110m.geojson",
      format: new GeoJSON(),
      attributions: 'Made with <a href="https://www.naturalearthdata.com/" target="_blank" rel="noopener noreferrer">Natural Earth</a>',
    }),
  });
}

export function offlineBasemapStyle(color: (name: string) => string): StyleFunction {
  const land = new Style({
    fill: new Fill({ color: color("--basemap-land") }),
    stroke: new Stroke({ color: color("--basemap-border"), width: 1 }),
  });
  const boundary = new Style({
    stroke: new Stroke({ color: color("--basemap-border"), width: 0.7, lineDash: [3, 3] }),
  });
  const labels = new Map<string, Style>();
  return (feature, resolution) => {
    const kind = feature.get("kind");
    if (kind === "land") return land;
    const zoom = Math.log2(156543.03392804097 / resolution);
    if (kind === "boundary") return zoom >= 3 ? boundary : undefined;
    const city = kind === "city";
    if (city ? zoom < Math.max(3, feature.get("minZoom")) : zoom < 2 || zoom > 5) return;
    const name = feature.get("name") as string;
    const key = `${kind}:${name}`;
    let label = labels.get(key);
    if (!label) {
      label = new Style({
        image: city ? new Circle({
          radius: 2.5,
          fill: new Fill({ color: color("--basemap-label") }),
          stroke: new Stroke({ color: color("--basemap-land"), width: 1 }),
        }) : undefined,
        text: new Text({
          text: name,
          font: city ? "11px sans-serif" : "12px sans-serif",
          offsetY: city ? -10 : 0,
          fill: new Fill({ color: color("--basemap-label") }),
          stroke: new Stroke({ color: color("--basemap-land"), width: 3 }),
        }),
      });
      labels.set(key, label);
    }
    return label;
  };
}

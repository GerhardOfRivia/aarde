import { forwardRef, useEffect, useImperativeHandle, useRef } from "react";
import Map from "ol/Map";
import View from "ol/View";
import Feature from "ol/Feature";
import GeoJSON from "ol/format/GeoJSON";
import TileLayer from "ol/layer/Tile";
import VectorLayer from "ol/layer/Vector";
import OSM from "ol/source/OSM";
import VectorSource from "ol/source/Vector";
import { Draw, Modify } from "ol/interaction";
import { Fill, Stroke, Style } from "ol/style";
import { fromLonLat } from "ol/proj";
import { defaults as controls, ScaleLine } from "ol/control";
import { useAppTheme } from "./theme";
import { createOfflineBasemap, offlineBasemapStyle } from "./offlineBasemap";
import type { Area, Basemap, Imagery } from "./types";
import "ol/ol.css";

export interface MapHandle {
  loadArea(area: Area): void;
  draw(): void;
  edit(): void;
  clear(): void;
  stop(): void;
  area(): Area | undefined;
  fit(): void;
}
interface Props {
  basemap: Basemap;
  items: Imagery[];
  selected: Imagery | null;
  onSelect(id: string): void;
  onArea(exists: boolean, editing?: boolean): void;
}
const format = new GeoJSON();
const projection = {
  dataProjection: "EPSG:4326",
  featureProjection: "EPSG:3857",
};
const style = (
  color: string,
  fill: string,
  width: number,
  lineDash?: number[],
) =>
  new Style({
    stroke: new Stroke({ color, width, lineDash }),
    fill: new Fill({ color: fill }),
  });

export const MapCanvas = forwardRef<MapHandle, Props>(
  function MapCanvas(props, ref) {
    const { mode } = useAppTheme();
    const container = useRef<HTMLDivElement>(null);
    const state = useRef<{
      map: Map;
      basemapLayer: TileLayer<OSM>;
      offlineLayer: ReturnType<typeof createOfflineBasemap>;
      aoi: VectorSource;
      results: VectorSource;
      selected: VectorSource;
      draw: Draw;
      modify: Modify;
      resultLayer: VectorLayer;
      aoiLayer: VectorLayer;
      selectedLayer: VectorLayer;
    } | null>(null);
    const callbacks = useRef(props);
    callbacks.current = props;
    useEffect(() => {
      const aoi = new VectorSource(),
        results = new VectorSource(),
        selected = new VectorSource();
      const resultLayer = new VectorLayer({
        source: results,
        style: style("#24776a", "#24776a24", 2),
      });
      const aoiLayer = new VectorLayer({ source: aoi });
      const selectedLayer = new VectorLayer({ source: selected });
      // Start without a source: offline mode must never briefly request OSM tiles.
      const basemapLayer = new TileLayer<OSM>({ className: "aarde-basemap" });
      const offlineLayer = createOfflineBasemap();
      const map = new Map({
        target: container.current!,
        controls: controls().extend([new ScaleLine()]),
        layers: [
          basemapLayer,
          offlineLayer,
          resultLayer,
          aoiLayer,
          selectedLayer,
        ],
        view: new View(callbacks.current.basemap === "offline"
          ? { center: fromLonLat([0, 20]), zoom: 2 }
          : { center: fromLonLat([-105.9, 39.48]), zoom: 8 }),
      });
      const draw = new Draw({ source: aoi, type: "Polygon" }),
        modify = new Modify({ source: aoi });
      draw.setActive(false);
      modify.setActive(false);
      map.addInteraction(draw);
      map.addInteraction(modify);
      draw.on("drawstart", () => {
        aoi.clear();
        callbacks.current.onArea(false, true);
      });
      draw.on("drawend", () => {
        draw.setActive(false);
        callbacks.current.onArea(true, false);
      });
      modify.on("modifyend", () => callbacks.current.onArea(true, true));
      map.on("singleclick", (event) => {
        if (draw.getActive() || modify.getActive()) return;
        const feature = map.forEachFeatureAtPixel(event.pixel, (f) => f, {
          layerFilter: (layer) => layer === resultLayer,
          hitTolerance: 5,
        });
        if (feature) callbacks.current.onSelect(String(feature.getId()));
      });
      map.on("pointermove", (event) => {
        map.getTargetElement().style.cursor = draw.getActive()
          ? "crosshair"
          : map.hasFeatureAtPixel(event.pixel, {
                layerFilter: (layer) => layer === resultLayer,
              })
            ? "pointer"
            : "";
      });
      state.current = {
        map,
        basemapLayer,
        offlineLayer,
        aoi,
        results,
        selected,
        draw,
        modify,
        resultLayer,
        aoiLayer,
        selectedLayer,
      };
      const resize = new ResizeObserver(() => map.updateSize());
      resize.observe(container.current!);
      return () => {
        resize.disconnect();
        map.dispose();
        state.current = null;
      };
    }, []);
    useEffect(() => {
      // Detach the source, rather than hiding its pixels. Keep vectors and view intact.
      state.current!.basemapLayer.setSource(props.basemap === "osm" ? new OSM() : null);
      state.current!.offlineLayer.setVisible(props.basemap === "offline");
    }, [props.basemap]);
    // Update only layer styles; theme changes preserve the view, AOI and selection.
    useEffect(() => {
      const s = state.current!;
      const colors = getComputedStyle(document.documentElement);
      const color = (name: string) => colors.getPropertyValue(name).trim();
      s.offlineLayer.setBackground(color("--basemap-water"));
      s.offlineLayer.setStyle(offlineBasemapStyle(color));
      s.resultLayer.setStyle(
        style(color("--footprint-stroke"), color("--footprint-fill"), 2),
      );
      s.aoiLayer.setStyle(
        style(color("--aoi-stroke"), color("--aoi-fill"), 2, [8, 5]),
      );
      s.selectedLayer.setStyle(
        style(color("--selected-stroke"), color("--selected-fill"), 3),
      );
    }, [mode]);
    useImperativeHandle(
      ref,
      () => ({
        loadArea(area) {
          const s = state.current;
          if (!s) throw new Error("The map is still loading. Try again in a moment.");
          // Parse before replacing the existing AOI so a failed load preserves it.
          const feature = new Feature(format.readGeometry(area, projection));
          s.draw.abortDrawing();
          s.draw.setActive(false);
          s.modify.setActive(false);
          s.aoi.clear();
          s.aoi.addFeature(feature);
          callbacks.current.onArea(true, false);
          s.map.getView().fit(feature.getGeometry()!.getExtent(), {
            padding: [60, 60, 60, 60],
            maxZoom: 16,
            duration: 400,
          });
        },
        draw() {
          const s = state.current!;
          s.modify.setActive(false);
          s.draw.setActive(true);
        },
        edit() {
          const s = state.current!;
          s.draw.abortDrawing();
          s.draw.setActive(false);
          s.modify.setActive(!s.modify.getActive());
          callbacks.current.onArea(
            s.aoi.getFeatures().length > 0,
            s.modify.getActive(),
          );
        },
        clear() {
          const s = state.current!;
          s.draw.abortDrawing();
          s.draw.setActive(false);
          s.modify.setActive(false);
          s.aoi.clear();
          callbacks.current.onArea(false, false);
        },
        stop() {
          const s = state.current!;
          s.draw.abortDrawing();
          s.draw.setActive(false);
          s.modify.setActive(false);
        },
        area() {
          const s = state.current!;
          const feature = s.aoi.getFeatures()[0];
          return feature
            ? (format.writeGeometryObject(
                feature.getGeometry()!,
                projection,
              ) as Area)
            : undefined;
        },
        fit() {
          const s = state.current!;
          if (s.results.getFeatures().length)
            s.map.getView().fit(s.results.getExtent()!, {
              padding: [60, 60, 60, 60],
              maxZoom: 16,
              duration: 400,
            });
        },
      }),
      [],
    );
    useEffect(() => {
      const s = state.current!;
      s.results.clear();
      s.results.addFeatures(
        props.items.map((item) => {
          const f = new Feature(
            format.readGeometry(item.footprint, projection),
          );
          f.setId(item.id);
          return f;
        }),
      );
      if (props.items.length)
        s.map.getView().fit(s.results.getExtent()!, {
          padding: [60, 60, 60, 60],
          maxZoom: 14,
          duration: 400,
        });
    }, [props.items]);
    useEffect(() => {
      const s = state.current!;
      s.selected.clear();
      if (props.selected) {
        const feature = new Feature(
          format.readGeometry(props.selected.footprint, projection),
        );
        s.selected.addFeature(feature);
        s.map.getView().fit(feature.getGeometry()!.getExtent(), {
          padding: [80, 80, 80, 80],
          maxZoom: 16,
          duration: 400,
        });
      }
    }, [props.selected]);
    return (
      <div
        className="map-canvas"
        ref={container}
        aria-label="Imagery map: draw or load a search area, or select an imagery footprint"
      />
    );
  },
);

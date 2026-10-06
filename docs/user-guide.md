# Catalog user guide

[Documentation index](README.md) · [Project README](../README.md)

Start with the [Docker quick start](../README.md#five-minute-workflow).
For token setup and public browsing, see [web access](configuration.md#web-access-and-read-only-viewing).

## Search the catalog

Select a catalog and set optional area, acquisition-date, or cloud filters, then click **Search catalog**. To draw an area, click **Draw Area**, click vertices on the map, and double-click to finish. Select a result or footprint to inspect metadata. **Edit Area** changes vertices; searches run only when explicitly requested. **Clear area** removes only the spatial condition. **Reset filters** clears metadata and catalog while preserving the drawn area and spatial scope. Exact image IDs are available under **More filters**.

Drawing, presets, resets, and chip removal change the draft filters. Applied chips,
rows, and footprints stay on the last successful search; pagination is disabled
while changes are pending. Exact IDs use OR between IDs and AND with the other
filter groups. Cloud percentage and acquisition date (UTC) appear on each result.
The page shows a range and whether more results are available, rather than an exact total.

### Dates and cloud cover

**Scene cloud cover** offers **Any cloud cover**, **At most**, and **Unknown only**. At most accepts decimal percentages from 0 to 100 and includes the selected boundary. **Include unknown cloud cover** broadens a maximum filter. Acquisition dates include whole UTC days; unknown dates are excluded while date bounds are set. Unapplied edits preserve the previous results and disable pagination. Each successful search starts on page one. Cloud cover describes the whole scene, not just the selected area.

The From and Through dates include both selected days in UTC. **At most 0%**
includes recorded zero, and **At most 100%** includes fully cloudy scenes.
The CLI's `--cloud-cover-lt` uses a strict threshold instead; see the
[CLI search reference](cli.md#search).

## Load and save search areas

To use an existing boundary, choose **Paste GeoJSON**, paste a WGS84 Polygon or MultiPolygon, and click **Load Area**. A polygon Feature or a FeatureCollection containing only polygon Features also works; multiple features become one MultiPolygon search area. The map fits the loaded area, which can be adjusted with **Edit Area**. Click **Search catalog** to apply the area and other filters. Loading replaces the current area and keeps existing results until you search. Invalid JSON, unsupported geometries, open rings, out-of-range coordinates, and oversized areas show an error without replacing the existing area. PostGIS checks polygon topology when the search runs.

Choose **Save GeoJSON** to download the current search area to your device as `aarde-search-area.geojson`. The file contains the drawn or loaded Polygon/MultiPolygon in WGS84 longitude/latitude coordinates, including holes and any edits, and can be reused with **Paste GeoJSON**. Saving is available once an area is loaded or drawing is finished; no search is required.

## Image Viewer

Choose **Open image viewer** on a catalog row, or use the adjacent new-tab link.
The viewer requires the current bearer token even when catalog browsing is public.
See the [Image Viewer guide](image-viewer.md) for layers, cloud overlays,
resolution controls, and registration limits.

## Appearance and basemaps

The header includes the running server's build version, matching `aarde version`. Use **Theme → Auto / Light / Night** to follow your system preference or choose a mode; the choice is remembered in your browser. Night uses Onderzeeer's charcoal-green palette and lime accents, with a dimmed basemap and distinct imagery/AOI colors.

The OpenStreetMap basemap requires internet access and sends tile requests to OpenStreetMap. Choose **Basemap → Offline map** for the bundled overview map, or **No basemap** for a plain background. Either choice removes the external tile source and stops new tile requests. The choice is remembered in this browser and applied before the map loads on later visits. Requests already sent before switching may finish. Footprints, selection, drawing/editing, pasted GeoJSON, and catalog searches work in all modes; switching basemaps preserves the current view and search area.

The offline basemap ships inside the binary/container: roughly 300 KB of [Natural Earth](https://www.naturalearthdata.com/) 1:110 million vector data, served by Aarde itself. It shows land, country boundaries and names, U.S. state boundaries, and major cities in Light and Night themes. Empty maps start at a world view; imagery and loaded search areas still zoom to their footprints. This is a generalized overview map without roads, street-level detail, or imagery. The data is public domain; source versions and regeneration instructions are in [`web/public/basemaps/NOTICE.txt`](../web/public/basemaps/NOTICE.txt). Normal builds use the checked-in data without fetching it from the internet.

A server configured with `AARDE_WEB_BASEMAP=offline` or `none` forces that mode
and disables the browser selector. See [offline deployments](configuration.md#offline-deployments).

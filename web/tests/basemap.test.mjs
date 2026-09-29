import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import GeoJSON from 'ol/format/GeoJSON.js'
import { createOfflineBasemap, offlineBasemapStyle } from '../src/offlineBasemap.ts'

const data = JSON.parse(readFileSync(new URL('../public/basemaps/natural-earth-110m.geojson', import.meta.url), 'utf8'))

test('bundled basemap has usable projected geography and labels', () => {
  const features = new GeoJSON().readFeatures(data, { featureProjection: 'EPSG:3857' })
  for (const kind of ['land', 'boundary', 'country', 'city']) {
    assert.ok(features.some((f) => f.get('kind') === kind), `missing ${kind}`)
  }
  for (const feature of features) {
    assert.ok(feature.getGeometry().getExtent().every(Number.isFinite))
  }
  assert.ok(features.some((f) => f.get('kind') === 'country' && f.get('name') === 'United States of America'))
  assert.ok(features.some((f) => f.get('kind') === 'city' && f.get('name') === 'Denver'))
  const style = offlineBasemapStyle(() => '#123456')
  const land = features.find((f) => f.get('kind') === 'land')
  const country = features.find((f) => f.get('kind') === 'country')
  const city = features.find((f) => f.get('kind') === 'city')
  assert.ok(style(land, 40000))
  assert.ok(style(country, 30000))
  assert.equal(style(country, 100), undefined)
  assert.equal(style(city, 80000), undefined)
  assert.ok(style(city, 100))
})

test('offline layer starts hidden and loads only a same-origin bundled resource', () => {
  const layer = createOfflineBasemap()
  assert.equal(layer.getVisible(), false)
  assert.equal(layer.getSource().getUrl(), '/basemaps/natural-earth-110m.geojson')
})

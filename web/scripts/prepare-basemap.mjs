// Optional maintainer step; normal builds use the checked-in data without downloads.
// See public/basemaps/NOTICE.txt for the pinned inputs and usage.
import { readFile, writeFile, mkdir } from 'node:fs/promises'
import { resolve } from 'node:path'

const input = process.argv[2]
if (!input) throw new Error('Usage: node scripts/prepare-basemap.mjs <input-directory>')
const read = async (name) => JSON.parse(await readFile(resolve(input, `${name}.geojson`), 'utf8')).features
const round = (coordinates) => coordinates.map((value) => Array.isArray(value) ? round(value) : Math.round(value * 10000) / 10000)
const feature = (geometry, properties) => ({ type: 'Feature', properties, geometry: { type: geometry.type, coordinates: round(geometry.coordinates) } })
const countries = await read('countries')
const states = await read('states')
const places = await read('places')
const features = [
  ...countries.map((f) => feature(f.geometry, { kind: 'land', name: f.properties.NAME_EN })),
  ...states.map((f) => feature(f.geometry, { kind: 'boundary' })),
  ...countries.map((f) => feature({ type: 'Point', coordinates: [f.properties.LABEL_X, f.properties.LABEL_Y] }, { kind: 'country', name: f.properties.NAME_EN })),
  ...places.sort((a, b) => b.properties.pop_max - a.properties.pop_max).map((f) => feature(f.geometry, { kind: 'city', name: f.properties.name, minZoom: f.properties.min_zoom })),
]
const output = new URL('../public/basemaps/', import.meta.url)
await mkdir(output, { recursive: true })
await writeFile(new URL('natural-earth-110m.geojson', output), JSON.stringify({ type: 'FeatureCollection', features }) + '\n')
console.log(`Prepared ${features.length} features`)

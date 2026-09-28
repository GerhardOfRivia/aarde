import assert from 'node:assert/strict'
import test from 'node:test'
import { swaggerOptions } from '../docs/options.js'

test('Swagger uses same-origin docs without external validation or persisted credentials', () => {
  assert.equal(swaggerOptions.url, '/openapi.json')
  assert.equal(swaggerOptions.validatorUrl, null)
  assert.equal(swaggerOptions.persistAuthorization, false)
  assert.equal(swaggerOptions.queryConfigEnabled, false)
  assert.deepEqual(swaggerOptions.supportedSubmitMethods, ['get', 'head', 'post'])
})

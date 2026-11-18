import { test } from 'node:test'
import assert from 'node:assert/strict'
import { bodyPreview, capturePreviewBadge, capturePreviewFlags, capturePreviewNote } from '../src/lib/traffic.ts'

test('HTTP bodies preserve UTF-8 text and render binary as hex', () => {
  assert.equal(bodyPreview(Buffer.from('中文<script>文本</script>').toString('base64')).text, '中文<script>文本</script>')
  const binary = bodyPreview(Buffer.from([0, 255, 128]).toString('base64'))
  assert.equal(binary.binary, true)
  assert.equal(binary.text, '00 ff 80')
})

test('a text preview boundary does not misclassify a split UTF-8 character', () => {
  const preview = bodyPreview(Buffer.from('中文测试').toString('base64'), 4)
  assert.equal(preview.text, '中')
  assert.equal(preview.truncated, true)
  assert.equal(preview.binary, false)
  assert.equal(preview.size, 12)
})

test('capture body flags label preview-only and truncated bytes', () => {
  const complete = capturePreviewFlags({ preview_only: false, truncated: false })
  assert.deepEqual(complete, { previewOnly: false, truncated: false, partial: false })
  assert.equal(capturePreviewBadge(complete), '')
  assert.equal(capturePreviewNote(complete), '')

  const preview = capturePreviewFlags({ preview_only: true, truncated: false })
  assert.equal(capturePreviewBadge(preview), '仅预览')
  assert.match(capturePreviewNote(preview), /4 KiB/)
  assert.match(capturePreviewNote(preview), /body_storage=disk/)

  const truncated = capturePreviewFlags({ preview_only: false, truncated: true })
  assert.equal(capturePreviewBadge(truncated), '已截断')
  assert.match(capturePreviewNote(truncated), /8 MiB/)

  const both = capturePreviewFlags({ preview_only: true, truncated: true })
  assert.equal(capturePreviewBadge(both), '仅预览 · 已截断')
  assert.match(capturePreviewNote(both), /仅预览/)
  assert.match(capturePreviewNote(both), /已截断/)
})

test('a legacy capture without flags is reconciled from its preserved error text', () => {
  // The server does exactly this derivation on read; an older index row must
  // not be presented as a complete body in the console either.
  const legacy = capturePreviewFlags({ error: 'response body truncated (10/20 bytes retained) (preview only; local storage disabled or unavailable)' })
  assert.equal(legacy.previewOnly, true)
  assert.equal(legacy.truncated, true)
  assert.equal(capturePreviewFlags({ error: 'connect timeout' }).partial, false)
})

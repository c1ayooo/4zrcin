// Deterministic display formatting (FA4): the console pins its locale/time zone
// so these expectations do not depend on the host's ICU locale or time zone.
// Every other module renders through these helpers, so this file is the
// compatibility contract that keeps the modern Intl implementation byte-for-byte
// equal to the old hand-rolled formatting.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { fmtBytes, fmtDuration, fmtInt, fmtTime, fmtUptime } from '../src/lib/format.ts'

test('fmtTime renders MM-DD HH:mm:ss and is deterministic for an explicit zone', () => {
  assert.equal(fmtTime('2026-11-01T02:03:04Z', 'UTC'), '11-01 02:03:04')
  assert.equal(fmtTime('2026-11-01T02:03:04+08:00', 'Asia/Shanghai'), '11-01 02:03:04')
  // The same instant in another zone moves, the wall clock does not drift.
  assert.equal(fmtTime('2026-11-01T02:03:04+08:00', 'UTC'), '10-31 18:03:04')
  // Midnight must render 00, never the "24:00" that hour12:false can produce.
  assert.equal(fmtTime('2026-11-01T00:00:00Z', 'UTC'), '11-01 00:00:00')
  assert.equal(fmtTime(''), '—')
  assert.equal(fmtTime('not-a-date'), 'not-a-date')
})

test('the default fmtTime path stays the operator local zone (compatibility)', () => {
  // This is the old behavior, stated as a contract: an explicit-offset ISO
  // timestamp renders as the same local wall clock the Date getters show.
  const iso = '2026-11-01T02:03:04Z'
  const local = new Date(iso)
  const pad = (n: number) => String(n).padStart(2, '0')
  assert.equal(
    fmtTime(iso),
    `${pad(local.getMonth() + 1)}-${pad(local.getDate())} ${pad(local.getHours())}:${pad(local.getMinutes())}:${pad(local.getSeconds())}`,
  )
})

test('fmtInt groups with a pinned en-US formatter, matching the previous toLocaleString', () => {
  assert.equal(fmtInt(1234567), '1,234,567')
  assert.equal(fmtInt(999), '999')
  assert.equal(fmtInt(0), '0')
  assert.equal(fmtInt(-1234), '-1,234')
  assert.equal(fmtInt(undefined), '0')
})

test('fmtDuration is deterministic when the caller injects the clock', () => {
  const from = '2026-11-01T00:00:00Z'
  const now = Date.parse('2026-11-01T00:00:05Z')
  assert.equal(fmtDuration(from, undefined, now), '5s')
  assert.equal(fmtDuration(from, '2026-11-01T00:01:05Z', now), '1m05s')
  assert.equal(fmtDuration(from, '2026-11-01T02:03:05Z', now), '2h03m')
  // A finished run ignores the injected clock; a bad row renders honestly.
  assert.equal(fmtDuration(from, '2026-11-01T00:00:05Z', 0), '5s')
  assert.equal(fmtDuration('', undefined, now), '—')
  assert.equal(fmtDuration('bad', undefined, now), '—')
})

test('fmtBytes keeps the console byte table (1024 divisor, one decimal below 10)', () => {
  // Byte units are not an Intl concept, so this stays hand-scaled: pin the
  // contract the tables already show instead of "modernizing" the output.
  assert.equal(fmtBytes(undefined), '—')
  assert.equal(fmtBytes(0), '—')
  assert.equal(fmtBytes(-1), '—')
  assert.equal(fmtBytes(512), '512B')
  assert.equal(fmtBytes(1024), '1.0K')
  assert.equal(fmtBytes(1536), '1.5K')
  assert.equal(fmtBytes(10240), '10K')
  assert.equal(fmtBytes(1024 * 1024), '1.0M')
  assert.equal(fmtBytes(1024 * 1024 * 1024 * 1024), '1.0T')
})

test('fmtUptime keeps the unattended chip contract', () => {
  assert.equal(fmtUptime(undefined), '—')
  assert.equal(fmtUptime(0), '—')
  assert.equal(fmtUptime(-10), '—')
  assert.equal(fmtUptime(59), '1m')
  assert.equal(fmtUptime(3600), '1h00m')
  assert.equal(fmtUptime(11520), '3h12m')
})

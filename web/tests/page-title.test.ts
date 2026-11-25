// The browser tab is how an operator tells projects apart, so the title rule is
// tested directly: app name first, only the named parts, no dangling separators.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { APP_NAME, pageTitle } from '../src/lib/page-title.ts'

test('pageTitle puts the app first and joins the named parts', () => {
  assert.equal(APP_NAME, '4zrcin')
  assert.equal(pageTitle(['客户 A', '图谱']), '4zrcin · 客户 A · 图谱')
  assert.equal(pageTitle(['态势']), '4zrcin · 态势')
})

test('pageTitle skips missing parts instead of leaving separators behind', () => {
  assert.equal(pageTitle([]), APP_NAME)
  assert.equal(pageTitle([undefined, null, '']), APP_NAME)
  assert.equal(pageTitle(['客户 A', undefined, '图谱']), '4zrcin · 客户 A · 图谱')
  assert.equal(pageTitle(['  ', '']), APP_NAME)
})

test('pageTitle trims each part so a stray space does not read as a segment', () => {
  assert.equal(pageTitle(['  客户 A  ', ' 图谱 ']), '4zrcin · 客户 A · 图谱')
})

import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  DEFAULT_PROJECT_CONSTRAINTS,
  projectDescriptionDraft,
} from '../src/lib/project-defaults.ts'

// The operator's rule (2026-11-21): the platform keeps mechanisms, not
// boundaries. The template may carry the one absolute data rule and an asset
// list; scope, methods, windows and stop lines are written by the operator in
// the brief. A built-in「不得 / 只能 / 停在某一步」would put every project back
// inside a scope nobody chose, so it is pinned out.
test('built-in text is one hard data rule plus an asset list', () => {
  assert.equal(DEFAULT_PROJECT_CONSTRAINTS.trim(), DEFAULT_PROJECT_CONSTRAINTS)
  assert.match(DEFAULT_PROJECT_CONSTRAINTS, /不要删除或修改真实数据/)
  assert.match(DEFAULT_PROJECT_CONSTRAINTS, /不改配置、不删文件、不锁账号/)
  assert.match(DEFAULT_PROJECT_CONSTRAINTS, /授权资产/)
  // An asset list with a blank to fill: the operator supplies the targets.
  assert.match(DEFAULT_PROJECT_CONSTRAINTS, /填写要测的资产/)
  // Three lines: the rule, the list header, one placeholder item.
  assert.equal(DEFAULT_PROJECT_CONSTRAINTS.split('\n').length, 4)
})

test('built-in text carries no penetration boundary of its own', () => {
  for (const banned of [
    /默认只读/,
    /受控的写入/,
    /只测试项目简报/,
    /不扫描无关网段/,
    /停在/,
    /拿 ?[Ss]hell/i,
    /不建会话/,
    /不落/,
    /归属不明|可能影响他人|由操作员确认后再继续/,
    /^4\./m,
  ]) {
    assert.doesNotMatch(DEFAULT_PROJECT_CONSTRAINTS, banned)
  }
  // Evidence/handoff contracts belong to the role prompts and keep applying when
  // this document is switched off; they must not ride along here.
  assert.doesNotMatch(DEFAULT_PROJECT_CONSTRAINTS, /原始证据|已验证结论|inferred|人工请求/)
})

test('a cancelled constraint document sends no description, never stale text', () => {
  assert.equal(projectDescriptionDraft(false, DEFAULT_PROJECT_CONSTRAINTS), '')
  assert.equal(projectDescriptionDraft(true, `  ${DEFAULT_PROJECT_CONSTRAINTS}  `), DEFAULT_PROJECT_CONSTRAINTS)
  assert.equal(projectDescriptionDraft(true, '   '), '')
  assert.equal(projectDescriptionDraft(true, ''), '')
})

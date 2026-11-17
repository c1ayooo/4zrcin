import { test } from 'node:test'
import assert from 'node:assert/strict'
import { mergeProjectSettings, nonNegativeInteger, projectSettingsDraft, projectSettingsPatch } from '../src/lib/settings.ts'
import type { ProjectInfo } from '../src/lib/types.ts'

const project = { id: 'p', max_runs: null, round_timeout: '', verify_auto: null } as ProjectInfo

test('refresh preserves unsaved fields while updating untouched settings', () => {
  const previous = projectSettingsDraft(project)
  const incoming = projectSettingsDraft({ ...project, scope: 'updated scope', max_runs: 8 })
  const draft = { ...previous, max_runs: '3', description: 'unsaved constraints' }
  assert.deepEqual(mergeProjectSettings(previous, incoming, draft), { ...draft, scope: 'updated scope' })
})

test('project save distinguishes inheritance from zero and only updates changed fields', () => {
  const previous = projectSettingsDraft({ ...project, max_runs: 4, verify_auto: true })
  assert.deepEqual(projectSettingsPatch(previous, { ...previous, max_runs: '', verify_auto: '' }), { max_runs: null, verify_auto: null })
  assert.deepEqual(projectSettingsPatch(previous, { ...previous, max_runs: '0', verify_auto: 'off' }), { max_runs: 0, verify_auto: false })
  assert.deepEqual(projectSettingsPatch(previous, { ...previous, scope: 'changed' }), { scope: 'changed' })
})

test('invalid numeric input cannot silently turn into an unlimited value', () => {
  for (const raw of ['-1', '1.5', '1e3', 'NaN', '9007199254740992']) assert.throws(() => nonNegativeInteger(raw, '并发'))
  assert.equal(nonNegativeInteger('', '并发'), 0)
  assert.equal(nonNegativeInteger(' 12 ', '并发'), 12)
})

// 链上事实的短标签：读一条事实不该先点开面板。
//
// 两个约束钉在这里：① 同前缀的事实不能读成同一个名字（夹具 5 条事实原本全截成
// 「示例：网关…」）；② 画布与 DOM 清单必须共用同一个函数，否则肉眼看到的和读屏
// 读到的会是两套截断规则。
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { FOCUS_FACT_LABEL_CHARS, focusFactLabel } from '../src/graph/render.ts'

test('the short label keeps the part after the class prefix', () => {
  assert.equal(focusFactLabel('示例：网关 OPTIONS 绕过鉴权'), '网关 OP…')
  assert.equal(focusFactLabel('示例：登录验证码可爆破'), '登录验证码…')
  // Two facts that share the prefix must still read differently.
  const a = focusFactLabel('示例：网关 OPTIONS 绕过鉴权')
  const b = focusFactLabel('示例：网关白名单直通下游 actuator')
  assert.notEqual(a, b)
})

test('a label without a separator is clipped from the start', () => {
  assert.equal(focusFactLabel('网关 OPTIONS 绕过鉴权'), '网关 OP…')
  assert.equal(focusFactLabel('短标题'), '短标题')
  assert.equal(focusFactLabel(''), '')
})

test('a dangling separator falls back to the whole label', () => {
  // Nothing useful after the colon: the prefix is all there is, so keep it.
  assert.equal(focusFactLabel('事实：'), '事实：')
  assert.equal(focusFactLabel('事实： a'), '事实： a')
})

test('the clip never exceeds the documented width', () => {
  for (const label of ['示例：网关 OPTIONS 绕过鉴权', '网关 OPTIONS 绕过鉴权', '短标题']) {
    assert.ok(focusFactLabel(label).length <= FOCUS_FACT_LABEL_CHARS, label)
  }
})

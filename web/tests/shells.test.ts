// The webshell ledger's presentation rules (docs/18): these are the strings the
// operator reads for a shell's standing and for each operation's result, so the
// wording and the failure lines are pinned here.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  LOCAL_NODE,
  decodeShellChunk,
  describeShellResult,
  encodeShellWrite,
  formatShellSize,
  outputPreview,
  protocolLabel,
  shellNodeLabel,
  shellStatusView,
} from '../src/lib/shells.ts'

test('every shell status maps onto the console tones', () => {
  const cases = {
    alive: { label: '存活', tone: 'success', dot: 'online' },
    dead: { label: '失联', tone: 'destructive', dot: 'error' },
    removed: { label: '已自删除', tone: 'muted', dot: 'idle' },
    residual: { label: '残留（无法自删除）', tone: 'warning', dot: 'warning' },
    remove_failed: { label: '删除失败', tone: 'destructive', dot: 'error' },
    unknown: { label: '未确认', tone: 'muted', dot: 'pending' },
    anything_else: { label: '未确认', tone: 'muted', dot: 'pending' },
  }
  for (const [status, want] of Object.entries(cases)) {
    assert.deepEqual(shellStatusView(status), want, status)
  }
})

test('protocol and node labels never leave a blank cell', () => {
  assert.equal(protocolLabel('v2'), 'v2 加密')
  assert.equal(protocolLabel('legacy'), 'legacy 明文')
  assert.equal(shellNodeLabel('0123456789abcdef'), '0123456789abcdef')
  assert.equal(shellNodeLabel(undefined), LOCAL_NODE)
  assert.equal(shellNodeLabel(''), LOCAL_NODE)
})

test('the list preview keeps the first line and cuts long ones', () => {
  assert.equal(outputPreview(undefined), '')
  // Leading whitespace is dropped, so the preview is the first real line.
  assert.equal(outputPreview('  \nsecond'), 'second')
  assert.equal(outputPreview('uid=0(root)\nfar more text\n'), 'uid=0(root)')
  const long = 'x'.repeat(300)
  const preview = outputPreview(long)
  assert.equal(preview.length, 121, 'one preview line is 120 characters plus the cut mark')
  assert.ok(preview.endsWith('…'))
})

test('sizes read in B, KB and MB', () => {
  assert.equal(formatShellSize(999), '999 B')
  assert.equal(formatShellSize(1536), '1.5 KB')
  assert.equal(formatShellSize(3 * 1024 * 1024), '3.0 MB')
})

test('a text chunk round-trips; binary content is measured, never printed', () => {
  const encoded = encodeShellWrite('hello 世界\n')
  const decoded = decodeShellChunk(encoded)
  assert.equal(decoded.binary, false)
  assert.equal(decoded.text, 'hello 世界\n')
  assert.equal(decoded.size, 13)
  // 0xff is not valid UTF-8.
  const binary = decodeShellChunk(btoa('\xff\x00\xfe'))
  assert.equal(binary.binary, true)
  assert.equal(binary.text, '')
  assert.equal(binary.size, 3)
})

const shell = {
  id: 'sh_1',
  name: 'shell.php',
  url: 'http://target.example/shell.php',
  type: 'php',
  protocol: 'v2',
  source: 'operator',
  status: 'alive',
  created_at: '2026-11-26T00:00:00Z',
  updated_at: '2026-11-26T00:00:00Z',
}

test('an exec result reads as its output, and an error rides along', () => {
  assert.equal(describeShellResult({ shell, result: { output: 'uid=0(root)\n' } }), 'uid=0(root)\n')
  assert.equal(
    describeShellResult({ shell, result: { output: 'partial' }, error: 'shell 返回 HTTP 500' }),
    'partial\n错误：shell 返回 HTTP 500',
  )
  assert.equal(describeShellResult({ shell, result: {}, error: '节点不在线' }), '错误：节点不在线')
  assert.equal(describeShellResult({ shell, result: {} }), '（无输出）')
})

test('a file listing renders one line per entry', () => {
  const text = describeShellResult({
    shell,
    result: {
      files: {
        path: '/var/www',
        parent: '/var',
        truncated: false,
        entries: [
          { name: 'index.php', type: 'file', size: 1536, modified: 0, mode: '-rw-r--r--' },
          { name: 'sub', type: 'dir', size: 0, modified: 0, mode: 'drwxr-xr-x' },
        ],
      },
    },
  })
  assert.equal(text, '目录 /var/www · 2 项\n- index.php  1.5 KB\nd sub/')
})

test('a ping, a write and a verified removal each say what they proved', () => {
  assert.equal(describeShellResult({ shell, result: { message: '命令回显正常' } }), '命令回显正常')
  assert.equal(
    describeShellResult({ shell, result: { written: { path: '/tmp/a', offset: 0, written: 4, size: 4 } } }),
    '已写入 /tmp/a：4 字节（文件共 4）',
  )
  assert.equal(describeShellResult({ shell, result: { removed: true, message: 'HTTP 404，协议请求已被拒绝' } }), 'HTTP 404，协议请求已被拒绝\n自删除已执行并验证消失')
})

test('an info result renders as the fields it learned', () => {
  const text = describeShellResult({ shell, result: { info: { os: 'linux', php: '8.2', cwd: '/var/www' } } })
  assert.equal(text, 'os: linux\nphp: 8.2\ncwd: /var/www')
})

// FA4: command search must not be locale-sensitive. `toLocaleLowerCase()` in a
// Turkish browser lowercases "I" to "ı", so the same query would match different
// commands on different machines. The protocol names are ASCII, so plain
// `toLowerCase()` is both correct and cross-platform deterministic.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { filterCommands } from '../src/lib/tools.ts'

test('command search is case-insensitive for names, descriptions and usage', () => {
  const commands = [
    { name: '!NMAP-Scan', description: 'Network MAPping', usage: '!NMAP-SCAN --target URL' },
    { name: '/skill:RECON', description: 'Recon' },
  ]
  assert.deepEqual(filterCommands(commands, 'native', ' nmap '), [commands[0]])
  assert.deepEqual(filterCommands(commands, 'native', 'URL'), [commands[0]])
  assert.deepEqual(filterCommands(commands, 'native', 'mapping'), [commands[0]])
  assert.deepEqual(filterCommands(commands, 'skill', 'recon'), [commands[1]])
  assert.deepEqual(filterCommands(commands, 'runtime', ''), [])
})

import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

// Layout guard for the 通信 sidebar. The participant list can be 40+ rows long;
// it used to push the 操作员指令 card below the fold of the only scroll area, so
// the card was practically unreachable. The fix gives the participants panel the
// remaining height and its own scroll, and pins backlog + the pending-fence card
// + instructions as a non-shrinking footer of the column. These assertions pin
// that split: if the cards ever move back into the scroll container, the fix is
// lost silently.
const source = await readFile(fileURLToPath(new URL('../src/views/Messages.tsx', import.meta.url)), 'utf8')

test('the participants panel owns the scroll instead of the whole column', () => {
  assert.match(source, /className="@3xl:flex @3xl:min-h-0 @3xl:flex-col"/)
  assert.match(source, /bodyClassName="p-1\.5 @3xl:min-h-0 @3xl:flex-1 @3xl:overflow-y-auto"/)
  assert.match(source, /@3xl:max-h-none @3xl:overflow-hidden/)
})

test('backlog, 待审资产 and 操作员指令 form one shrink-0 footer below the list', () => {
  assert.match(
    source,
    /<div className="flex min-h-0 shrink flex-col gap-3 overflow-y-auto overscroll-contain">\s*\n\s*<CommunicationBacklog[^\n]*\n\s*<FencePendingCard[^\n]*\n\s*<OperatorInstructionsCard \/>/,
  )
})

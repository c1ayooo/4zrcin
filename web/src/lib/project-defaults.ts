// Built-in project text the console proposes at creation time.
//
// Creating an engagement needs an explicit display name plus the operator's
// start and end: 起点 becomes the project brief, 终点 becomes the single endpoint
// goal. The directory id is a short random id decoupled from the name, so the
// console shows no derived-name preview.
//
// What the template may contain is deliberately narrow. The operator's rule
// (2026-11-21) is that the platform keeps mechanisms, not boundaries: every
// 「不得」「只能」「停在某一步」 belongs to the brief the operator writes, and a
// built-in document that pre-loads them puts the model back inside a scope the
// operator did not choose. So the template carries exactly two things — the one
// absolute data rule and an asset list to fill in. Scope, methods, windows and
// stop lines are the operator's words, not ours.
//
// The earlier three-clause document (scope limits, default read-only, hard
// limits) was removed for the same reason; its absence is intentional and must
// not be "restored" from git history. The data rule survives because deleting
// or modifying real data is the one thing no authorisation can undo.
//
// The text is canonical copy: tests pin it so a casual edit cannot quietly
// re-introduce a boundary the operator removed.
export const DEFAULT_PROJECT_CONSTRAINTS =
  '硬约束（唯一一条）：不要删除或修改真实数据——不改配置、不删文件、不锁账号。\n' +
  '\n授权资产：\n' +
  '- （在这里填写要测的资产，例如 10.0.0.0/24、example.com 或某个 host:port）'

/** The description a creation request should carry: a cancelled document
 *  (switch off) is sent as empty, never as stale text. */
export function projectDescriptionDraft(enabled: boolean, text: string): string {
  return enabled ? text.trim() : ''
}

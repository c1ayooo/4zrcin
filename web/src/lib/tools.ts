import type { RuntimeCommand } from './types.ts'

export type CommandKind = 'native' | 'skill' | 'runtime'

/** These prefixes are the harness command protocol, not a guessed tool list. */
export function commandKind(command: RuntimeCommand): CommandKind {
  if (command.name.startsWith('/skill:')) return 'skill'
  return command.name.startsWith('!') ? 'native' : 'runtime'
}

export function filterCommands(commands: RuntimeCommand[], kind: CommandKind, query: string) {
  // Plain `toLowerCase`, not `toLocaleLowerCase`: the command protocol is ASCII
  // and a locale-sensitive lowercase (e.g. Turkish, where "I" lowercases to "ı")
  // would make search results depend on the operator's browser locale.
  const text = query.trim().toLowerCase()
  return commands.filter(command => commandKind(command) === kind &&
    (!text || [command.name, command.description, command.usage].some(value => value?.toLowerCase().includes(text))))
}

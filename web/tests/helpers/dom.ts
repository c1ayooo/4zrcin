// Node-side browser globals that Radix primitives read directly.
//
// Radix Switch's form-association code (react-switch dist/index.mjs) references
// the `HTMLFormElement` global even when there is no DOM. react-test-renderer
// gives us no document, so rendering any settings page that contains a Switch
// throws `ReferenceError: HTMLFormElement is not defined` before the assertions
// run. The real browser always has it; this restores the same global for tests.
export function installFormElementShim(): void {
  const globals = globalThis as Record<string, unknown>
  if (!globals.HTMLFormElement) globals.HTMLFormElement = class HTMLFormElement {}
}

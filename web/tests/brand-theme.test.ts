// The brand wordmark used to be pinned to wordmark-white.svg in both themes:
// on the light theme a white wordmark on a light surface was (near-)invisible.
// The lockup must follow the theme: white on dark, black on light.
import { after, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { createTestServer } from './helpers/vite.ts'

let server: Awaited<ReturnType<typeof createTestServer>>
let Brand: any, ThemeProvider: any

before(async () => {
  server = await createTestServer({ root: fileURLToPath(new URL('..', import.meta.url)) })
  Brand = (await server.ssrLoadModule('/src/components/Brand.tsx')).Brand
  const themeSrc = fileURLToPath(new URL('../packages/theme/src', import.meta.url))
  ThemeProvider = (await server.ssrLoadModule(`/@fs${themeSrc}/ThemeProvider.tsx`)).ThemeProvider
})

after(async () => {
  await server.close()
})

function brandMarkup(theme: 'dark' | 'light'): string {
  return renderToStaticMarkup(
    React.createElement(ThemeProvider, { initial: theme, className: '' },
      React.createElement(Brand)),
  )
}

test('dark theme serves the white wordmark', () => {
  const html = brandMarkup('dark')
  assert.ok(html.includes('wordmark-white.svg'), `dark markup should use the white wordmark: ${html}`)
  assert.ok(!html.includes('wordmark-black.svg'))
})

test('light theme serves the black wordmark', () => {
  const html = brandMarkup('light')
  assert.ok(html.includes('wordmark-black.svg'), `light markup should use the black wordmark: ${html}`)
  assert.ok(!html.includes('wordmark-white.svg'))
})

test('the dead .tw-brand-cn CSS class is gone from the stylesheet', () => {
  const css = readFileSync(new URL('../src/app.css', import.meta.url), 'utf8')
  assert.ok(!css.includes('tw-brand-cn'), 'tw-brand-cn must not come back')
})

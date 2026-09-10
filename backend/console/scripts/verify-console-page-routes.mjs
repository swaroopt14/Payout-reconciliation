#!/usr/bin/env node
/**
 * Maps app pages that call /api/prod to expected BFF route files.
 * Run: node scripts/verify-console-page-routes.mjs
 */
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = path.join(path.dirname(fileURLToPath(import.meta.url)), '..')
const appDir = path.join(root, 'app')

/** grep-like: page path segment → required BFF suffix */
const PAGE_BFF_EXPECTATIONS = [
  ['overview/page.tsx', 'overview/route.ts'],
  ['transactions/page.tsx', 'intents/route.ts'],
  ['exceptions/page.tsx', 'exceptions/summary/route.ts'],
  ['payouts/page.tsx', 'intents/route.ts'],
]

function walk(dir, acc = []) {
  for (const name of fs.readdirSync(dir)) {
    const full = path.join(dir, name)
    if (fs.statSync(full).isDirectory()) walk(full, acc)
    else if (name === 'page.tsx') acc.push(full)
  }
  return acc
}

const pages = walk(appDir)
const prodCallers = pages.filter((p) => {
  const text = fs.readFileSync(p, 'utf8')
  return text.includes("'/api/prod") || text.includes('"/api/prod') || text.includes('`/api/prod')
})

let missing = 0
for (const [pageSuffix, routeSuffix] of PAGE_BFF_EXPECTATIONS) {
  const pagePath = path.join(appDir, pageSuffix)
  const routePath = path.join(root, 'app/api/prod', routeSuffix)
  if (!fs.existsSync(pagePath)) {
    console.error(`MISSING PAGE: app/${pageSuffix}`)
    missing += 1
  }
  if (!fs.existsSync(routePath)) {
    console.error(`MISSING BFF for ${pageSuffix}: app/api/prod/${routeSuffix}`)
    missing += 1
  }
}

console.log(`Pages with /api/prod fetch calls: ${prodCallers.length}`)
console.log(`Checked ${PAGE_BFF_EXPECTATIONS.length} page→route expectations`)

if (missing > 0) {
  console.error(`\n${missing} expected route(s) missing.`)
  process.exit(1)
}
console.log('\nConsole page→BFF expectations OK.')

// Minimal Node ESM loader for console tests — no new deps (uses the existing `typescript` devDependency).
// - resolves `@/…` to the console root, extensionless relative imports to .ts/.tsx/index.ts
// - maps bare `next/server` to `next/server.js`
// - transpiles .ts/.tsx to ESM
// - instruments services/analytics/store.ts so tests can prove it was (not) loaded
import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import ts from 'typescript'

const CONSOLE_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..')
const EXTS = ['.ts', '.tsx', '/index.ts', '/index.tsx']

function tryFile(base) {
  if (existsSync(base) && !base.endsWith('/')) {
    try { if (readFileSync(base) && /\.(m?js|cjs|json|tsx?)$/.test(base)) return base } catch {}
  }
  for (const ext of EXTS) {
    const p = base + ext
    if (existsSync(p)) return p
  }
  return null
}

export async function resolve(specifier, context, nextResolve) {
  if (specifier === 'next/server') return nextResolve('next/server.js', context)
  let base = null
  if (specifier.startsWith('@/')) base = path.join(CONSOLE_ROOT, specifier.slice(2))
  else if ((specifier.startsWith('./') || specifier.startsWith('../')) && context.parentURL?.startsWith('file:')) {
    base = path.resolve(path.dirname(fileURLToPath(context.parentURL)), specifier)
  }
  if (base) {
    const hit = tryFile(base)
    if (hit) return { url: pathToFileURL(hit).href, shortCircuit: true }
  }
  return nextResolve(specifier, context)
}

const STORE_FILE = path.join(CONSOLE_ROOT, 'services', 'analytics', 'store.ts')

export async function load(url, context, nextLoad) {
  if (url.startsWith('file:') && /\.tsx?$/.test(url)) {
    const file = fileURLToPath(url)
    let source = readFileSync(file, 'utf8')
    const out = ts.transpileModule(source, {
      fileName: file,
      compilerOptions: {
        module: ts.ModuleKind.ESNext,
        target: ts.ScriptTarget.ES2022,
        jsx: ts.JsxEmit.ReactJSX,
        esModuleInterop: true,
      },
    })
    let code = out.outputText
    if (file === STORE_FILE) {
      code += '\nglobalThis.__clearlineAnalyticsStoreLoads = (globalThis.__clearlineAnalyticsStoreLoads || 0) + 1\n'
    }
    return { format: 'module', source: code, shortCircuit: true }
  }
  return nextLoad(url, context)
}

/** @type {import('next').NextConfig} */
const path = require('path')

const nextConfig = {
  reactStrictMode: true,
  output: 'standalone',
  eslint: {
    ignoreDuringBuilds: false,
  },
  typescript: {
    ignoreBuildErrors: false,
  },
  // Server fetch cache: opt out per-request via `cache: 'no-store'` on fetches and
  // `Cache-Control` on Route Handlers - there is no global "disable all fetch cache" flag here.
  experimental: {},
  // Auth-guarded HTML must not be stored by shared CDNs (stale shell / wrong session after deploy).
  async headers() {
    const privateHtml = [
      '/sandbox',
      '/sandbox/:path*',
      '/payout-command-view',
      '/payout-command-view/:path*',
      '/admin/:path*',
    ]
    const cacheHeaders = [
      { key: 'Cache-Control', value: 'private, no-cache, no-store, max-age=0, must-revalidate' },
      { key: 'Vary', value: 'Cookie' },
    ]
    return privateHtml.map((source) => ({ source, headers: cacheHeaders }))
  },
  // Mutate resolve.alias in place - replacing the whole object can drop Next.js
  // internal aliases and cause "Cannot find the middleware module" at runtime.
  webpack: (config) => {
    const alias = config.resolve.alias ?? {}
    config.resolve.alias = alias
    alias['@/constants'] = path.resolve(__dirname, 'constants')
    alias['@/components'] = path.resolve(__dirname, 'components')
    alias['@/features'] = path.resolve(__dirname, 'src/features')
    alias['@/shared'] = path.resolve(__dirname, 'src/shared')
    alias['@/server'] = path.resolve(__dirname, 'src/server')
    alias['@/styles'] = path.resolve(__dirname, 'src/styles')
    alias['@/lib'] = path.resolve(__dirname, 'src/shared/lib')
    alias['@/types'] = path.resolve(__dirname, 'types')
    alias['@/utils'] = path.resolve(__dirname, 'utils')
    alias['@/services'] = path.resolve(__dirname, 'services')
    alias['@/config'] = path.resolve(__dirname, 'config')
    return config
  },
}

module.exports = nextConfig

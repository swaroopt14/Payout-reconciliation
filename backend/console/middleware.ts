import { NextRequest, NextResponse } from 'next/server'

function isWorkspaceAdminPath(pathname: string) {
  return pathname === '/admin' || pathname === '/admin/'
}

/** India + Cross-border console surfaces — same cookie/role gate. */
function isCustomerConsolePath(pathname: string) {
  return (
    pathname.startsWith('/payout-command-view') ||
    pathname.startsWith('/sandbox') ||
    pathname.startsWith('/overview') ||
    pathname.startsWith('/connections') ||
    pathname.startsWith('/controls') ||
    pathname.startsWith('/payouts') ||
    pathname.startsWith('/contracts') ||
    pathname.startsWith('/execution') ||
    pathname.startsWith('/payments') ||
    pathname.startsWith('/settlement') ||
    pathname.startsWith('/proof') ||
    pathname.startsWith('/developer') ||
    pathname.startsWith('/ask') ||
    pathname.startsWith('/actions') ||
    pathname.startsWith('/agents') ||
    pathname.startsWith('/exceptions') ||
    pathname.startsWith('/reconciliation') ||
    pathname.startsWith('/cash-position') ||
    pathname.startsWith('/investigations') ||
    pathname.startsWith('/evaluation') ||
    pathname.startsWith('/transactions') ||
    pathname.startsWith('/build') ||
    isWorkspaceAdminPath(pathname)
  )
}

function loginRedirectUrl(request: NextRequest) {
  return new URL('/signin', request.url)
}

function roleMatchesPath(pathname: string, role: string) {
  if (isCustomerConsolePath(pathname)) {
    return role === 'CUSTOMER_USER' || role === 'CUSTOMER_ADMIN'
  }
  return true
}

export function middleware(request: NextRequest) {
  const pathname = request.nextUrl.pathname

  if (pathname === '/signin/tenant' || (pathname === '/signin' && request.nextUrl.search)) {
    return NextResponse.redirect(new URL('/signin', request.url))
  }

  if (pathname === '/signin' || pathname === '/signup') {
    return NextResponse.next()
  }

  const hasAccessToken = Boolean(request.cookies.get('zord_access_token')?.value)
  const hasRefreshToken = Boolean(request.cookies.get('zord_refresh_token')?.value)
  if (!hasAccessToken && !hasRefreshToken) {
    return NextResponse.redirect(loginRedirectUrl(request))
  }

  const role = request.cookies.get('zord_role')?.value
  if (role && !roleMatchesPath(pathname, role)) {
    return NextResponse.redirect(loginRedirectUrl(request))
  }

  return NextResponse.next()
}

export const config = {
  matcher: [
    '/signin',
    '/signin/tenant',
    '/admin',
    '/admin/:path*',
    '/payout-command-view',
    '/payout-command-view/:path*',
    '/sandbox',
    '/sandbox/:path*',
    '/overview',
    '/overview/:path*',
    '/connections',
    '/connections/:path*',
    '/controls',
    '/controls/:path*',
    '/payouts',
    '/payouts/:path*',
    '/contracts',
    '/contracts/:path*',
    '/execution',
    '/execution/:path*',
    '/payments',
    '/payments/:path*',
    '/settlement',
    '/settlement/:path*',
    '/proof',
    '/proof/:path*',
    '/developer',
    '/developer/:path*',
    '/ask',
    '/ask/:path*',
    '/actions',
    '/actions/:path*',
    '/agents',
    '/agents/:path*',
    '/exceptions',
    '/exceptions/:path*',
    '/reconciliation',
    '/reconciliation/:path*',
    '/cash-position',
    '/cash-position/:path*',
    '/investigations',
    '/investigations/:path*',
    '/evaluation',
    '/evaluation/:path*',
    '/transactions',
    '/transactions/:path*',
    '/build',
    '/build/:path*',
  ],
}

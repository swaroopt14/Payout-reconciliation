'use client'

import { useEffect } from 'react'
import { usePathname, useRouter } from 'next/navigation'
import { clearAuth, getCurrentUser, hasSessionHint, hydrateSession } from '@/services/auth'
import { UserRole } from '@/types/auth'

function isWorkspaceAdminPath(pathname: string) {
  return pathname === '/admin' || pathname === '/admin/'
}

function isProtectedPath(pathname: string) {
  return (
    pathname.startsWith('/admin') ||
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
    pathname.startsWith('/build')
  )
}

function isLoginPath(pathname: string) {
  return pathname === '/signin' || pathname === '/signup' || pathname === '/register'
}

function roleMatchesPath(pathname: string, role: UserRole) {
  if (pathname.startsWith('/payout-command-view') || pathname.startsWith('/sandbox') || isWorkspaceAdminPath(pathname)) {
    return role === 'CUSTOMER_USER' || role === 'CUSTOMER_ADMIN'
  }
  return true
}

export function AuthSessionBootstrap() {
  const pathname = usePathname()
  const router = useRouter()

  useEffect(() => {
    if (!pathname || isLoginPath(pathname) || !isProtectedPath(pathname)) {
      return
    }

    let cancelled = false

    void hydrateSession()
      .then((user) => {
        if (cancelled) return

        if (!user) {
          if (!hasSessionHint() && !getCurrentUser()) {
            router.replace('/signin')
          }
          return
        }

        if (!roleMatchesPath(pathname, user.role)) {
          clearAuth()
          router.replace('/signin')
        }
      })
      .catch(() => {
        /* hydrateSession is defensive; swallow stray rejections */
      })

    return () => {
      cancelled = true
    }
  }, [pathname, router])

  return null
}

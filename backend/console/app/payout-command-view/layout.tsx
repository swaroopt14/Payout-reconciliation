'use client'

import { useEffect } from 'react'
import { usePathname, useRouter } from 'next/navigation'

/**
  * Payout Command View is session-scoped: unauthenticated or tenant-less users
  * are redirected to sign-in.
  */
export default function PayoutCommandViewLayout({ children }: { children: React.ReactNode }) {
  const router = useRouter()
  const pathname = usePathname()

  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        const res = await fetch('/api/auth/me', { credentials: 'include' })
        if (cancelled) return
        if (!res.ok) {
          router.replace('/signin')
          return
        }
        const data = (await res.json().catch(() => null)) as {
          user?: { tenant_id?: string }
          session?: { tenant_id?: string }
        } | null
        const tid = data?.session?.tenant_id?.trim() || data?.user?.tenant_id?.trim()
        if (!tid) {
          router.replace('/signin')
        }
      } catch {
        if (cancelled) return
        router.replace('/signin')
      }
    })()
    return () => {
      cancelled = true
    }
  }, [router, pathname])

  return <>{children}</>
}

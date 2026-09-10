'use client'

import { Suspense, useEffect, type ReactNode } from 'react'
import { useSearchParams } from 'next/navigation'
import { persistEnvMode } from '@/services/auth/persistEnvMode'
import { enterDemoSession, isDemoQuery } from '@/services/payout-command/demo/ycDemoConstants'
import { WorkspaceAdminPage } from '@/features/payout-command/workspace-admin/WorkspaceAdminPage'

/**
 * Spec 7.18 - Team, Access, Audit, and Support (`/admin`).
 * API credentials live in `/developer`.
 */
function AdminBootstrap({ children }: { children: ReactNode }) {
  const params = useSearchParams()
  const demo = isDemoQuery(params.get('demo'))

  useEffect(() => {
    persistEnvMode('sandbox')
    if (demo) enterDemoSession()
  }, [demo])

  return <>{children}</>
}

export default function WorkspaceAdminRoutePage() {
  return (
    <Suspense
      fallback={
        <div className="flex min-h-screen items-center justify-center bg-[#F8FAFC] text-[13px] text-[#64748B]">
          Loading workspace administration…
        </div>
      }
    >
      <AdminBootstrap>
        <WorkspaceAdminPage />
      </AdminBootstrap>
    </Suspense>
  )
}

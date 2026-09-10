'use client'

import { useState, useEffect } from 'react'
import { useRouter } from 'next/navigation'
import { getCurrentUser, isAuthenticated, getCurrentRole, hydrateSession, subscribeToAuthChanges } from '@/services/auth'
import { User, UserRole } from '@/types/auth'

export function useAuth() {
  const router = useRouter()
  const [user, setUser] = useState<User | null>(null)
  const [role, setRole] = useState<UserRole | null>(null)
  const [isLoading, setIsLoading] = useState(true)

  useEffect(() => {
    let cancelled = false

    const syncSession = async () => {
      const currentUser = getCurrentUser()
      const currentRole = getCurrentRole()
      setUser(currentUser)
      setRole(currentRole)

      const hydratedUser = await hydrateSession()
      if (cancelled) return

      setUser(hydratedUser)
      setRole(hydratedUser?.role ?? null)
      setIsLoading(false)
    }

    void syncSession()

    const unsubscribe = subscribeToAuthChanges(() => {
      const currentUser = getCurrentUser()
      setUser(currentUser)
      setRole(getCurrentRole())
    })

    return () => {
      cancelled = true
      unsubscribe()
    }
  }, [])

  const checkAuth = () => {
    if (!isAuthenticated()) {
      router.push('/signin')
      return false
    }
    return true
  }

  return {
    user,
    role,
    isLoading,
    isAuthenticated: isAuthenticated(),
    checkAuth,
  }
}

import React, { createContext, useContext, useEffect, useState, useCallback } from 'react'
import { api2, Me } from './api'

interface SessionCtx {
  user: Me | null
  loading: boolean
  error: string | null
  login: (email: string, password: string) => Promise<void>
  signup: (email: string, password: string) => Promise<void>
  logout: () => Promise<void>
  refresh: () => Promise<void>
}

const Ctx = createContext<SessionCtx | undefined>(undefined)

export function SessionProvider({ children }: { children: React.ReactNode }) {
  const [user, setUser] = useState<Me | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try { const u = await api2.me(); setUser(u) } catch { setUser(null) } finally { setLoading(false) }
  }, [])

  useEffect(() => { load() }, [load])

  const login = useCallback(async (email: string, password: string) => {
    setError(null)
    try { await api2.login(email, password); await load() } catch (e:any) { setError(e.message); throw e }
  }, [load])

  const signup = useCallback(async (email: string, password: string) => {
    setError(null)
    try { await api2.signup(email, password); await login(email, password) } catch (e:any) { setError(e.message); throw e }
  }, [login])

  const logout = useCallback(async () => { setUser(null) }, []) // server currently has no logout endpoint

  const refresh = load

  return <Ctx.Provider value={{ user, loading, error, login, signup, logout, refresh }}>{children}</Ctx.Provider>
}

export function useSession() {
  const ctx = useContext(Ctx)
  if (!ctx) throw new Error('useSession outside provider')
  return ctx
}


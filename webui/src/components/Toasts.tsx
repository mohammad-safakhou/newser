import React, { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'

export type ToastLevel = 'info' | 'success' | 'error' | 'warn'
export interface Toast { id: string; level: ToastLevel; title?: string; message: string; timeout?: number }

interface ToastCtx {
  push: (t: Omit<Toast, 'id'>) => string
  success: (message: string, title?: string) => void
  error: (message: string, title?: string) => void
  info: (message: string, title?: string) => void
  warn: (message: string, title?: string) => void
  remove: (id: string) => void
}

const Ctx = createContext<ToastCtx | null>(null)

const levelStyles: Record<ToastLevel, string> = {
  info: 'bg-slate-800 border-slate-700 text-slate-200',
  success: 'bg-emerald-600/90 border-emerald-400/40 text-emerald-50',
  error: 'bg-red-600/90 border-red-400/40 text-red-50',
  warn: 'bg-amber-600/90 border-amber-400/40 text-amber-50'
}

export function ToastProvider({ children }: { children: React.ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([])
  const timers = useRef<Record<string, number>>({})

  const remove = useCallback((id: string) => {
    setToasts(t => t.filter(x => x.id !== id))
    if (timers.current[id]) { window.clearTimeout(timers.current[id]); delete timers.current[id] }
  }, [])

  const push = useCallback((t: Omit<Toast, 'id'>) => {
    const id = Math.random().toString(36).slice(2)
    const timeout = t.timeout ?? 4000
    setToasts(prev => [...prev, { ...t, id }])
    if (timeout > 0) {
      timers.current[id] = window.setTimeout(() => remove(id), timeout)
    }
    return id
  }, [remove])

  const api: ToastCtx = {
    push,
    success: (m, title) => push({ level: 'success', message: m, title }),
    error: (m, title) => push({ level: 'error', message: m, title }),
    info: (m, title) => push({ level: 'info', message: m, title }),
    warn: (m, title) => push({ level: 'warn', message: m, title }),
    remove,
  }

  useEffect(()=>()=>{ Object.values(timers.current).forEach(id => window.clearTimeout(id)) }, [])

  return (
    <Ctx.Provider value={api}>
      {children}
      {createPortal(
        <div className="fixed inset-0 pointer-events-none flex flex-col-reverse items-end gap-2 p-4 z-[100]">
          {toasts.map(t => (
            <div key={t.id} className={`pointer-events-auto w-full max-w-sm border rounded-lg shadow-sm px-4 py-3 fade-in ${levelStyles[t.level]}`}>
              <div className="flex items-start gap-3">
                <div className="flex-1 min-w-0">
                  {t.title && <div className="text-xs font-semibold tracking-wide uppercase opacity-80 mb-0.5">{t.title}</div>}
                  <div className="text-sm leading-snug break-words">{t.message}</div>
                </div>
                <button onClick={()=>remove(t.id)} className="text-xs text-slate-300/70 hover:text-white ml-2">×</button>
              </div>
            </div>
          ))}
        </div>,
        document.body
      )}
    </Ctx.Provider>
  )
}

export function useToast() {
  const ctx = useContext(Ctx)
  if (!ctx) throw new Error('useToast must be inside ToastProvider')
  return ctx
}


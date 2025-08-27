import React, { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useSession } from '../session'

export default function AuthPage() {
  const { login, signup, error } = useSession()
  const nav = useNavigate()
  const [mode, setMode] = useState<'login'|'signup'>('login')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [localError, setLocalError] = useState<string| null>(null)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!email || !password) { setLocalError('Email and password required'); return }
    setBusy(true); setLocalError(null)
    try {
      if (mode==='login') {
        await login(email, password)
      } else {
        await signup(email, password)
      }
      nav('/topics')
    } catch (e:any) { setLocalError(e.message) } finally { setBusy(false) }
  }

  return (
    <div className="min-h-full flex items-center justify-center p-6">
      <div className="w-full max-w-md space-y-6">
        <div className="text-center space-y-1">
          <h1 className="text-2xl font-semibold tracking-tight">Newser Console</h1>
          <p className="text-sm text-slate-400">Intelligent topic orchestration</p>
        </div>
        {(error || localError) && <div className="text-sm bg-red-600/20 border border-red-500/40 text-red-200 px-3 py-2 rounded">{localError || error}</div>}
        <form onSubmit={submit} className="space-y-4">
          <div className="space-y-2">
            <input autoFocus type="email" placeholder="Email" className="input" value={email} onChange={e=>setEmail(e.target.value)} disabled={busy} />
            <input type="password" placeholder="Password" className="input" value={password} onChange={e=>setPassword(e.target.value)} disabled={busy} />
          </div>
          <button type="submit" disabled={busy} className="btn w-full justify-center">{busy ? '...' : mode==='login' ? 'Sign In' : 'Create Account'}</button>
        </form>
        <div className="text-center text-xs text-slate-500">
          {mode==='login' ? (
            <button type="button" onClick={()=>setMode('signup')} className="hover:text-slate-300">Need an account? Sign up</button>
          ) : (
            <button type="button" onClick={()=>setMode('login')} className="hover:text-slate-300">Have an account? Sign in</button>
          )}
        </div>
      </div>
    </div>
  )
}

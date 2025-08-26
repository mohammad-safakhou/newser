import React, { useEffect, useMemo, useState } from 'react'
import { api, Topic } from './api'
import TopicPage from './TopicPage'

function useSession() {
  const [loading, setLoading] = useState(true)
  const [userId, setUserId] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        setLoading(true)
        setError(null)
        const me = await api.me()
        if (!cancelled) setUserId(me.user_id)
      } catch {
        if (!cancelled) setUserId(null)
      } finally {
        if (!cancelled) setLoading(false)
      }
    })()
    return () => { cancelled = true }
  }, [])

  return { loading, userId, setUserId, error, setError }
}

export default function App() {
  const { loading, userId, setUserId, error, setError } = useSession()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [topics, setTopics] = useState<Topic[]>([])
  const [newTopic, setNewTopic] = useState('')
  const [busy, setBusy] = useState(false)
  const [activeTopic, setActiveTopic] = useState<Topic | null>(null)

  const authenticated = useMemo(() => !!userId, [userId])

  useEffect(() => {
    if (!authenticated) return
    let cancelled = false
    ;(async () => {
      try {
        setBusy(true)
        const list = await api.topics()
        if (!cancelled) setTopics(list)
      } catch (e: any) {
        if (!cancelled) setError(e.message || 'Failed to load topics')
      } finally {
        if (!cancelled) setBusy(false)
      }
    })()
    return () => { cancelled = true }
  }, [authenticated, setError])

  const onLogin = async (ev: React.FormEvent) => {
    ev.preventDefault()
    try {
      setBusy(true)
      setError(null)
      await api.login(email, password)
      const me = await api.me()
      setUserId(me.user_id)
    } catch (e: any) {
      setError(e.message || 'Login failed')
    } finally {
      setBusy(false)
    }
  }

  const onSignup = async (ev: React.FormEvent) => {
    ev.preventDefault()
    try {
      setBusy(true)
      setError(null)
      await api.signup(email, password)
      await onLogin(ev)
    } catch (e: any) {
      setError(e.message || 'Signup failed')
      setBusy(false)
    }
  }

  const onCreateTopic = async (ev: React.FormEvent) => {
    ev.preventDefault()
    if (!newTopic.trim()) return
    try {
      setBusy(true)
      setError(null)
      await api.createTopic(newTopic.trim())
      setNewTopic('')
      const list = await api.topics()
      setTopics(list)
    } catch (e: any) {
      setError(e.message || 'Failed to create topic')
    } finally {
      setBusy(false)
    }
  }

  const onTrigger = async (id: string) => {
    try {
      setBusy(true)
      setError(null)
      await api.trigger(id)
      alert('Run triggered')
    } catch (e: any) {
      setError(e.message || 'Failed to trigger run')
    } finally {
      setBusy(false)
    }
  }

  if (loading) {
    return <div style={{ maxWidth: 480, margin: '80px auto', fontFamily: 'system-ui' }}>Loading…</div>
  }

  if (!authenticated) {
    return (
      <div style={{ maxWidth: 420, margin: '80px auto', fontFamily: 'system-ui' }}>
        <h1 style={{ marginBottom: 8 }}>Newser</h1>
        <p style={{ color: '#666', marginTop: 0 }}>Sign in to manage your topics.</p>
        {error && <div style={{ background: '#ffe5e5', color: '#900', padding: 8, borderRadius: 6, marginBottom: 10 }}>{error}</div>}
        <form onSubmit={onLogin} style={{ display: 'grid', gap: 10 }}>
          <input placeholder='Email' value={email} onChange={e=>setEmail(e.target.value)} disabled={busy} style={{ padding: 10, borderRadius: 6, border: '1px solid #ccc' }} />
          <input placeholder='Password' type='password' value={password} onChange={e=>setPassword(e.target.value)} disabled={busy} style={{ padding: 10, borderRadius: 6, border: '1px solid #ccc' }} />
          <div>
            <button type='submit' disabled={busy} style={{ padding: '8px 12px' }}>Login</button>
            <button type='button' onClick={onSignup} disabled={busy} style={{ padding: '8px 12px', marginLeft: 8 }}>Sign up</button>
          </div>
        </form>
      </div>
    )
  }

  if (activeTopic) {
    return (
      <div style={{ maxWidth: 1000, margin: '40px auto', fontFamily: 'system-ui' }}>
        <button onClick={()=>setActiveTopic(null)} style={{ marginBottom: 12 }}>&larr; Back</button>
        <TopicPage id={activeTopic.ID} name={activeTopic.Name} />
      </div>
    )
  }

  return (
    <div style={{ maxWidth: 900, margin: '40px auto', fontFamily: 'system-ui' }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <h2 style={{ margin: 0 }}>Topics</h2>
        {busy && <span style={{ color: '#666' }}>Working…</span>}
      </div>
      {error && <div style={{ background: '#ffe5e5', color: '#900', padding: 8, borderRadius: 6, marginTop: 10 }}>{error}</div>}
      <form onSubmit={onCreateTopic} style={{ display: 'flex', gap: 8, marginTop: 16 }}>
        <input placeholder='New topic name' value={newTopic} onChange={e=>setNewTopic(e.target.value)} disabled={busy} style={{ flex: 1, padding: 10, borderRadius: 6, border: '1px solid #ccc' }} />
        <button type='submit' disabled={busy || !newTopic.trim()} style={{ padding: '8px 12px' }}>Create</button>
        <button type='button' onClick={async()=>{ setBusy(true); try { const list = await api.topics(); setTopics(list) } finally { setBusy(false) } }} disabled={busy} style={{ padding: '8px 12px' }}>Refresh</button>
      </form>
      <ul style={{ listStyle: 'none', padding: 0, marginTop: 16 }}>
        {topics.map(t => (
          <li key={t.ID} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: 12, border: '1px solid #eee', borderRadius: 8, marginBottom: 8 }}>
            <div style={{ cursor: 'pointer' }} onClick={()=>setActiveTopic(t)}>
              <div style={{ fontWeight: 600 }}>{t.Name}</div>
              <div style={{ color: '#666', fontSize: 12 }}>Cron: {t.ScheduleCron || '—'}</div>
            </div>
            <div>
              <button onClick={()=>onTrigger(t.ID)} disabled={busy} style={{ padding: '6px 10px' }}>Trigger</button>
            </div>
          </li>
        ))}
        {topics.length === 0 && <li style={{ color: '#666' }}>No topics yet. Create one above.</li>}
      </ul>
    </div>
  )
}



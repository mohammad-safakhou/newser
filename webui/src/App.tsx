import React, { useEffect, useState } from 'react'

async function api(path: string, opts: RequestInit = {}) {
  const res = await fetch(path, { credentials: 'include', headers: { 'Content-Type': 'application/json' }, ...opts })
  if (!res.ok) throw new Error(await res.text())
  return res.headers.get('content-type')?.includes('application/json') ? res.json() : null
}

export default function App() {
  const [userId, setUserId] = useState<string | null>(null)
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [topics, setTopics] = useState<any[]>([])
  const [newTopic, setNewTopic] = useState('')

  useEffect(() => {
    api('/api/me').then(data => setUserId(data.user_id)).catch(() => {})
  }, [])

  const login = async (ev: React.FormEvent) => {
    ev.preventDefault()
    await api('/api/auth/login', { method: 'POST', body: JSON.stringify({ email, password }) })
    const me = await api('/api/me')
    setUserId(me.user_id)
    loadTopics()
  }

  const signup = async (ev: React.FormEvent) => {
    ev.preventDefault()
    await api('/api/auth/signup', { method: 'POST', body: JSON.stringify({ email, password }) })
    await login(ev)
  }

  const loadTopics = async () => {
    const list = await api('/api/topics')
    setTopics(list)
  }

  const createTopic = async (ev: React.FormEvent) => {
    ev.preventDefault()
    await api('/api/topics', { method: 'POST', body: JSON.stringify({ name: newTopic, preferences: { bias_detection: true }, schedule_cron: '@daily' }) })
    setNewTopic('')
    loadTopics()
  }

  const trigger = async (id: string) => {
    await api(`/api/topics/${id}/trigger`, { method: 'POST' })
    alert('Run triggered')
  }

  if (!userId) {
    return (
      <div style={{ maxWidth: 400, margin: '40px auto', fontFamily: 'system-ui' }}>
        <h2>Newser - Login</h2>
        <form onSubmit={login}>
          <input placeholder='email' value={email} onChange={e=>setEmail(e.target.value)} style={{ width: '100%', marginBottom: 8 }} />
          <input placeholder='password' type='password' value={password} onChange={e=>setPassword(e.target.value)} style={{ width: '100%', marginBottom: 8 }} />
          <button type='submit'>Login</button>
          <button type='button' onClick={signup} style={{ marginLeft: 8 }}>Sign up</button>
        </form>
      </div>
    )
  }

  return (
    <div style={{ maxWidth: 800, margin: '40px auto', fontFamily: 'system-ui' }}>
      <h2>Topics</h2>
      <form onSubmit={createTopic}>
        <input placeholder='new topic name' value={newTopic} onChange={e=>setNewTopic(e.target.value)} />
        <button type='submit'>Create</button>
        <button type='button' onClick={loadTopics} style={{ marginLeft: 8 }}>Refresh</button>
      </form>
      <ul>
        {topics.map(t => (
          <li key={t.ID}>
            <b>{t.Name}</b> <small>({t.ScheduleCron})</small>
            <button style={{ marginLeft: 8 }} onClick={()=>trigger(t.ID)}>Trigger</button>
          </li>
        ))}
      </ul>
    </div>
  )
}



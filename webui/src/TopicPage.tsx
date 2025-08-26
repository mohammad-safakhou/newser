import React, { useEffect, useState } from 'react'
import { api, Run } from './api'

export default function TopicPage({ id, name }: { id: string, name: string }) {
  const [runs, setRuns] = useState<Run[]>([])
  const [latest, setLatest] = useState<any | null>(null)
  const [message, setMessage] = useState('')
  const [reply, setReply] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        setBusy(true)
        setError(null)
        const [rs, lr] = await Promise.all([api.runs(id), api.latestResult(id).catch(()=>null)])
        if (!cancelled) {
          setRuns(rs)
          setLatest(lr)
        }
      } catch (e: any) {
        if (!cancelled) setError(e.message || 'Failed to load activity')
      } finally {
        if (!cancelled) setBusy(false)
      }
    })()
    return () => { cancelled = true }
  }, [id])

  const send = async (ev: React.FormEvent) => {
    ev.preventDefault()
    if (!message.trim()) return
    try {
      setBusy(true)
      setError(null)
      const resp = await api.chat(id, message.trim())
      setReply(resp.message)
      setMessage('')
    } catch (e: any) {
      setError(e.message || 'Chat failed')
    } finally {
      setBusy(false)
    }
  }

  const trigger = async () => {
    try {
      setBusy(true)
      await api.trigger(id)
      const rs = await api.runs(id)
      setRuns(rs)
      alert('Run triggered')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
      <div style={{ border: '1px solid #eee', borderRadius: 8, padding: 12 }}>
        <h3 style={{ marginTop: 0 }}>Conversation - {name}</h3>
        {error && <div style={{ background: '#ffe5e5', color: '#900', padding: 8, borderRadius: 6, marginBottom: 10 }}>{error}</div>}
        <form onSubmit={send} style={{ display: 'flex', gap: 8 }}>
          <input placeholder='Ask or refine this topic...' value={message} onChange={e=>setMessage(e.target.value)} disabled={busy} style={{ flex: 1, padding: 10, borderRadius: 6, border: '1px solid #ccc' }} />
          <button type='submit' disabled={busy || !message.trim()} style={{ padding: '8px 12px' }}>Send</button>
        </form>
        {reply && (
          <div style={{ whiteSpace: 'pre-wrap', marginTop: 12 }}>{reply}</div>
        )}
      </div>
      <div style={{ border: '1px solid #eee', borderRadius: 8, padding: 12 }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
          <h3 style={{ marginTop: 0 }}>Activity</h3>
          <button onClick={trigger} disabled={busy} style={{ padding: '6px 10px' }}>Trigger Run</button>
        </div>
        <div>
          <h4 style={{ marginBottom: 4 }}>Latest Result</h4>
          {!latest && <div style={{ color: '#666' }}>No result yet.</div>}
          {latest && (
            <div style={{ fontSize: 14 }}>
              <div><b>Summary:</b> {latest.summary || '—'}</div>
              <div style={{ marginTop: 6 }}><b>Highlights:</b> {Array.isArray(latest.highlights) ? latest.highlights.length : 0}</div>
              <div style={{ marginTop: 6 }}><b>Confidence:</b> {latest.confidence ?? '—'}</div>
            </div>
          )}
        </div>
        <div style={{ marginTop: 12 }}>
          <h4 style={{ marginBottom: 4 }}>Runs</h4>
          <ul style={{ listStyle: 'none', padding: 0 }}>
            {runs.map(r => (
              <li key={r.ID} style={{ padding: 8, border: '1px solid #f0f0f0', borderRadius: 6, marginBottom: 6 }}>
                <div><b>{r.Status}</b> · <small>{new Date(r.StartedAt).toLocaleString()}</small></div>
                {r.Error && <div style={{ color: '#900' }}>{r.Error}</div>}
              </li>
            ))}
            {runs.length === 0 && <li style={{ color: '#666' }}>No runs yet.</li>}
          </ul>
        </div>
      </div>
    </div>
  )
}




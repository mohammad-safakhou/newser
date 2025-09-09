import React, { useEffect, useState, useCallback, useRef } from 'react'
import { useParams, Link } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import PreferenceDiff, { DiffEntry } from '../components/PreferenceDiff'
import { api2, Run, formatDate, ChatMessage } from '../api'
import { useToast } from '../components/Toasts'
import ConfidenceGauge from '../components/ConfidenceGauge'
import HighlightsList from '../components/HighlightsList'
import KnowledgeGraph from '../components/KnowledgeGraph'
import MarkdownView from '../components/MarkdownView'
import Spinner from '../components/Spinner'
import { useDialogA11y } from '../components/useDialogA11y'
import { useSession } from '../session'

function RunDetailModal({ topicId, runId, onClose }: { topicId: string; runId: string; onClose: () => void }) {
  const [data, setData] = useState<any | null>(null)
  const [error, setError] = useState<string | null>(null)
  // expansion of individual highlights removed per UX request; use markdown only
  const [md, setMd] = useState<string | null>(null)
  const [mdOpen, setMdOpen] = useState(false)
  const [mdLoading, setMdLoading] = useState(false)
  const containerRef = useRef<HTMLDivElement | null>(null)
  useDialogA11y(containerRef, onClose, '#run-detail-close')
  useEffect(()=>{
    let mounted = true
    api2.runResult(topicId, runId).then(d => { if (mounted) setData(d) }).catch(e => { if (mounted) setError(e.message) })
    // auto-load the primary markdown report on open
    api2.runMarkdown(topicId, runId).then(text => { if (mounted) { setMd(text); setMdOpen(true) } }).catch(()=>{})
    return ()=>{ mounted = false }
  }, [topicId, runId])
  // per-run expand kept via Expand All button to merge into markdown
  const loadMarkdown = async () => {
    try {
      setMdLoading(true)
      const text = await api2.runMarkdown(topicId, runId)
      setMd(text)
      setMdOpen(true)
    } catch (e:any) {
      setMd(`Error: ${e.message || e}`)
      setMdOpen(true)
    }
    finally { setMdLoading(false) }
  }
  const expandAll = async () => {
    try {
      setMdLoading(true)
      const resp = await api2.expandAll(topicId, runId, { group_by: 'type' })
      setMd(resp.markdown)
      setMdOpen(true)
    } catch (e:any) {
      setMd(`Error: ${e.message || e}`)
      setMdOpen(true)
    } finally { setMdLoading(false) }
  }
  return (
    <div className="fixed inset-0 z-40 flex items-center justify-center p-4" role="dialog" aria-modal="true" aria-labelledby="run-detail-title">
      <div className="absolute inset-0 bg-slate-950/80 backdrop-blur" onClick={onClose} aria-hidden="true" />
      <div ref={containerRef} className="relative w-full max-w-5xl card max-h-[90vh] overflow-y-auto focus:outline-none" tabIndex={-1}>
          <div className="flex items-center justify-between border-b border-slate-800 px-5 py-3">
            <h3 id="run-detail-title" className="text-sm font-semibold">Run Details</h3>
            <div className="flex items-center gap-2">
              <button className="btn-secondary text-xs px-3" onClick={loadMarkdown} disabled={mdLoading}>{mdLoading ? 'Loading…' : 'View Markdown'}</button>
              <button className="btn-secondary text-xs px-3" onClick={expandAll} disabled={mdLoading}>{mdLoading ? 'Generating…' : 'Expand All'}</button>
              <button id="run-detail-close" className="btn-secondary text-xs px-3" onClick={onClose}>Close</button>
            </div>
          </div>
        {!data && !error && <div className="p-4"><Spinner label="Loading run details" /></div>}
        {error && <div className="p-4 text-xs text-red-400">{error}</div>}
        {data && (
          <div className="p-5 space-y-5">
            {mdOpen && (
              <div className="bg-slate-900/60 border border-slate-800 rounded p-3 max-h-[70vh] overflow-auto">
                {mdLoading ? <Spinner label="Generating" /> : (md ? <MarkdownView markdown={md} /> : <div className="text-xs text-slate-500">—</div>)}
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  )
}

export default function TopicDetailPage() {
  const { id } = useParams<{ id: string }>()
  const qc = useQueryClient()
  const [conversation, setConversation] = useState<ChatMessage[]>([])
  const [input, setInput] = useState('')
  const [optimisticReply, setOptimisticReply] = useState(false)
  const [showDetail, setShowDetail] = useState(false)
  const [openRunId, setOpenRunId] = useState<string | null>(null)
  const bottomRef = useRef<HTMLDivElement | null>(null)
  const toast = useToast()

    const runsQ = useQuery({
      queryKey: ['runs', id],
      queryFn: () => api2.runs(id!),
      enabled: !!id,
      refetchInterval: (q) => {
        const data = q.state.data as Run[] | undefined
        if (!data) return 0
        return data.some((r: Run) => r.Status === 'running') ? 4000 : 0
      }
    })
  const latestQ = useQuery({ queryKey: ['latest', id], queryFn: () => api2.latestResult(id!), enabled: !!id, staleTime: 10_000 })
  const topicQ = useQuery({ queryKey: ['topic', id], queryFn: () => api2.getTopic(id!), enabled: !!id })
  const topicName = qc.getQueryData<any>(['topics'])?.find?.((t: any)=>t.ID===id)?.Name
  const [editingName, setEditingName] = useState(false)
  const [newName, setNewName] = useState<string>('')
  const renameMut = useMutation({ mutationFn: (name: string) => api2.updateTopicName(id!, name), onSuccess: ()=>{ qc.invalidateQueries({ queryKey: ['topics'] }); setEditingName(false); toast.success('Topic renamed') }, onError: (e:any)=> toast.error(e.message || 'Rename failed') })

  const triggerMut = useMutation({ mutationFn: () => api2.triggerRun(id!), onSuccess: () => { qc.invalidateQueries({ queryKey: ['runs', id] }); toast.success('Run triggered') }, onError: (e:any) => toast.error(e.message || 'Trigger failed') })
  const chatMut = useMutation({
    mutationFn: (msg: string) => api2.chat(id!, msg),
    onSuccess: async (resp, msg) => {
      // Normalize assistant message if backend returned raw JSON
      const clean = normalizeAssistantMessage(resp?.message)
      setConversation(prev => [...prev, { role: 'user', content: msg }, { role: 'assistant', content: clean }])
      setOptimisticReply(false)
      // Try to compute diffs using response; fallback to refetching topic
      try {
        const updated = resp?.topic as any
        const serverPrefs = (updated && (updated.preferences || updated.Preferences)) || null
        let newPrefs: Record<string, any> = serverPrefs || {}
        if (!serverPrefs) {
          // If response didn't include prefs (LLM parse quirks), refetch topic
          const fresh = await qc.fetchQuery({ queryKey: ['topic', id], queryFn: () => api2.getTopic(id!) }) as any
          newPrefs = (fresh && (fresh.preferences || fresh.Preferences)) || {}
        }
        const base = lastPrefs ?? (topicQ.data?.preferences || {})
        const d = computeDiff(base, newPrefs)
        setDiffs(d)
        setLastPrefs(newPrefs)
        if (d.length > 0) toast.success('Preferences updated')
        else toast.info('No preference changes')
      } catch (e) {
        toast.warn('Assistant replied; preferences unchanged')
      }
    },
    onError: (e:any) => { setOptimisticReply(false); toast.error((e && e.message) || 'Chat failed') }
  })

  function normalizeAssistantMessage(text: string): string {
    if (!text) return ''
    let s = String(text).trim()
    // Strip code fences e.g. ```json ... ```
    const fence = /^```[a-zA-Z]*\n([\s\S]*?)\n```$/m.exec(s)
    if (fence && fence[1]) s = fence[1].trim()
    // Try parse JSON and extract message/content
    try {
      const obj = JSON.parse(s)
      if (typeof obj?.message === 'string') return obj.message
      if (typeof obj?.content === 'string') return obj.content
      if (Array.isArray(obj?.messages)) {
        const last = obj.messages[obj.messages.length-1]
        if (typeof last?.content === 'string') return last.content
      }
    } catch {}
    return s
  }

  const [lastPrefs, setLastPrefs] = useState<Record<string, any> | null>(null)
  const [diffs, setDiffs] = useState<DiffEntry[]>([])

  useEffect(()=>{
    if (topicQ.data && !lastPrefs) {
      setLastPrefs(topicQ.data.preferences || {})
    }
  }, [topicQ.data, lastPrefs])

  const send = useCallback(() => {
    const msg = input.trim(); if (!msg) return
    setConversation(prev => [...prev, { role: 'user', content: msg }])
    setInput('')
    setOptimisticReply(true)
    chatMut.mutate(msg)
  }, [input, chatMut])

  useEffect(() => { bottomRef.current?.scrollIntoView({ behavior: 'smooth' }) }, [conversation, optimisticReply])

  return (
    <div className="h-full flex flex-col">
      <header className="px-6 py-3 border-b border-slate-800 flex items-center gap-3">
        <Link to="/topics" className="text-xs text-slate-400 hover:text-brand-400">← Topics</Link>
        {!editingName ? (
          <>
            <h2 className="text-lg font-semibold flex-1 truncate">{topicName || 'Topic'}</h2>
            <button className="btn-secondary text-xs px-2" onClick={()=>{ setEditingName(true); setNewName(topicName || '') }}>Rename</button>
          </>
        ) : (
          <div className="flex items-center gap-2 flex-1">
            <input className="input text-sm flex-1" value={newName} onChange={e=>setNewName(e.target.value)} />
            <button className="btn text-xs px-3" disabled={!newName.trim() || renameMut.isPending} onClick={()=>renameMut.mutate(newName.trim())}>{renameMut.isPending ? 'Saving…' : 'Save'}</button>
            <button className="btn-secondary text-xs px-3" onClick={()=>setEditingName(false)}>Cancel</button>
          </div>
        )}
        <div className="flex items-center gap-2">
          <button className="btn-secondary text-xs px-3" disabled={triggerMut.isPending} onClick={()=>triggerMut.mutate()}>{triggerMut.isPending ? 'Triggering…' : 'Trigger Run'}</button>
          <span className="mx-1 h-4 w-px bg-slate-700" aria-hidden />
          <LogoutButton />
        </div>
      </header>
      <div className="flex-1 grid lg:grid-cols-2 gap-0 overflow-hidden">
        <section className="flex flex-col border-r border-slate-800 min-h-0">
          <div className="px-5 py-3 border-b border-slate-800 flex items-center justify-between">
            <h3 className="text-sm font-medium tracking-wide text-slate-300">Conversation</h3>
            {chatMut.isPending && <span className="text-xs text-slate-500 animate-pulse">thinking…</span>}
          </div>
          <div className="flex-1 overflow-y-auto px-4 py-4 space-y-3 text-sm" aria-live="polite">
            {conversation.length === 0 && <div className="text-slate-500 text-xs">Start refining this topic by asking for scope, preferences, or scheduling help.</div>}
            {conversation.map((m, i) => (
              <div key={i} className={m.role === 'user' ? 'flex justify-end' : 'flex justify-start'}>
                <div className={(m.role==='user' ? 'bg-brand-600 text-white' : 'bg-slate-800 text-slate-100') + ' max-w-[80%] rounded-lg px-3 py-2 whitespace-pre-wrap leading-relaxed text-[13px]'}>{m.content}</div>
              </div>
            ))}
            {optimisticReply && <div className="flex justify-start"><div className="bg-slate-800 rounded-lg px-3 py-2 text-[13px] text-slate-400" role="status" aria-live="polite"><span className="inline-flex gap-1"><span className="w-1.5 h-1.5 rounded-full bg-slate-500 animate-pulse"/><span className="w-1.5 h-1.5 rounded-full bg-slate-600 animate-pulse delay-150"/><span className="w-1.5 h-1.5 rounded-full bg-slate-700 animate-pulse delay-300"/></span></div></div>}
            <div ref={bottomRef} />
          </div>
          <form onSubmit={e=>{e.preventDefault(); send()}} className="p-3 flex gap-2 border-t border-slate-800">
            <label htmlFor="topic-chat" className="sr-only">Topic message</label>
            <input id="topic-chat" className="input text-xs" placeholder="Ask or refine topic…" value={input} onChange={e=>setInput(e.target.value)} disabled={chatMut.isPending} />
            <button type="submit" className="btn text-xs px-4" disabled={!input.trim() || chatMut.isPending}>Send</button>
          </form>
        </section>
        <section className="flex flex-col min-h-0">
          <div className="px-5 py-3 border-b border-slate-800 flex items-center justify-between">
            <h3 className="text-sm font-medium tracking-wide text-slate-300">Activity</h3>
            <button onClick={()=>latestQ.refetch()} className="text-xs text-slate-400 hover:text-slate-200">Refresh</button>
          </div>
          <div className="flex-1 overflow-y-auto p-5 space-y-6">
            <div className="card space-y-2">
              <h4 className="text-xs font-semibold uppercase tracking-wider text-slate-400">Latest Result</h4>
              {latestQ.isLoading && <Spinner label="Loading latest result" />}
              {latestQ.isError && <div className="text-xs text-red-400">No recent result.</div>}
              {latestQ.data && (
                <div className="flex items-center gap-4 text-xs">
                  <div className="text-slate-400">Confidence:</div>
                  <ConfidenceGauge value={typeof latestQ.data.confidence === 'number' ? latestQ.data.confidence : null} />
                  <button className="btn-secondary text-xs ml-auto" onClick={()=>{
                    // open details for latest run
                    const runs = (runsQ.data || []) as Run[]
                    if (runs.length > 0) {
                      const latest = runs.reduce((a, b) => (new Date(a.StartedAt) > new Date(b.StartedAt) ? a : b))
                      setOpenRunId(latest.ID)
                    }
                  }}>View Details</button>
                </div>
              )}
            </div>
            <div className="card space-y-3">
              <h4 className="text-xs font-semibold uppercase tracking-wider text-slate-400">Runs</h4>
              {runsQ.isLoading && <Spinner label="Loading runs" />}
              {runsQ.data && runsQ.data.length === 0 && <div className="text-xs text-slate-500">No runs yet.</div>}
              <ul className="space-y-2 text-xs">
                {runsQ.data?.map(r => <RunRow key={r.ID} run={r} />)}
              </ul>
            </div>
            <div className="card space-y-2">
              <h4 className="text-xs font-semibold uppercase tracking-wider text-slate-400">Preference Changes</h4>
              {!topicQ.isSuccess && <div className="text-xs text-slate-500">{topicQ.isLoading ? 'Loading topic…' : 'No data'}</div>}
              {topicQ.isSuccess && <PreferenceDiff diffs={diffs} />}
            </div>
          </div>
        </section>
      </div>
      {openRunId && <RunDetailModal topicId={id!} runId={openRunId} onClose={()=>setOpenRunId(null)} />}
    </div>
  )
}

function LogoutButton() {
  const { logout } = useSession()
  return (
    <button onClick={async()=>{ await logout(); window.location.assign('/login') }} className="text-xs text-slate-400 hover:text-slate-200">Logout</button>
  )
}

function RunRow({ run }: { run: Run }) {
  const color = run.Status === 'succeeded' ? 'bg-emerald-500/20 text-emerald-300 border-emerald-500/30' : run.Status === 'failed' ? 'bg-red-500/20 text-red-300 border-red-500/30' : 'bg-slate-600/20 text-slate-300 border-slate-500/30'
  const [open, setOpen] = useState(false)
  const topicId = (useParams() as any).id as string
  return (
    <li className="flex flex-col gap-2 p-2 rounded border border-slate-800 bg-slate-900/40">
      <div className="flex items-start gap-3">
        <div className={`px-2 py-0.5 rounded-full text-xs font-medium border ${color}`}>{run.Status}</div>
        <div className="flex-1 leading-tight">
          <div className="text-slate-300">{formatDate(run.StartedAt)}</div>
          {run.Error && <div className="text-xs text-red-400 mt-1">{run.Error}</div>}
        </div>
        <button className="btn-secondary text-xs px-2 py-1" onClick={()=>setOpen(true)}>Details</button>
      </div>
      {open && topicId && <RunDetailModal topicId={topicId} runId={run.ID} onClose={()=>setOpen(false)} />}
    </li>
  )
}

// simple diff utility
function computeDiff(oldObj: Record<string, any>, newObj: Record<string, any>): DiffEntry[] {
  const diffs: DiffEntry[] = []
  const visited = new Set<string>()
  const walk = (prefix: string, a: any, b: any) => {
    if (typeof a !== 'object' || a === null || typeof b !== 'object' || b === null) {
      if (JSON.stringify(a) !== JSON.stringify(b)) {
        if (a === undefined) diffs.push({ path: prefix, type: 'added', to: b })
        else if (b === undefined) diffs.push({ path: prefix, type: 'removed', from: a })
        else diffs.push({ path: prefix, type: 'changed', from: a, to: b })
      }
      return
    }
    const keys = new Set([...Object.keys(a||{}), ...Object.keys(b||{})])
    keys.forEach(k => walk(prefix ? `${prefix}.${k}` : k, a?.[k], b?.[k]))
  }
  walk('', oldObj || {}, newObj || {})
  diffs.sort((x,y)=> x.path.localeCompare(y.path))
  return diffs
}

import React, { useEffect, useState, useCallback, useRef } from 'react'
import { useParams, Link } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
// Preference diff removed in favor of always-visible Preferences panel
import { api2, Run, formatDate, ChatMessage, TemporalPolicy, TopicDetail, BudgetConfig, PendingApproval } from '../api'
import { useToast } from '../components/Toasts'
import ConfidenceGauge from '../components/ConfidenceGauge'
import MarkdownView from '../components/MarkdownView'
import Spinner from '../components/Spinner'
import { useDialogA11y } from '../components/useDialogA11y'
import { useSession } from '../session'

type PolicyDraft = {
  repeatMode: string
  refreshInterval: string
  dedupWindow: string
  freshnessThreshold: string
  metadata: string
}

const POLICY_METADATA_PLACEHOLDER = `{
  "channels": ["rss"]
}`

function RunDetailModal({ topicId, runId, onClose }: { topicId: string; runId: string; onClose: () => void }) {
  const [md, setMd] = useState<string | null>(null)
  const [mdOpen, setMdOpen] = useState(false)
  const [mdLoading, setMdLoading] = useState(false)
  const containerRef = useRef<HTMLDivElement | null>(null)
  useDialogA11y(containerRef, onClose, '#run-detail-close')
  useEffect(()=>{
    let mounted = true
    // load the primary markdown report on open
    setMdLoading(true)
    api2.runMarkdown(topicId, runId)
      .then(text => { if (mounted) { setMd(text); setMdOpen(true) } })
      .catch((e:any)=>{ if (mounted) { setMd(`Error: ${e?.message || e}`); setMdOpen(true) } })
      .finally(()=>{ if (mounted) setMdLoading(false) })
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
  return (
    <div className="fixed inset-0 z-40 flex items-center justify-center p-4" role="dialog" aria-modal="true" aria-labelledby="run-detail-title">
      <div className="absolute inset-0 bg-slate-950/80 backdrop-blur" onClick={onClose} aria-hidden="true" />
      <div ref={containerRef} className="relative w-full max-w-5xl card max-h-[90vh] overflow-y-auto focus:outline-none" tabIndex={-1}>
          <div className="flex items-center justify-between border-b border-slate-800 px-5 py-3">
            <h3 id="run-detail-title" className="text-sm font-semibold">Run Details</h3>
            <div className="flex items-center gap-2">
              <button className="btn-secondary text-xs px-3" onClick={loadMarkdown} disabled={mdLoading}>{mdLoading ? 'Loading…' : 'View Report'}</button>
              <button id="run-detail-close" className="btn-secondary text-xs px-3" onClick={onClose}>Close</button>
            </div>
          </div>
        <div className="p-5 space-y-5">
          <div className="bg-slate-900/60 border border-slate-800 rounded p-3 max-h-[70vh] overflow-auto">
            {mdLoading ? <Spinner label="Loading run details" /> : (md ? <MarkdownView markdown={md} /> : <div className="text-xs text-slate-500">—</div>)}
          </div>
        </div>
      </div>
    </div>
  )
}

export default function TopicDetailPage() {
  const { id } = useParams<{ id: string }>()
  const qc = useQueryClient()
  const [conversation, setConversation] = useState<ChatMessage[]>([])
  const [oldestCursor, setOldestCursor] = useState<string | null>(null)
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
  const topicQ = useQuery<TopicDetail>({ queryKey: ['topic', id], queryFn: () => api2.getTopic(id!), enabled: !!id })
  const topicName = qc.getQueryData<any>(['topics'])?.find?.((t: any)=>t.ID===id)?.Name
  const policy: TemporalPolicy | null = topicQ.data?.temporal_policy ?? null
  const budgetCfg: BudgetConfig | null = topicQ.data?.budget ?? null
  const pendingBudget: PendingApproval | null = topicQ.data?.pending_budget ?? null
  const [editingName, setEditingName] = useState(false)
  const [newName, setNewName] = useState<string>('')
  const [editingPrefs, setEditingPrefs] = useState(false)
  const [prefsDraft, setPrefsDraft] = useState<string>('')
  const [editingPolicy, setEditingPolicy] = useState(false)
  const [policyDraft, setPolicyDraft] = useState<PolicyDraft>({
    repeatMode: 'adaptive',
    refreshInterval: '',
    dedupWindow: '',
    freshnessThreshold: '',
    metadata: ''
  })
  const [budgetDecisionReason, setBudgetDecisionReason] = useState('')
  const renameMut = useMutation({ mutationFn: (name: string) => api2.updateTopicName(id!, name), onSuccess: ()=>{ qc.invalidateQueries({ queryKey: ['topics'] }); setEditingName(false); toast.success('Topic renamed') }, onError: (e:any)=> toast.error(e.message || 'Rename failed') })
  const prefsMut = useMutation({ mutationFn: (payload: { preferences: any; scheduleCron?: string }) => api2.updateTopicPrefs(id!, payload.preferences, payload.scheduleCron), onSuccess: ()=>{ qc.invalidateQueries({ queryKey: ['topic', id] }); setEditingPrefs(false); toast.success('Preferences updated') }, onError: (e:any)=> toast.error(e.message || 'Update failed') })
  const policyMut = useMutation({
    mutationFn: (payload: { repeat_mode: string; refresh_interval?: string; dedup_window?: string; freshness_threshold?: string; metadata?: Record<string, any> }) =>
      api2.updateTopicPolicy(id!, payload),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['topic', id] })
      setEditingPolicy(false)
      toast.success('Temporal policy updated')
    },
    onError: (e: any) => toast.error(e.message || 'Policy update failed')
  })

  const budgetDecisionMut = useMutation({
    mutationFn: (payload: { approved: boolean; reason?: string }) => api2.decideBudget(id!, pendingBudget!.run_id, payload),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['topic', id] })
      qc.invalidateQueries({ queryKey: ['runs', id] })
      setBudgetDecisionReason('')
      toast.success('Budget decision recorded')
    },
    onError: (e: any) => toast.error(e.message || 'Budget decision failed')
  })

  const beginPolicyEdit = useCallback(() => {
    const current = policy
    setPolicyDraft({
      repeatMode: current?.repeat_mode || 'adaptive',
      refreshInterval: current?.refresh_interval || '',
      dedupWindow: current?.dedup_window || '',
      freshnessThreshold: current?.freshness_threshold || '',
      metadata: current?.metadata ? JSON.stringify(current.metadata, null, 2) : ''
    })
    setEditingPolicy(true)
  }, [policy])

  const savePolicy = useCallback(() => {
    if (!id) return
    let metadataValue: Record<string, any> | undefined
    const metadataText = policyDraft.metadata.trim()
    if (metadataText) {
      try {
        metadataValue = JSON.parse(metadataText)
      } catch (e: any) {
        toast.error('Metadata must be valid JSON')
        return
      }
    }

    const payload: { repeat_mode: string; refresh_interval?: string; dedup_window?: string; freshness_threshold?: string; metadata?: Record<string, any> } = {
      repeat_mode: policyDraft.repeatMode
    }
    const refreshText = policyDraft.refreshInterval.trim()
    const dedupText = policyDraft.dedupWindow.trim()
    const freshText = policyDraft.freshnessThreshold.trim()
    if (refreshText) payload.refresh_interval = refreshText
    if (dedupText) payload.dedup_window = dedupText
    if (freshText) payload.freshness_threshold = freshText
    if (metadataValue !== undefined) payload.metadata = metadataValue

    policyMut.mutate(payload)
  }, [id, policyDraft, policyMut, toast])

  const triggerMut = useMutation({ mutationFn: () => api2.triggerRun(id!), onSuccess: () => { qc.invalidateQueries({ queryKey: ['runs', id] }); toast.success('Run triggered') }, onError: (e:any) => toast.error(e.message || 'Trigger failed') })
  const chatMut = useMutation({
    mutationFn: (msg: string) => api2.chat(id!, msg),
    onSuccess: async (resp, msg) => {
      // Normalize assistant message if backend returned raw JSON
      const clean = normalizeAssistantMessage(resp?.message)
      // Only append assistant reply; the user message was added in send()
      setConversation(prev => [...prev, { role: 'assistant', content: clean }])
      setOptimisticReply(false)
      // Refresh Preferences panel immediately
      try {
        const updated = resp?.topic as any
        const serverPrefs = (updated && (updated.preferences || updated.Preferences)) || null
        let newPrefs: Record<string, any> = serverPrefs || {}
        if (!serverPrefs) {
          // If response didn't include prefs (LLM parse quirks), refetch topic
          await qc.invalidateQueries({ queryKey: ['topic', id] })
          const fresh = await qc.fetchQuery({ queryKey: ['topic', id], queryFn: () => api2.getTopic(id!) }) as any
          newPrefs = (fresh && (fresh.preferences || fresh.Preferences)) || {}
        }
        setLastPrefs(newPrefs)
        // Update cache so UI panel reflects immediately
        qc.setQueryData(['topic', id], (prev: any) => prev ? { ...prev, preferences: newPrefs } : prev)
        toast.success('Preferences updated')
      } catch (e) {
        // Show assistant reply, but we couldn’t update prefs view
        toast.warn('Assistant replied; preferences view unchanged')
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

  useEffect(()=>{
    if (topicQ.data) {
      setLastPrefs(topicQ.data.preferences || {})
    }
  }, [topicQ.data])

  // Load conversation history (latest 20) from server on mount/topic change
  useEffect(()=>{
    let mounted = true
    if (!id) return
    ;(async ()=>{
      try {
        const msgs = await api2.chatHistory(id, { limit: 20 })
        if (!mounted) return
        // API returns newest-first; display ascending
        const ordered = [...msgs].reverse().map(m => ({ role: m.role as 'user'|'assistant', content: m.content }))
        setConversation(ordered)
        if (msgs.length > 0) setOldestCursor(msgs[msgs.length-1].created_at)
        else setOldestCursor(null)
      } catch {}
    })()
    return ()=>{ mounted = false }
  }, [id])

  const send = useCallback(() => {
    const msg = input.trim(); if (!msg) return
    const next = [...conversation, { role: 'user', content: msg }]
    setConversation(next)
    setInput('')
    setOptimisticReply(true)
    chatMut.mutate(msg)
  }, [input, chatMut, conversation, id])

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
          <div id="chat-scroll" className="flex-1 overflow-y-auto px-4 py-4 space-y-3 text-sm" aria-live="polite" onScroll={async (e)=>{ const el=e.currentTarget; if (el.scrollTop < 20 && oldestCursor) { try { const more = await api2.chatHistory(id!, { limit: 20, before: oldestCursor }); if (more.length>0) { const asc = [...more].reverse().map(m=>({ role: m.role as 'user'|'assistant', content: m.content })); setConversation(prev=> [...asc, ...prev]); setOldestCursor(more[more.length-1].created_at) } else { setOldestCursor(null) } } catch {} } }}>
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
                {runsQ.data?.map(r => <RunRow key={r.ID} run={r} onOpen={()=>setOpenRunId(r.ID)} />)}
              </ul>
            </div>
            <div className="card space-y-2">
              <div className="flex items-center justify-between">
                <h4 className="text-xs font-semibold uppercase tracking-wider text-slate-400">Temporal Policy</h4>
                {topicQ.isSuccess && !editingPolicy && (
                  <button className="btn-secondary text-xs px-3" onClick={beginPolicyEdit}>Edit</button>
                )}
              </div>
              {!topicQ.isSuccess && <div className="text-xs text-slate-500">{topicQ.isLoading ? 'Loading policy…' : 'No data'}</div>}
              {topicQ.isSuccess && !editingPolicy && (
                <div className="text-xs space-y-2">
                  <div className="flex items-center justify-between">
                    <span className="text-slate-400">Repeat mode</span>
                    <span className="text-slate-200 font-medium">{policy?.repeat_mode || 'adaptive'}</span>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className="text-slate-400">Refresh interval</span>
                    <span className="text-slate-200">{policy?.refresh_interval || '—'}</span>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className="text-slate-400">Dedup window</span>
                    <span className="text-slate-200">{policy?.dedup_window || '—'}</span>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className="text-slate-400">Freshness threshold</span>
                    <span className="text-slate-200">{policy?.freshness_threshold || '—'}</span>
                  </div>
                  {policy?.metadata && Object.keys(policy.metadata).length > 0 ? (
                    <pre className="bg-slate-900/60 border border-slate-800 rounded p-3 whitespace-pre-wrap overflow-x-auto">
                      {JSON.stringify(policy.metadata, null, 2)}
                    </pre>
                  ) : (
                    <div className="text-slate-500">No additional metadata.</div>
                  )}
                  <p className="text-[11px] text-slate-500">Durations use Go format (e.g. <code>30m</code>, <code>1h30m</code>). Blank fields keep defaults.</p>
                </div>
              )}
              {topicQ.isSuccess && editingPolicy && (
                <form className="space-y-3" onSubmit={(e)=>{ e.preventDefault(); savePolicy() }}>
                  <label className="flex flex-col gap-1 text-xs">
                    <span className="text-slate-300">Repeat mode</span>
                    <select
                      className="input text-xs"
                      value={policyDraft.repeatMode}
                      onChange={e=>setPolicyDraft(prev=>({ ...prev, repeatMode: e.target.value }))}
                    >
                      <option value="adaptive">adaptive</option>
                      <option value="always">always</option>
                      <option value="manual">manual</option>
                    </select>
                  </label>
                  <div className="grid gap-2 sm:grid-cols-3 text-xs">
                    <label className="flex flex-col gap-1">
                      <span className="text-slate-300">Refresh interval</span>
                      <input className="input text-xs" placeholder="e.g. 2h" value={policyDraft.refreshInterval} onChange={e=>setPolicyDraft(prev=>({ ...prev, refreshInterval: e.target.value }))} />
                    </label>
                    <label className="flex flex-col gap-1">
                      <span className="text-slate-300">Dedup window</span>
                      <input className="input text-xs" placeholder="e.g. 6h" value={policyDraft.dedupWindow} onChange={e=>setPolicyDraft(prev=>({ ...prev, dedupWindow: e.target.value }))} />
                    </label>
                    <label className="flex flex-col gap-1">
                      <span className="text-slate-300">Freshness threshold</span>
                      <input className="input text-xs" placeholder="e.g. 12h" value={policyDraft.freshnessThreshold} onChange={e=>setPolicyDraft(prev=>({ ...prev, freshnessThreshold: e.target.value }))} />
                    </label>
                  </div>
                  <label className="flex flex-col gap-1 text-xs">
                    <span className="text-slate-300">Metadata (JSON)</span>
                    <textarea
                      className="w-full h-32 bg-slate-900/60 border border-slate-800 rounded p-3 text-xs font-mono"
                      placeholder={POLICY_METADATA_PLACEHOLDER}
                      value={policyDraft.metadata}
                      onChange={e=>setPolicyDraft(prev=>({ ...prev, metadata: e.target.value }))}
                    />
                  </label>
                  <div className="flex gap-2 justify-end">
                    <button type="button" className="btn-secondary text-xs px-3" onClick={()=>setEditingPolicy(false)} disabled={policyMut.isPending}>Cancel</button>
                    <button type="submit" className="btn text-xs px-4" disabled={policyMut.isPending}>{policyMut.isPending ? 'Saving…' : 'Save'}</button>
                  </div>
                </form>
              )}
            </div>
            <div className="card space-y-2">
              <div className="flex items-center justify-between">
                <h4 className="text-xs font-semibold uppercase tracking-wider text-slate-400">Budget</h4>
              </div>
              {!topicQ.isSuccess && <div className="text-xs text-slate-500">{topicQ.isLoading ? 'Loading budget…' : 'No data'}</div>}
              {topicQ.isSuccess && (
                <div className="text-xs space-y-2">
                  <div className="flex items-center justify-between">
                    <span className="text-slate-400">Max cost</span>
                    <span className="text-slate-200">{budgetCfg?.max_cost != null ? `$${budgetCfg.max_cost.toFixed(2)}` : '—'}</span>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className="text-slate-400">Max tokens</span>
                    <span className="text-slate-200">{budgetCfg?.max_tokens != null ? budgetCfg.max_tokens.toLocaleString() : '—'}</span>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className="text-slate-400">Max time</span>
                    <span className="text-slate-200">{budgetCfg?.max_time_seconds != null ? `${budgetCfg.max_time_seconds}s` : '—'}</span>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className="text-slate-400">Approval threshold</span>
                    <span className="text-slate-200">{budgetCfg?.approval_threshold != null ? `$${budgetCfg.approval_threshold.toFixed(2)}` : '—'}</span>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className="text-slate-400">Requires approval</span>
                    <span className="text-slate-200">{budgetCfg?.require_approval ? 'Yes' : 'No'}</span>
                  </div>
                  {budgetCfg?.metadata && Object.keys(budgetCfg.metadata).length > 0 && (
                    <pre className="bg-slate-900/60 border border-slate-800 rounded p-3 whitespace-pre-wrap overflow-x-auto">
                      {JSON.stringify(budgetCfg.metadata, null, 2)}
                    </pre>
                  )}
                </div>
              )}
            </div>
            {pendingBudget && (
              <div className="card space-y-3">
                <div className="flex items-center justify-between">
                  <h4 className="text-xs font-semibold uppercase tracking-wider text-slate-400">Pending Budget Approval</h4>
                </div>
                <div className="text-xs space-y-2">
                  <div className="flex items-center justify-between"><span className="text-slate-400">Run ID</span><span className="text-slate-200 font-mono">{pendingBudget.run_id}</span></div>
                  <div className="flex items-center justify-between"><span className="text-slate-400">Estimated cost</span><span className="text-slate-200">${pendingBudget.estimated_cost.toFixed(2)}</span></div>
                  <div className="flex items-center justify-between"><span className="text-slate-400">Threshold</span><span className="text-slate-200">${pendingBudget.threshold.toFixed(2)}</span></div>
                  <div className="flex items-center justify-between"><span className="text-slate-400">Requested by</span><span className="text-slate-200">{pendingBudget.requested_by}</span></div>
                  <div className="flex items-center justify-between"><span className="text-slate-400">Requested at</span><span className="text-slate-200">{new Date(pendingBudget.created_at).toLocaleString()}</span></div>
                </div>
                <label className="text-xs flex flex-col gap-1">
                  <span className="text-slate-300">Decision note (required for rejection)</span>
                  <textarea className="w-full h-20 bg-slate-900/60 border border-slate-800 rounded p-3 text-xs" value={budgetDecisionReason} onChange={e=>setBudgetDecisionReason(e.target.value)} placeholder="Reason for rejection" />
                </label>
                <div className="flex gap-2 justify-end">
                  <button className="btn-secondary text-xs px-3" disabled={budgetDecisionMut.isPending} onClick={()=>budgetDecisionMut.mutate({ approved: true })}>Approve</button>
                  <button className="btn text-xs px-3" disabled={budgetDecisionMut.isPending || !budgetDecisionReason.trim()} onClick={()=>budgetDecisionMut.mutate({ approved: false, reason: budgetDecisionReason.trim() })}>{budgetDecisionMut.isPending ? 'Submitting…' : 'Reject'}</button>
                </div>
              </div>
            )}
            <div className="card space-y-2">
              <div className="flex items-center justify-between">
                <h4 className="text-xs font-semibold uppercase tracking-wider text-slate-400">Preferences</h4>
                {topicQ.isSuccess && !editingPrefs && (
                  <button className="btn-secondary text-xs px-3" onClick={()=>{ setPrefsDraft(JSON.stringify(topicQ.data.preferences || {}, null, 2)); setEditingPrefs(true) }}>Edit</button>
                )}
              </div>
              {!topicQ.isSuccess && <div className="text-xs text-slate-500">{topicQ.isLoading ? 'Loading topic…' : 'No data'}</div>}
              {topicQ.isSuccess && !editingPrefs && (
                <pre className="text-xs bg-slate-900/60 border border-slate-800 rounded p-3 max-h-64 overflow-auto whitespace-pre-wrap">{JSON.stringify(topicQ.data.preferences || {}, null, 2)}</pre>
              )}
              {topicQ.isSuccess && editingPrefs && (
                <div className="space-y-2">
                  <textarea className="w-full h-48 bg-slate-900/60 border border-slate-800 rounded p-3 text-xs font-mono" value={prefsDraft} onChange={e=>setPrefsDraft(e.target.value)} />
                  <div className="flex gap-2 justify-end">
                    <button className="btn-secondary text-xs px-3" onClick={()=>setEditingPrefs(false)}>Cancel</button>
                    <button className="btn text-xs px-4" onClick={()=>{
                      try {
                        const obj = JSON.parse(prefsDraft)
                        prefsMut.mutate({ preferences: obj })
                      } catch (e:any) {
                        toast.error('Invalid JSON')
                      }
                    }} disabled={prefsMut.isPending}>{prefsMut.isPending ? 'Saving…' : 'Save'}</button>
                  </div>
                </div>
              )}
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

function RunRow({ run, onOpen }: { run: Run; onOpen: () => void }) {
  const color = run.Status === 'succeeded' ? 'bg-emerald-500/20 text-emerald-300 border-emerald-500/30' : run.Status === 'failed' ? 'bg-red-500/20 text-red-300 border-red-500/30' : 'bg-slate-600/20 text-slate-300 border-slate-500/30'
  return (
    <li className="flex flex-col gap-2 p-2 rounded border border-slate-800 bg-slate-900/40">
      <div className="flex items-start gap-3">
        <div className={`px-2 py-0.5 rounded-full text-xs font-medium border ${color}`}>{run.Status}</div>
        <div className="flex-1 leading-tight">
          <div className="text-slate-300">{formatDate(run.StartedAt)}</div>
          {run.Error && <div className="text-xs text-red-400 mt-1">{run.Error}</div>}
        </div>
        <button className="btn-secondary text-xs px-2 py-1" onClick={onOpen}>Details</button>
      </div>
    </li>
  )
}

// (client no longer persists; history comes from backend)

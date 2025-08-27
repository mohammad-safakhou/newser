import React, { useEffect, useState, useCallback, useRef } from 'react'
import { useParams, Link } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import PreferenceDiff, { DiffEntry } from '../components/PreferenceDiff'
import { api2, Run, formatDate, ChatMessage } from '../api'
import { useToast } from '../components/Toasts'
import ConfidenceGauge from '../components/ConfidenceGauge'
import HighlightsList from '../components/HighlightsList'
import KnowledgeGraph from '../components/KnowledgeGraph'

export default function TopicDetailPage() {
  const { id } = useParams<{ id: string }>()
  const qc = useQueryClient()
  const [conversation, setConversation] = useState<ChatMessage[]>([])
  const [input, setInput] = useState('')
  const [optimisticReply, setOptimisticReply] = useState(false)
  const [showDetail, setShowDetail] = useState(false)
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

  const triggerMut = useMutation({ mutationFn: () => api2.triggerRun(id!), onSuccess: () => { qc.invalidateQueries({ queryKey: ['runs', id] }); toast.success('Run triggered') }, onError: (e:any) => toast.error(e.message || 'Trigger failed') })
  const chatMut = useMutation({ mutationFn: (msg: string) => api2.chat(id!, msg), onSuccess: (resp, msg) => {
    setConversation(prev => [...prev, { role: 'user', content: msg }, { role: 'assistant', content: resp.message }])
    setOptimisticReply(false)
    try {
      const updated = resp.topic
      const newPrefs = (updated && updated.preferences) || updated?.Preferences || {}
      setDiffs(computeDiff(lastPrefs || (topicQ.data?.preferences || {}), newPrefs))
      setLastPrefs(newPrefs)
    } catch {}
    toast.success('Updated preferences')
  }, onError: (e:any) => { setOptimisticReply(false); toast.error((e && e.message) || 'Chat failed') } })

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
      <header className="px-6 py-3 border-b border-slate-800 flex items-center gap-4">
        <Link to="/topics" className="text-xs text-slate-400 hover:text-brand-400">← Topics</Link>
        <h2 className="text-lg font-semibold flex-1 truncate">{topicName || 'Topic'}</h2>
        <button className="btn-secondary text-xs px-3" disabled={triggerMut.isPending} onClick={()=>triggerMut.mutate()}>{triggerMut.isPending ? 'Triggering…' : 'Trigger Run'}</button>
      </header>
      <div className="flex-1 grid lg:grid-cols-2 gap-0 overflow-hidden">
        <section className="flex flex-col border-r border-slate-800 min-h-0">
          <div className="px-5 py-3 border-b border-slate-800 flex items-center justify-between">
            <h3 className="text-sm font-medium tracking-wide text-slate-300">Conversation</h3>
            {chatMut.isPending && <span className="text-[10px] text-slate-500 animate-pulse">thinking…</span>}
          </div>
          <div className="flex-1 overflow-y-auto px-4 py-4 space-y-3 text-sm">
            {conversation.length === 0 && <div className="text-slate-500 text-xs">Start refining this topic by asking for scope, preferences, or scheduling help.</div>}
            {conversation.map((m, i) => (
              <div key={i} className={m.role === 'user' ? 'flex justify-end' : 'flex justify-start'}>
                <div className={(m.role==='user' ? 'bg-brand-600 text-white' : 'bg-slate-800 text-slate-100') + ' max-w-[80%] rounded-lg px-3 py-2 whitespace-pre-wrap leading-relaxed text-[13px]'}>{m.content}</div>
              </div>
            ))}
            {optimisticReply && <div className="flex justify-start"><div className="bg-slate-800 rounded-lg px-3 py-2 text-[13px] text-slate-400"><span className="inline-flex gap-1"><span className="w-1.5 h-1.5 rounded-full bg-slate-500 animate-pulse"/><span className="w-1.5 h-1.5 rounded-full bg-slate-600 animate-pulse delay-150"/><span className="w-1.5 h-1.5 rounded-full bg-slate-700 animate-pulse delay-300"/></span></div></div>}
            <div ref={bottomRef} />
          </div>
          <form onSubmit={e=>{e.preventDefault(); send()}} className="p-3 flex gap-2 border-t border-slate-800">
            <input className="input text-xs" placeholder="Ask or refine topic…" value={input} onChange={e=>setInput(e.target.value)} disabled={chatMut.isPending} />
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
              <h4 className="text-xs font-semibold uppercase tracking-wider text-slate-400 flex items-center justify-between">Latest Result
                {latestQ.data && <button onClick={()=>setShowDetail(s=>!s)} className="text-[10px] text-brand-300 hover:underline ml-2">{showDetail ? 'Hide' : 'Details'}</button>}
              </h4>
              {latestQ.isLoading && <div className="text-xs text-slate-500">Loading…</div>}
              {latestQ.isError && <div className="text-xs text-red-400">No result</div>}
              {latestQ.data && (
                <div className="space-y-3 text-xs leading-relaxed">
                  <div className="flex items-center gap-4">
                    <div className="flex-1"><span className="text-slate-400">Summary:</span> {latestQ.data.summary || '—'}</div>
                    <ConfidenceGauge value={typeof latestQ.data.confidence === 'number' ? latestQ.data.confidence : null} />
                  </div>
                  {showDetail && (
                    <>
                      <div className="space-y-1">
                        <div className="text-slate-400">Detailed Report:</div>
                        <div className="max-h-40 overflow-auto whitespace-pre-wrap bg-slate-900/50 p-2 rounded border border-slate-800 text-[11px]">{latestQ.data.detailed_report || '—'}</div>
                      </div>
                      <div className="space-y-1">
                        <div className="text-slate-400">Sources ({Array.isArray(latestQ.data.sources)? latestQ.data.sources.length : 0}):</div>
                        <ul className="max-h-32 overflow-auto space-y-1 text-[11px] list-disc pl-4">
                          {Array.isArray(latestQ.data.sources) && latestQ.data.sources.map((s:any,i:number)=>(
                            <li key={i} className="truncate" title={s.url || s.URL || s.title || s.Title}>{s.title || s.Title || s.url || s.URL || 'Source'}</li>
                          ))}
                        </ul>
                      </div>
                    </>
                  )}
                </div>
              )}
            </div>
            <div className="card space-y-3">
              <h4 className="text-xs font-semibold uppercase tracking-wider text-slate-400">Runs</h4>
              {runsQ.isLoading && <div className="text-xs text-slate-500">Loading…</div>}
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
            {latestQ.data && showDetail && Array.isArray(latestQ.data.highlights) && latestQ.data.highlights.length > 0 && (
              <div className="card space-y-2">
                <h4 className="text-xs font-semibold uppercase tracking-wider text-slate-400">Highlights ({latestQ.data.highlights.length})</h4>
                <HighlightsList items={latestQ.data.highlights} />
              </div>
            )}
            {latestQ.data && showDetail && latestQ.data.metadata && latestQ.data.metadata.knowledge_graph && (
              <div className="card space-y-3">
                <h4 className="text-xs font-semibold uppercase tracking-wider text-slate-400">Knowledge Graph</h4>
                <KnowledgeGraph nodes={(latestQ.data.metadata.knowledge_graph.nodes)||[]} edges={(latestQ.data.metadata.knowledge_graph.edges)||[]} />
              </div>
            )}
          </div>
        </section>
      </div>
    </div>
  )
}

function RunRow({ run }: { run: Run }) {
  const color = run.Status === 'succeeded' ? 'bg-emerald-500/20 text-emerald-300 border-emerald-500/30' : run.Status === 'failed' ? 'bg-red-500/20 text-red-300 border-red-500/30' : 'bg-slate-600/20 text-slate-300 border-slate-500/30'
  return (
    <li className="flex items-start gap-3 p-2 rounded border border-slate-800 bg-slate-900/40">
      <div className={`px-2 py-0.5 rounded-full text-[10px] font-medium border ${color}`}>{run.Status}</div>
      <div className="flex-1 leading-tight">
        <div className="text-slate-300">{formatDate(run.StartedAt)}</div>
        {run.Error && <div className="text-[10px] text-red-400 mt-1">{run.Error}</div>}
      </div>
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

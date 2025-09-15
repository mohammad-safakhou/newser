import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { api2 } from '../api'
import PreferenceDiff, { DiffEntry } from './PreferenceDiff'
import cronParser from 'cron-parser'
import { useDialogA11y } from './useDialogA11y'

interface ProvisionalTopic { name: string; preferences: Record<string, any>; schedule_cron: string }
interface Props { onClose: () => void; onCreated: () => void }
interface ChatMsg { role: 'user' | 'assistant'; content: string }

type Step = 0 | 1 | 2 | 3

export default function NewTopicWizard({ onClose, onCreated }: Props) {
  const [step, setStep] = useState<Step>(0)
  const [name, setName] = useState('')
  const [goalMsg, setGoalMsg] = useState('')
  const [assistConversation, setAssistConversation] = useState<ChatMsg[]>([])
  const [provisional, setProvisional] = useState<ProvisionalTopic>({ name: '', preferences: {}, schedule_cron: '@daily' })
  const [prefMessage, setPrefMessage] = useState('')
  const [prefDiffs, setPrefDiffs] = useState<DiffEntry[]>([])
  const [scheduleCron, setScheduleCron] = useState('@daily')
  const [cronError, setCronError] = useState<string | null>(null)
  const [finalizing, setFinalizing] = useState(false)
  const scrollRef = useRef<HTMLDivElement | null>(null)
  const containerRef = useRef<HTMLDivElement | null>(null)
  useDialogA11y(containerRef, onClose, '#new-topic-close')

  const assistMut = useMutation({
    mutationFn: (message: string) => api2.assistChat({ message, name, preferences: provisional.preferences, schedule_cron: provisional.schedule_cron }),
    onSuccess: (resp, sent) => {
      setAssistConversation(prev => [...prev, { role: 'user', content: sent }, { role: 'assistant', content: resp.message }])
      // merge returned topic
      const t = resp.topic || {}
      const newPrefs = t.preferences || t.Preferences || provisional.preferences
      const newCron = t.cron_spec || t.CronSpec || provisional.schedule_cron
      // Do NOT override user's chosen name from LLM suggestions
      setProvisional(p => ({ ...p, preferences: newPrefs, schedule_cron: newCron }))
    }
  })

  const prefsMut = useMutation({
    mutationFn: (message: string) => api2.assistChat({ message, name: provisional.name || name, preferences: provisional.preferences, schedule_cron: provisional.schedule_cron }),
    onSuccess: (resp, sent) => {
      const updated = resp.topic || {}
      const newPrefs = updated.preferences || updated.Preferences || {}
      const diffs = computeDiff(provisional.preferences, newPrefs)
      setPrefDiffs(diffs)
      setProvisional(p => ({ ...p, preferences: newPrefs, schedule_cron: updated.cron_spec || updated.CronSpec || p.schedule_cron }))
      setPrefMessage('')
    }
  })

  const createMut = useMutation({
    mutationFn: () => api2.createTopicFull(name, provisional.preferences, scheduleCron),
    onSuccess: () => { onCreated() }
  })

  useEffect(()=>{ scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight, behavior: 'smooth' }) }, [assistConversation])

  useEffect(()=>{ setProvisional(p => ({ ...p, schedule_cron: scheduleCron })) }, [scheduleCron])

  const nextDisabled = useMemo(()=>{
    if (step === 0) return !name.trim() || assistMut.isPending
    if (step === 1) return false
    if (step === 2) return !!cronError
    return false
  }, [step, name, assistMut.isPending, cronError])

  const validateCron = useCallback((val: string) => {
    if (!val) return 'Cron required'
      try {
        const expr = macroToCron(val)
        cronParser.parse(expr)
        return null
      } catch (e:any) {
      return e.message?.slice(0,80) || 'Invalid cron'
    }
  }, [])

  useEffect(()=>{ setCronError(validateCron(scheduleCron)) }, [scheduleCron, validateCron])

  const advance = () => setStep(s => (s < 3 ? (s + 1) as Step : s))
  const back = () => setStep(s => (s > 0 ? (s - 1) as Step : s))

  const sendAssist = () => {
    if (!goalMsg.trim()) return
    assistMut.mutate(goalMsg.trim())
    setGoalMsg('')
  }

  const refinePrefs = () => {
    if (!prefMessage.trim()) return
    prefsMut.mutate(prefMessage.trim())
  }

  const finalize = async () => {
    setFinalizing(true)
    try { await createMut.mutateAsync() } finally { setFinalizing(false) }
  }

  return (
    <div className="fixed inset-0 z-40 flex items-center justify-center p-4" role="dialog" aria-modal="true" aria-labelledby="new-topic-title">
      <div className="absolute inset-0 bg-slate-950/80 backdrop-blur" onClick={onClose} aria-hidden="true" />
      <div ref={containerRef} className="relative w-full max-w-3xl card p-0 flex flex-col max-h-[90vh] focus:outline-none" tabIndex={-1}>
        <header className="px-6 py-4 border-b border-slate-800 flex items-center gap-4">
          <h3 id="new-topic-title" className="text-base font-semibold tracking-wide">New Topic Wizard</h3>
          <span className="text-xs text-slate-500">Step {step+1} / 4</span>
          <div className="ml-auto flex gap-2">
            <button id="new-topic-close" onClick={onClose} className="btn-secondary text-xs px-3">Close</button>
          </div>
        </header>
        <div className="flex-1 overflow-y-auto" ref={scrollRef}>
          {step === 0 && <StepIdea name={name} setName={setName} goalMsg={goalMsg} setGoalMsg={setGoalMsg} conversation={assistConversation} sendAssist={sendAssist} loading={assistMut.isPending} provisional={provisional} setProvisional={setProvisional} />}
          {step === 1 && <StepPreferences provisional={provisional} setProvisional={setProvisional} prefMessage={prefMessage} setPrefMessage={setPrefMessage} refine={refinePrefs} loading={prefsMut.isPending} diffs={prefDiffs} />}
          {step === 2 && <StepSchedule scheduleCron={scheduleCron} setScheduleCron={setScheduleCron} error={cronError} />}
          {step === 3 && <StepReview provisional={provisional} scheduleCron={scheduleCron} prefDiffs={prefDiffs} creating={createMut.isPending || finalizing} />}
        </div>
        <footer className="px-6 py-4 border-t border-slate-800 flex items-center gap-3">
          <div className="flex-1 text-xs text-slate-500 truncate" aria-live="polite">{createMut.isError && <span className="text-red-400">{(createMut.error as Error).message}</span>}</div>
          {step>0 && <button onClick={back} className="btn-secondary text-xs px-4">Back</button>}
          {step < 3 && <button onClick={advance} disabled={nextDisabled} className="btn text-xs px-5">Next</button>}
          {step === 3 && <button onClick={finalize} disabled={createMut.isPending || finalizing} className="btn text-xs px-5">{finalizing ? 'Creating…' : 'Create Topic'}</button>}
        </footer>
      </div>
    </div>
  )
}

  function StepIdea({ name, setName, goalMsg, setGoalMsg, conversation, sendAssist, loading, provisional, setProvisional }: {
    name: string
    setName: (v: string) => void
    goalMsg: string
    setGoalMsg: (v: string) => void
    conversation: ChatMsg[]
    sendAssist: () => void
    loading: boolean
    provisional: ProvisionalTopic
    setProvisional: (fn: any) => void
  }) {
  const applyPreset = (preset: string) => {
    const base: any = { search: { count: 20, freshness: 'pw', safesearch: 'moderate' }, analysis: { min_credibility: 0.5, key_topics_limit: 10, relevance_weight: 0.4, credibility_weight: 0.3, importance_weight: 0.3, sentiment_mode: 'detect' } }
    if (preset === 'US Daily') {
      base.search.country = 'US'; base.search.search_lang = 'en'; base.search.ui_lang = 'en-US';
      base.search.domains_preferred = ['reuters.com','apnews.com','bbc.com','npr.org']
    } else if (preset === 'Markets AM') {
      base.search.country = 'US'; base.search.search_lang = 'en'; base.search.ui_lang = 'en-US';
      base.search.domains_preferred = ['bloomberg.com','ft.com','wsj.com','cnbc.com']
    } else if (preset === 'Policy Watch') {
      base.search.country = 'US'; base.search.search_lang = 'en'; base.search.ui_lang = 'en-US';
      base.search.domains_preferred = ['politico.com','axios.com','nytimes.com','washingtonpost.com']
    }
    setProvisional((p: ProvisionalTopic) => ({ ...p, preferences: { ...(p.preferences||{}), ...base } }))
  }
  return (
    <div className="p-6 space-y-6">
      <div className="space-y-2">
        <label className="text-xs font-medium text-slate-400">Topic Name</label>
        <input className="input text-sm" value={name} onChange={e=>setName(e.target.value)} placeholder="e.g. Global Renewable Energy Trends" />
      </div>
      <div className="space-y-2">
        <label className="text-xs font-medium text-slate-400">Preset</label>
        <div className="flex gap-2">
          {['US Daily','Markets AM','Policy Watch'].map(p=> (
            <button key={p} type="button" onClick={()=>applyPreset(p)} className="btn-secondary text-xs px-3">{p}</button>
          ))}
        </div>
      </div>
      <div className="space-y-2">
        <label className="text-xs font-medium text-slate-400">Describe your goal / scope</label>
        <div className="flex gap-2">
          <input className="input text-xs flex-1" value={goalMsg} onChange={e=>setGoalMsg(e.target.value)} placeholder="What do you want this topic to cover?" />
          <button type="button" onClick={sendAssist} disabled={!goalMsg.trim() || loading} className="btn text-xs px-4">{loading ? '...' : 'Ask AI'}</button>
        </div>
        <div className="bg-slate-900/60 rounded border border-slate-800 p-3 h-40 overflow-y-auto text-xs space-y-2">
          {conversation.length === 0 && <div className="text-slate-500">AI suggestions will appear here.</div>}
          {conversation.map((m,i)=> <div key={i} className={m.role==='user' ? 'text-brand-300' : 'text-slate-300'}>{m.role==='user' ? 'You: ' : 'AI: '}{m.content}</div> )}
        </div>
      </div>
      <div className="text-xs text-slate-500 space-y-1">
        <div><span className="text-slate-400">Draft Name:</span> {provisional.name || '—'}</div>
        <div><span className="text-slate-400">Draft Cron:</span> {provisional.schedule_cron}</div>
        <div><span className="text-slate-400">Pref Keys:</span> {Object.keys(provisional.preferences||{}).length}</div>
      </div>
    </div>
  )
}

function StepPreferences({ provisional, setProvisional, prefMessage, setPrefMessage, refine, loading, diffs }: any) {
  const search = (provisional.preferences?.search || {}) as any
  const analysis = (provisional.preferences?.analysis || {}) as any
  const kg = (provisional.preferences?.knowledge_graph || {}) as any
  const conflict = (provisional.preferences?.conflict || {}) as any
  const setSearch = (patch: any) => {
    setProvisional((p: ProvisionalTopic) => {
      const prefs = { ...(p.preferences || {}) }
      const nextSearch = { ...(prefs.search || {}), ...patch }
      return { ...p, preferences: { ...prefs, search: nextSearch } }
    })
  }
  const setAnalysis = (patch: any) => {
    setProvisional((p: ProvisionalTopic) => {
      const prefs = { ...(p.preferences || {}) }
      const next = { ...(prefs.analysis || {}), ...patch }
      return { ...p, preferences: { ...prefs, analysis: next } }
    })
  }
  const setKG = (patch: any) => {
    setProvisional((p: ProvisionalTopic) => {
      const prefs = { ...(p.preferences || {}) }
      const next = { ...(prefs.knowledge_graph || {}), ...patch }
      return { ...p, preferences: { ...prefs, knowledge_graph: next } }
    })
  }
  const setConflict = (patch: any) => {
    setProvisional((p: ProvisionalTopic) => {
      const prefs = { ...(p.preferences || {}) }
      const next = { ...(prefs.conflict || {}), ...patch }
      return { ...p, preferences: { ...prefs, conflict: next } }
    })
  }
  const parseCsv = (s: string) => s.split(',').map(x=>x.trim()).filter(Boolean)
  return (
    <div className="p-6 space-y-6">
      <div>
        <h4 className="text-sm font-semibold mb-1">Refine Preferences</h4>
        <p className="text-xs text-slate-500">Ask for additions, constraints, or quality requirements. Each AI response updates the draft preferences.</p>
      </div>
      <div className="flex gap-2">
        <input className="input text-xs flex-1" value={prefMessage} onChange={e=>setPrefMessage(e.target.value)} placeholder="e.g. Add bias detection and focus on APAC markets" />
        <button type="button" onClick={refine} disabled={!prefMessage.trim() || loading} className="btn text-xs px-4">{loading ? '...' : 'Refine'}</button>
      </div>
      <div className="grid md:grid-cols-2 gap-4">
        <div className="space-y-3 bg-slate-900/50 border border-slate-800 rounded p-3">
          <h5 className="text-xs uppercase tracking-wide text-slate-400 font-medium">Search Preferences</h5>
          <div className="flex items-center justify-between gap-2">
            <label className="text-xs text-slate-300">Strict domain mode</label>
            <input type="checkbox" className="h-4 w-4" checked={!!search.strict_domains} onChange={e=>setSearch({ strict_domains: e.target.checked })} />
          </div>
          <div className="space-y-1">
            <label className="text-xs text-slate-400">Preferred domains (comma-separated)</label>
            <input className="input text-xs" value={(search.domains_preferred||[]).join(', ')} onChange={e=>setSearch({ domains_preferred: parseCsv(e.target.value) })} placeholder="e.g. reuters.com, apnews.com, bbc.com" />
          </div>
          <div className="space-y-1">
            <label className="text-xs text-slate-400">Blocked domains (comma-separated)</label>
            <input className="input text-xs" value={(search.domains_blocked||[]).join(', ')} onChange={e=>setSearch({ domains_blocked: parseCsv(e.target.value) })} placeholder="e.g. example.com, lowtrust.com" />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1">
              <label className="text-xs text-slate-400">Safesearch</label>
              <select className="input text-xs" value={search.safesearch||''} onChange={e=>setSearch({ safesearch: e.target.value })}>
                <option value="">Default</option>
                <option value="off">off</option>
                <option value="moderate">moderate</option>
                <option value="strict">strict</option>
              </select>
            </div>
            <div className="space-y-1">
              <label className="text-xs text-slate-400">Freshness</label>
              <select className="input text-xs" value={search.freshness||''} onChange={e=>setSearch({ freshness: e.target.value })}>
                <option value="">Auto</option>
                <option value="pd">24h</option>
                <option value="pw">7d</option>
                <option value="pm">31d</option>
                <option value="py">365d</option>
              </select>
            </div>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1">
              <label className="text-xs text-slate-400">Search language</label>
              <input className="input text-xs" value={search.search_lang||''} onChange={e=>setSearch({ search_lang: e.target.value })} placeholder="e.g. en" />
            </div>
            <div className="space-y-1">
              <label className="text-xs text-slate-400">UI language</label>
              <input className="input text-xs" value={search.ui_lang||''} onChange={e=>setSearch({ ui_lang: e.target.value })} placeholder="e.g. en-US" />
            </div>
          </div>
          <p className="text-[11px] text-slate-500">When strict domain mode is on, search results are limited to preferred domains. Blocked domains are excluded regardless.</p>
        </div>
        <div className="space-y-3">
          <div className="bg-slate-900/50 border border-slate-800 rounded p-3 space-y-2">
            <h5 className="text-xs uppercase tracking-wide text-slate-400 font-medium">Analysis</h5>
            <div className="grid grid-cols-2 gap-3">
              <label className="text-xs text-slate-400">Min credibility
                <input type="number" step="0.05" min={0} max={1} className="input text-xs" value={analysis.min_credibility ?? ''} onChange={e=>setAnalysis({ min_credibility: parseFloat(e.target.value) || 0 })} />
              </label>
              <label className="text-xs text-slate-400">Sources limit
                <input type="number" min={1} className="input text-xs" value={analysis.sources_limit ?? ''} onChange={e=>setAnalysis({ sources_limit: parseInt(e.target.value||'0') })} />
              </label>
            </div>
            <div className="grid grid-cols-3 gap-3">
              <label className="text-xs text-slate-400">Relevance
                <input type="number" step="0.1" min={0} max={1} className="input text-xs" value={analysis.relevance_weight ?? ''} onChange={e=>setAnalysis({ relevance_weight: parseFloat(e.target.value)||0 })} />
              </label>
              <label className="text-xs text-slate-400">Credibility
                <input type="number" step="0.1" min={0} max={1} className="input text-xs" value={analysis.credibility_weight ?? ''} onChange={e=>setAnalysis({ credibility_weight: parseFloat(e.target.value)||0 })} />
              </label>
              <label className="text-xs text-slate-400">Importance
                <input type="number" step="0.1" min={0} max={1} className="input text-xs" value={analysis.importance_weight ?? ''} onChange={e=>setAnalysis({ importance_weight: parseFloat(e.target.value)||0 })} />
              </label>
            </div>
            <div className="grid grid-cols-2 gap-3">
              <label className="text-xs text-slate-400">Key topics limit
                <input type="number" min={1} className="input text-xs" value={analysis.key_topics_limit ?? ''} onChange={e=>setAnalysis({ key_topics_limit: parseInt(e.target.value||'0') })} />
              </label>
              <label className="text-xs text-slate-400">Sentiment mode
                <select className="input text-xs" value={analysis.sentiment_mode || ''} onChange={e=>setAnalysis({ sentiment_mode: e.target.value })}>
                  <option value="">none</option>
                  <option value="detect">detect</option>
                </select>
              </label>
            </div>
          </div>
          <div className="bg-slate-900/50 border border-slate-800 rounded p-3 space-y-2">
            <h5 className="text-xs uppercase tracking-wide text-slate-400 font-medium">Knowledge Graph</h5>
            <div className="space-y-1">
              <label className="text-xs text-slate-400">Entity types (comma-separated)</label>
              <input className="input text-xs" value={(kg.entity_types||[]).join(', ')} onChange={e=>setKG({ entity_types: parseCsv(e.target.value) })} placeholder="person, organization, location, event" />
            </div>
            <div className="space-y-1">
              <label className="text-xs text-slate-400">Relation types (comma-separated)</label>
              <input className="input text-xs" value={(kg.relation_types||[]).join(', ')} onChange={e=>setKG({ relation_types: parseCsv(e.target.value) })} placeholder="partnership, acquisition, competition" />
            </div>
          </div>
          <div className="bg-slate-900/50 border border-slate-800 rounded p-3 space-y-2">
            <h5 className="text-xs uppercase tracking-wide text-slate-400 font-medium">Conflict Detection</h5>
            <div className="grid grid-cols-2 gap-3">
              <label className="text-xs text-slate-400">Threshold
                <input type="number" step="0.05" min={0} max={1} className="input text-xs" value={conflict.contradictory_threshold ?? ''} onChange={e=>setConflict({ contradictory_threshold: parseFloat(e.target.value) || 0 })} />
              </label>
              <label className="text-xs text-slate-400">Grouping by
                <select className="input text-xs" value={conflict.grouping_by || ''} onChange={e=>setConflict({ grouping_by: e.target.value })}>
                  <option value="">topic</option>
                  <option value="topic">topic</option>
                  <option value="claim">claim</option>
                  <option value="source">source</option>
                </select>
              </label>
            </div>
            <div className="flex items-center justify-between gap-2">
              <label className="text-xs text-slate-300">Require citations</label>
              <input type="checkbox" className="h-4 w-4" checked={!!conflict.require_citations} onChange={e=>setConflict({ require_citations: e.target.checked })} />
            </div>
            <div className="flex items-center justify-between gap-2">
              <label className="text-xs text-slate-300">Stance detection</label>
              <input type="checkbox" className="h-4 w-4" checked={!!conflict.stance_detection} onChange={e=>setConflict({ stance_detection: e.target.checked })} />
            </div>
          </div>
        </div>
      </div>
      <div className="grid md:grid-cols-2 gap-4">
        <div className="space-y-2">
          <h5 className="text-xs uppercase tracking-wide text-slate-400 font-medium">Current Preferences (JSON)</h5>
          <pre className="text-xs bg-slate-900/60 border border-slate-800 rounded p-3 max-h-64 overflow-auto whitespace-pre-wrap">{JSON.stringify(provisional.preferences, null, 2) || '{}'}</pre>
        </div>
        <div className="space-y-2">
          <h5 className="text-xs uppercase tracking-wide text-slate-400 font-medium">Last Changes</h5>
            <div className="bg-slate-900/60 border border-slate-800 rounded p-3 max-h-64 overflow-auto">
              <PreferenceDiff diffs={diffs} />
            </div>
        </div>
      </div>
    </div>
  )
}

function StepSchedule({ scheduleCron, setScheduleCron, error }: any) {
  const [nextRuns, setNextRuns] = useState<string[]>([])
  useEffect(()=>{
    if (error) { setNextRuns([]); return }
      try {
        const expr = macroToCron(scheduleCron)
        const it = cronParser.parse(expr, { currentDate: new Date() })
        const arr: string[] = []
        for (let i=0;i<5;i++) arr.push(new Date(it.next().toString()).toLocaleString())
        setNextRuns(arr)
      } catch { setNextRuns([]) }
  }, [scheduleCron, error])
  return (
    <div className="p-6 space-y-6">
      <div>
        <h4 className="text-sm font-semibold mb-1">Scheduling</h4>
        <p className="text-xs text-slate-500">Use a cron expression (standard 5-field) or macros like @daily. Preview of next run times shown.</p>
      </div>
      <div className="space-y-2">
        <label className="text-xs font-medium text-slate-400">Cron Expression</label>
        <input className="input text-xs" value={scheduleCron} onChange={e=>setScheduleCron(e.target.value)} placeholder="@daily or 0 9 * * *" />
        {error && <div className="text-xs text-red-400">{error}</div>}
        {!error && <div className="text-xs text-emerald-300/80">Valid expression</div>}
      </div>
      <div className="grid md:grid-cols-2 gap-4">
        <div className="bg-slate-900/60 border border-slate-800 rounded p-3 text-xs space-y-1">
          <p className="text-slate-400">Examples:</p>
          <ul className="list-disc pl-4 space-y-0.5">
            <li><code>@daily</code> – once per day</li>
            <li><code>0 */6 * * *</code> – every 6 hours</li>
            <li><code>15 9 * * MON-FRI</code> – 09:15 weekdays</li>
            <li><code>0 0 1 * *</code> – first day monthly</li>
          </ul>
        </div>
        <div className="bg-slate-900/60 border border-slate-800 rounded p-3 text-xs space-y-1">
          <p className="text-slate-400">Next Runs:</p>
          {nextRuns.length === 0 && <div className="text-slate-500">—</div>}
          <ol className="list-decimal pl-4 space-y-0.5">
            {nextRuns.map((r,i)=>(<li key={i}>{r}</li>))}
          </ol>
        </div>
      </div>
    </div>
  )
}

function StepReview({ provisional, scheduleCron, prefDiffs, creating }: any) {
  return (
    <div className="p-6 space-y-6">
      <div>
        <h4 className="text-sm font-semibold mb-1">Review & Create</h4>
        <p className="text-xs text-slate-500">Confirm the configuration below. Creating finalizes and schedules the topic.</p>
      </div>
      <div className="grid md:grid-cols-2 gap-4">
        <div className="space-y-2">
          <h5 className="text-xs uppercase tracking-wide text-slate-400 font-medium">Summary</h5>
          <div className="text-xs space-y-1 bg-slate-900/60 border border-slate-800 rounded p-3">
            <div><span className="text-slate-400">Name:</span> {provisional.name || '—'}</div>
            <div><span className="text-slate-400">Cron:</span> {scheduleCron}</div>
            <div><span className="text-slate-400">Preference Keys:</span> {Object.keys(provisional.preferences||{}).length}</div>
          </div>
        </div>
        <div className="space-y-2">
          <h5 className="text-xs uppercase tracking-wide text-slate-400 font-medium">Latest Changes</h5>
          <div className="bg-slate-900/60 border border-slate-800 rounded p-3 max-h-48 overflow-auto">
            <PreferenceDiff diffs={prefDiffs} />
          </div>
        </div>
      </div>
      <div className="space-y-2">
        <h5 className="text-xs uppercase tracking-wide text-slate-400 font-medium">Preferences JSON</h5>
        <pre className="text-xs bg-slate-900/60 border border-slate-800 rounded p-3 max-h-64 overflow-auto whitespace-pre-wrap">{JSON.stringify(provisional.preferences, null, 2)}</pre>
      </div>
      {creating && <div className="text-xs text-slate-400">Creating topic…</div>}
    </div>
  )
}

function computeDiff(oldObj: Record<string, any>, newObj: Record<string, any>): DiffEntry[] {
  const diffs: DiffEntry[] = []
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
  diffs.sort((x,y)=> x.type.localeCompare(y.type) || x.path.localeCompare(y.path))
  return diffs
}

function macroToCron(expr: string): string {
  switch(expr) {
    case '@hourly': return '0 * * * *'
    case '@daily': return '0 0 * * *'
    case '@weekly': return '0 0 * * 0'
    case '@monthly': return '0 0 1 * *'
    default: return expr
  }
}

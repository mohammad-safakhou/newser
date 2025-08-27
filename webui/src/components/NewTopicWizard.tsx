import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { api2 } from '../api'
import PreferenceDiff, { DiffEntry } from './PreferenceDiff'
import cronParser from 'cron-parser'

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

  const assistMut = useMutation({
    mutationFn: (message: string) => api2.assistChat({ message, name, preferences: provisional.preferences, schedule_cron: provisional.schedule_cron }),
    onSuccess: (resp, sent) => {
      setAssistConversation(prev => [...prev, { role: 'user', content: sent }, { role: 'assistant', content: resp.message }])
      // merge returned topic
      const t = resp.topic || {}
      const newPrefs = t.preferences || t.Preferences || provisional.preferences
      const newCron = t.cron_spec || t.CronSpec || provisional.schedule_cron
      setProvisional(p => ({ ...p, name: t.title || t.Title || name || p.name, preferences: newPrefs, schedule_cron: newCron }))
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
    mutationFn: () => api2.createTopicFull(provisional.name || name, provisional.preferences, scheduleCron),
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
    <div className="fixed inset-0 z-40 flex items-center justify-center p-4">
      <div className="absolute inset-0 bg-slate-950/80 backdrop-blur" onClick={onClose} />
      <div className="relative w-full max-w-3xl bg-slate-925 card p-0 flex flex-col max-h-[90vh]">
        <header className="px-6 py-4 border-b border-slate-800 flex items-center gap-4">
          <h3 className="text-base font-semibold tracking-wide">New Topic Wizard</h3>
          <span className="text-[11px] text-slate-500">Step {step+1} / 4</span>
          <div className="ml-auto flex gap-2">
            <button onClick={onClose} className="btn-secondary text-xs px-3">Close</button>
          </div>
        </header>
        <div className="flex-1 overflow-y-auto" ref={scrollRef}>
          {step === 0 && <StepIdea name={name} setName={setName} goalMsg={goalMsg} setGoalMsg={setGoalMsg} conversation={assistConversation} sendAssist={sendAssist} loading={assistMut.isPending} provisional={provisional} />}
          {step === 1 && <StepPreferences provisional={provisional} prefMessage={prefMessage} setPrefMessage={setPrefMessage} refine={refinePrefs} loading={prefsMut.isPending} diffs={prefDiffs} />}
          {step === 2 && <StepSchedule scheduleCron={scheduleCron} setScheduleCron={setScheduleCron} error={cronError} />}
          {step === 3 && <StepReview provisional={provisional} scheduleCron={scheduleCron} prefDiffs={prefDiffs} creating={createMut.isPending || finalizing} />}
        </div>
        <footer className="px-6 py-4 border-t border-slate-800 flex items-center gap-3">
          <div className="flex-1 text-[11px] text-slate-500 truncate">{createMut.isError && <span className="text-red-400">{(createMut.error as Error).message}</span>}</div>
          {step>0 && <button onClick={back} className="btn-secondary text-xs px-4">Back</button>}
          {step < 3 && <button onClick={advance} disabled={nextDisabled} className="btn text-xs px-5">Next</button>}
          {step === 3 && <button onClick={finalize} disabled={createMut.isPending || finalizing} className="btn text-xs px-5">{finalizing ? 'Creating…' : 'Create Topic'}</button>}
        </footer>
      </div>
    </div>
  )
}

  function StepIdea({ name, setName, goalMsg, setGoalMsg, conversation, sendAssist, loading, provisional }: {
    name: string
    setName: (v: string) => void
    goalMsg: string
    setGoalMsg: (v: string) => void
    conversation: ChatMsg[]
    sendAssist: () => void
    loading: boolean
    provisional: ProvisionalTopic
  }) {
  return (
    <div className="p-6 space-y-6">
      <div className="space-y-2">
        <label className="text-xs font-medium text-slate-400">Topic Name</label>
        <input className="input text-sm" value={name} onChange={e=>setName(e.target.value)} placeholder="e.g. Global Renewable Energy Trends" />
      </div>
      <div className="space-y-2">
        <label className="text-xs font-medium text-slate-400">Describe your goal / scope</label>
        <div className="flex gap-2">
          <input className="input text-xs flex-1" value={goalMsg} onChange={e=>setGoalMsg(e.target.value)} placeholder="What do you want this topic to cover?" />
          <button type="button" onClick={sendAssist} disabled={!goalMsg.trim() || loading} className="btn text-xs px-4">{loading ? '...' : 'Ask AI'}</button>
        </div>
        <div className="bg-slate-900/60 rounded border border-slate-800 p-3 h-40 overflow-y-auto text-[11px] space-y-2">
          {conversation.length === 0 && <div className="text-slate-500">AI suggestions will appear here.</div>}
          {conversation.map((m,i)=> <div key={i} className={m.role==='user' ? 'text-brand-300' : 'text-slate-300'}>{m.role==='user' ? 'You: ' : 'AI: '}{m.content}</div> )}
        </div>
      </div>
      <div className="text-[11px] text-slate-500 space-y-1">
        <div><span className="text-slate-400">Draft Name:</span> {provisional.name || '—'}</div>
        <div><span className="text-slate-400">Draft Cron:</span> {provisional.schedule_cron}</div>
        <div><span className="text-slate-400">Pref Keys:</span> {Object.keys(provisional.preferences||{}).length}</div>
      </div>
    </div>
  )
}

function StepPreferences({ provisional, prefMessage, setPrefMessage, refine, loading, diffs }: any) {
  return (
    <div className="p-6 space-y-6">
      <div>
        <h4 className="text-sm font-semibold mb-1">Refine Preferences</h4>
        <p className="text-[11px] text-slate-500">Ask for additions, constraints, or quality requirements. Each AI response updates the draft preferences.</p>
      </div>
      <div className="flex gap-2">
        <input className="input text-xs flex-1" value={prefMessage} onChange={e=>setPrefMessage(e.target.value)} placeholder="e.g. Add bias detection and focus on APAC markets" />
        <button type="button" onClick={refine} disabled={!prefMessage.trim() || loading} className="btn text-xs px-4">{loading ? '...' : 'Refine'}</button>
      </div>
      <div className="grid md:grid-cols-2 gap-4">
        <div className="space-y-2">
          <h5 className="text-[11px] uppercase tracking-wide text-slate-400 font-medium">Current Preferences (JSON)</h5>
          <pre className="text-[10px] bg-slate-900/60 border border-slate-800 rounded p-3 max-h-64 overflow-auto whitespace-pre-wrap">{JSON.stringify(provisional.preferences, null, 2) || '{}'}</pre>
        </div>
        <div className="space-y-2">
          <h5 className="text-[11px] uppercase tracking-wide text-slate-400 font-medium">Last Changes</h5>
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
        <p className="text-[11px] text-slate-500">Use a cron expression (standard 5-field) or macros like @daily. Preview of next run times shown.</p>
      </div>
      <div className="space-y-2">
        <label className="text-xs font-medium text-slate-400">Cron Expression</label>
        <input className="input text-xs" value={scheduleCron} onChange={e=>setScheduleCron(e.target.value)} placeholder="@daily or 0 9 * * *" />
        {error && <div className="text-[11px] text-red-400">{error}</div>}
        {!error && <div className="text-[10px] text-emerald-300/80">Valid expression</div>}
      </div>
      <div className="grid md:grid-cols-2 gap-4">
        <div className="bg-slate-900/60 border border-slate-800 rounded p-3 text-[11px] space-y-1">
          <p className="text-slate-400">Examples:</p>
          <ul className="list-disc pl-4 space-y-0.5">
            <li><code>@daily</code> – once per day</li>
            <li><code>0 */6 * * *</code> – every 6 hours</li>
            <li><code>15 9 * * MON-FRI</code> – 09:15 weekdays</li>
            <li><code>0 0 1 * *</code> – first day monthly</li>
          </ul>
        </div>
        <div className="bg-slate-900/60 border border-slate-800 rounded p-3 text-[11px] space-y-1">
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
        <p className="text-[11px] text-slate-500">Confirm the configuration below. Creating finalizes and schedules the topic.</p>
      </div>
      <div className="grid md:grid-cols-2 gap-4">
        <div className="space-y-2">
          <h5 className="text-[11px] uppercase tracking-wide text-slate-400 font-medium">Summary</h5>
          <div className="text-[11px] space-y-1 bg-slate-900/60 border border-slate-800 rounded p-3">
            <div><span className="text-slate-400">Name:</span> {provisional.name || '—'}</div>
            <div><span className="text-slate-400">Cron:</span> {scheduleCron}</div>
            <div><span className="text-slate-400">Preference Keys:</span> {Object.keys(provisional.preferences||{}).length}</div>
          </div>
        </div>
        <div className="space-y-2">
          <h5 className="text-[11px] uppercase tracking-wide text-slate-400 font-medium">Latest Changes</h5>
          <div className="bg-slate-900/60 border border-slate-800 rounded p-3 max-h-48 overflow-auto">
            <PreferenceDiff diffs={prefDiffs} />
          </div>
        </div>
      </div>
      <div className="space-y-2">
        <h5 className="text-[11px] uppercase tracking-wide text-slate-400 font-medium">Preferences JSON</h5>
        <pre className="text-[10px] bg-slate-900/60 border border-slate-800 rounded p-3 max-h-64 overflow-auto whitespace-pre-wrap">{JSON.stringify(provisional.preferences, null, 2)}</pre>
      </div>
      {creating && <div className="text-[11px] text-slate-400">Creating topic…</div>}
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

import React, { useState } from 'react'

export interface HighlightSource { title?: string; url?: string; origin?: string }
export interface HighlightItem {
  title?: string
  content?: string
  type?: string
  priority?: number
  is_pinned?: boolean
  sources?: HighlightSource[]
  created_at?: string
  expires_at?: string
}

interface Props { items: HighlightItem[] }

export default function HighlightsList({ items }: Props) {
  if (!items || items.length === 0) return <div className="text-[11px] text-slate-500">No highlights.</div>
  return (
    <ul className="space-y-2">
      {items.map((h, i) => <HighlightRow key={i} h={h} />)}
    </ul>
  )
}

function HighlightRow({ h }: { h: HighlightItem }) {
  const [open, setOpen] = useState(false)
  const badgeColor = h.type ? colorForType(h.type) : 'bg-slate-700 text-slate-200'
  const priorityColor = h.priority != null ? priorityClass(h.priority) : 'text-slate-400'
  return (
    <li className="border border-slate-800 rounded-md bg-slate-900/50 hover:border-slate-700 transition">
      <button type="button" onClick={()=>setOpen(o=>!o)} className="w-full text-left px-3 py-2 flex items-start gap-3">
        <div className={`px-2 py-0.5 rounded-full text-[10px] font-medium ${badgeColor}`}>{(h.type||'').toUpperCase() || 'HL'}</div>
        <div className="flex-1 min-w-0">
          <div className="text-[12px] font-medium text-slate-200 truncate flex items-center gap-2">
            {h.title || 'Untitled'}
            {h.is_pinned && <span className="text-[9px] px-1 py-0.5 rounded bg-amber-500/20 text-amber-300 border border-amber-400/30">PIN</span>}
            {h.priority != null && <span className={`text-[9px] px-1 py-0.5 rounded border ${priorityColor}`}>P{h.priority}</span>}
          </div>
          <div className="text-[10px] text-slate-500 line-clamp-2">{h.content || '—'}</div>
        </div>
        <div className="text-[10px] text-slate-500 pl-2">{open ? '−' : '+'}</div>
      </button>
      {open && (
        <div className="px-4 pb-4 -mt-1 space-y-3 text-[11px]">
          <div className="whitespace-pre-wrap text-slate-300">{h.content || '—'}</div>
          {Array.isArray(h.sources) && h.sources.length > 0 && (
            <div className="space-y-1">
              <div className="text-slate-400">Sources ({h.sources.length}):</div>
              <ul className="space-y-0.5 list-disc pl-5">
                {h.sources.map((s, i) => (
                  <li key={i} className="truncate">
                    {s.url ? <a href={s.url} target="_blank" rel="noreferrer" className="text-brand-300 hover:underline">{s.title || s.url}</a> : (s.title || s.origin || 'Source')}
                  </li>
                ))}
              </ul>
            </div>
          )}
          <div className="flex gap-4 text-[10px] text-slate-500">
            {h.created_at && <span>Created {formatDate(h.created_at)}</span>}
            {h.expires_at && <span>Expires {formatDate(h.expires_at)}</span>}
          </div>
        </div>
      )}
    </li>
  )
}

function colorForType(t: string): string {
  const key = t.toLowerCase()
  if (key.includes('risk')) return 'bg-red-600/30 text-red-300 border border-red-500/40'
  if (key.includes('opportunity')) return 'bg-emerald-600/30 text-emerald-300 border border-emerald-500/40'
  if (key.includes('trend')) return 'bg-indigo-600/30 text-indigo-300 border border-indigo-500/40'
  if (key.includes('insight')) return 'bg-cyan-600/30 text-cyan-200 border border-cyan-500/40'
  return 'bg-slate-700 text-slate-200'
}

function priorityClass(p: number): string {
  if (p <= 1) return 'bg-slate-700 text-slate-200 border-slate-600'
  if (p === 2) return 'bg-emerald-600/30 text-emerald-300 border-emerald-500/40'
  if (p === 3) return 'bg-amber-600/30 text-amber-300 border-amber-500/40'
  if (p >= 4) return 'bg-red-600/30 text-red-300 border-red-500/40'
  return 'bg-slate-700 text-slate-200 border-slate-600'
}

function formatDate(ts: string): string {
  try { return new Date(ts).toLocaleString() } catch { return ts }
}


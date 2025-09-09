import React from 'react'

export interface DiffEntry {
  path: string
  type: 'added' | 'removed' | 'changed'
  from?: any
  to?: any
}

function formatVal(v: any) {
  if (v === null || v === undefined) return '∅'
  if (typeof v === 'object') return JSON.stringify(v)
  return String(v)
}

export default function PreferenceDiff({ diffs }: { diffs: DiffEntry[] }) {
  if (!diffs.length) return <div className="text-xs text-slate-500">No changes.</div>
  return (
    <ul className="space-y-1 text-xs font-mono leading-snug">
      {diffs.map((d,i)=> (
        <li key={i} className="flex gap-2 items-start">
          <span className={
            d.type==='added' ? 'text-emerald-400' : d.type==='removed' ? 'text-red-400' : 'text-amber-400'
          }>{d.type === 'changed' ? '±' : d.type === 'added' ? '+' : '−'}</span>
          <div className="flex-1">
            <span className="text-slate-300">{d.path}</span>{' '}
            {d.type==='changed' && <><span className="text-slate-500">{formatVal(d.from)}</span> → <span className="text-slate-200">{formatVal(d.to)}</span></>}
            {d.type==='added' && <><span className="text-slate-500">=</span> <span className="text-slate-200">{formatVal(d.to)}</span></>}
            {d.type==='removed' && <><span className="text-slate-500">was</span> <span className="text-slate-200 line-through">{formatVal(d.from)}</span></>}
          </div>
        </li>
      ))}
    </ul>
  )
}

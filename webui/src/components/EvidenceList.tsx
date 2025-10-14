import React from 'react'
import type { Evidence, RunSource } from '../api'

interface EvidenceListProps {
  evidence: Evidence[]
  sources: RunSource[]
}

function buildSourceMap(sources: RunSource[]): Record<string, RunSource> {
  return sources.reduce<Record<string, RunSource>>((acc, src) => {
    if (src.id) acc[src.id] = src
    return acc
  }, {})
}

function formatSource(source: RunSource, fallback: string) {
  const label = source.title || source.summary || source.url || fallback
  if (source.url) {
    return (
      <a href={source.url} target="_blank" rel="noopener noreferrer" className="text-brand-300 hover:underline">
        {label}
      </a>
    )
  }
  return <span>{label}</span>
}

export default function EvidenceList({ evidence, sources }: EvidenceListProps) {
  if (!evidence || evidence.length === 0) {
    return <div className="text-xs text-slate-500">No evidence recorded.</div>
  }
  const sourceMap = buildSourceMap(sources || [])
  return (
    <div className="space-y-2" aria-live="polite">
      <div className="text-xs font-semibold uppercase tracking-wide text-slate-400">Evidence</div>
      <ul className="space-y-2">
        {evidence.map((item, idx) => {
          const key = item.id || `${idx}`
          const supportingSources = (item.source_ids || []).map((sid) => sourceMap[sid] || { id: sid })
          return (
            <li key={key} className="border border-slate-800 rounded bg-slate-900/40 p-3">
              <div className="text-xs text-slate-200 whitespace-pre-wrap leading-relaxed">{item.statement}</div>
              <div className="mt-2 flex flex-wrap items-center gap-2 text-[11px] text-slate-400">
                {item.category && <span className="px-2 py-0.5 rounded-full border border-blue-500/40 bg-blue-500/10 text-blue-200">{item.category}</span>}
                {typeof item.score === 'number' && item.score > 0 && (
                  <span className="px-2 py-0.5 rounded-full border border-emerald-500/40 bg-emerald-500/10 text-emerald-200">score {(item.score).toFixed(2)}</span>
                )}
              </div>
              {supportingSources.length > 0 && (
                <div className="mt-3 text-[11px] text-slate-400 space-y-1">
                  <div>Sources</div>
                  <ul className="list-disc pl-5 space-y-0.5">
                    {supportingSources.map((src, i) => (
                      <li key={src.id || `${key}-src-${i}`} className="truncate text-xs">
                        {formatSource(src, src.id || 'Source')}
                      </li>
                    ))}
                  </ul>
                </div>
              )}
            </li>
          )
        })}
      </ul>
    </div>
  )
}

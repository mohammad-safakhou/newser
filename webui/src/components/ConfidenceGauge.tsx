import React from 'react'

export default function ConfidenceGauge({ value }: { value: number | null }) {
  if (value == null || isNaN(value)) return <span className="text-[10px] text-slate-500">—</span>
  const pct = Math.max(0, Math.min(1, value))
  const deg = pct * 270 // 270deg arc
  const color = pct > 0.75 ? '#10b981' : pct > 0.5 ? '#3b82f6' : pct > 0.3 ? '#f59e0b' : '#ef4444'
  return (
    <div className="relative" title={`Confidence ${(pct*100).toFixed(1)}%`}>
      <div className="w-14 h-8 overflow-hidden">
        <div className="w-14 h-14 rounded-full border-2 border-slate-700 relative" style={{ background: `conic-gradient(${color} ${deg}deg, transparent 0)` }} />
      </div>
      <div className="absolute inset-0 flex items-end justify-center pb-0.5">
        <span className="text-[10px] font-medium text-slate-200">{(pct*100).toFixed(0)}%</span>
      </div>
    </div>
  )
}


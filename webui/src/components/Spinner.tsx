import React from 'react'

export default function Spinner({ size = 16, label = 'Loading' }: { size?: number; label?: string }) {
  const s = `${size}px`
  return (
    <span className="inline-flex items-center gap-2" role="status" aria-live="polite" aria-label={label}>
      <svg width={size} height={size} viewBox="0 0 24 24" className="animate-spin text-slate-400">
        <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" fill="none" />
        <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v4a4 4 0 00-4 4H4z" />
      </svg>
      <span className="text-xs text-slate-500">{label}…</span>
    </span>
  )
}


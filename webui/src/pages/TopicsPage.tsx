import React, { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api2, Topic } from '../api'
import NewTopicWizard from '../components/NewTopicWizard'
import Spinner from '../components/Spinner'
import { useSession } from '../session'

export default function TopicsPage() {
  const qc = useQueryClient()
  const nav = useNavigate()
  const { logout } = useSession()
  const [wizardOpen, setWizardOpen] = useState(false)
  const { data, isLoading, error } = useQuery<Topic[]>({
    queryKey: ['topics'],
    queryFn: api2.topics,
    select: (d) => Array.isArray(d) ? d : [],
  })
  const topics = Array.isArray(data) ? data : []

  return (
    <div className="h-full flex flex-col">
      <header className="px-6 py-4 border-b border-slate-800 flex items-center justify-between">
        <h2 className="text-lg font-semibold">Topics</h2>
        <div className="flex gap-2 items-center">
          <button onClick={()=>qc.invalidateQueries({ queryKey: ['topics'] })} className="btn-secondary text-xs">Refresh</button>
          <button onClick={()=>setWizardOpen(true)} className="btn text-sm">New Topic</button>
          <span className="mx-2 h-4 w-px bg-slate-700" aria-hidden />
          <button onClick={async()=>{ await logout(); nav('/login') }} className="text-xs text-slate-400 hover:text-slate-200">Logout</button>
        </div>
      </header>
      <div className="flex-1 overflow-y-auto px-6 py-6 space-y-4">
        {isLoading && <Spinner label="Loading topics" />}
        {error && <div className="text-sm text-red-400">{(error as Error).message}</div>}
        <ul className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {topics.map(t => (
            <li key={t.ID} className="card cursor-pointer group" onClick={()=>nav(`/topics/${t.ID}`)}>
              <div className="flex items-start justify-between">
                <p className="font-medium leading-tight group-hover:text-brand-400 transition">{t.Name}</p>
                <span className="badge text-xs">{t.ScheduleCron || '—'}</span>
              </div>
              <div className="mt-2 text-xs text-slate-500">Created topic</div>
            </li>
          ))}
          {topics.length === 0 && <div className="text-sm text-slate-500">No topics yet.</div>}
        </ul>
      </div>
      {wizardOpen && (
        <NewTopicWizard onClose={()=>setWizardOpen(false)} onCreated={()=>{ setWizardOpen(false); qc.invalidateQueries({ queryKey: ['topics'] }) }} />
      )}
    </div>
  )
}

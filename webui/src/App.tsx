import React from 'react'
import { Routes, Route, Navigate } from 'react-router-dom'
import AuthPage from './pages/AuthPage'
import TopicsPage from './pages/TopicsPage'
import TopicDetailPage from './pages/TopicDetailPage'
import { SessionProvider, useSession } from './session'
import { ToastProvider } from './components/Toasts'
import ErrorBoundary from './components/ErrorBoundary'

function Protected({ children }: { children: React.ReactNode }) {
  const { user, loading } = useSession()
  if (loading) return <div className="p-8 text-sm text-slate-400">Loading session…</div>
  if (!user) return <Navigate to="/login" replace />
  return <>{children}</>
}

export default function App() {
  return (
    <SessionProvider>
      <ToastProvider>
        <ErrorBoundary>
          <Routes>
            <Route path="/login" element={<AuthPage />} />
            <Route path="/topics" element={<Protected><TopicsPage /></Protected>} />
            <Route path="/topics/:id" element={<Protected><TopicDetailPage /></Protected>} />
            <Route path="/" element={<Navigate to="/topics" replace />} />
            <Route path="*" element={<div className="p-10 text-center text-slate-400">Not found</div>} />
          </Routes>
        </ErrorBoundary>
      </ToastProvider>
    </SessionProvider>
  )
}

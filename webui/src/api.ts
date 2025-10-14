export type Me = { user_id: string }
export type Topic = { ID: string; Name: string; ScheduleCron: string }
export type TemporalPolicy = {
  repeat_mode: string
  refresh_interval?: string
  dedup_window?: string
  freshness_threshold?: string
  metadata?: Record<string, any>
}
export type BudgetConfig = {
  max_cost?: number
  max_tokens?: number
  max_time_seconds?: number
  approval_threshold?: number
  require_approval: boolean
  metadata?: Record<string, any>
}
export type PendingApproval = {
  run_id: string
  estimated_cost: number
  threshold: number
  created_at: string
  requested_by: string
}
export type TopicDetail = {
  id: string
  name: string
  schedule_cron: string
  preferences: Record<string, any>
  temporal_policy?: TemporalPolicy | null
  budget?: BudgetConfig | null
  pending_budget?: PendingApproval | null
}
export type Run = { ID: string; Status: string; StartedAt: string; FinishedAt?: string; Error?: string }
export type ChatMessage = { role: 'user' | 'assistant'; content: string }
export type ChatLogMessage = { id: string; role: 'user'|'assistant'; content: string; created_at: string }

export type RunSource = {
  id: string
  title?: string
  url?: string
  type?: string
  credibility?: number
  published_at?: string
  summary?: string
}

export type Evidence = {
  id: string
  statement: string
  source_ids: string[]
  category?: string
  score?: number
  metadata?: Record<string, any>
}

export type RunResult = {
  id: string
  summary?: string
  detailed_report?: string
  confidence?: number
  sources?: RunSource[]
  highlights?: any[]
  conflicts?: any[]
  evidence?: Evidence[]
  metadata?: Record<string, any>
  created_at?: string
}

// Enhanced request with abort support
const API_BASE = (import.meta.env.VITE_API_BASE_URL || '').replace(/\/+$/, '')

export async function apiRequest<T = any>(path: string, options: RequestInit & { signal?: AbortSignal } = {}): Promise<T> {
  const url = /^https?:\/\//i.test(path)
    ? path
    : `${API_BASE}${path.startsWith('/') ? path : `/${path}`}`
  const token = typeof localStorage !== 'undefined' ? localStorage.getItem('auth_token') : null
  const res = await fetch(url, {
    credentials: 'include',
    headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}), ...(options.headers || {}) },
    ...options,
  })
  let data: any = null
  const ct = res.headers.get('content-type') || ''
  const text = await res.text()
  if (ct.includes('application/json') && text) {
    try { data = JSON.parse(text) } catch { data = text }
  } else { data = text || null }
  if (!res.ok) {
    const message = typeof data === 'string' ? data : data?.error || data?.message || `HTTP ${res.status}`
    throw new Error(message)
  }
  return data as T
}

export const api2 = {
  me: () => apiRequest<Me>('/api/me'),
  signup: (email: string, password: string) => apiRequest('/api/auth/signup', { method: 'POST', body: JSON.stringify({ email, password }) }),
  login: async (email: string, password: string) => {
    const res = await apiRequest<{ token?: string }>('/api/auth/login', { method: 'POST', body: JSON.stringify({ email, password }) })
    if (res && (res as any).token) {
      try { localStorage.setItem('auth_token', (res as any).token) } catch {}
    }
    return res
  },
  logout: async () => {
    try { await apiRequest('/api/auth/logout', { method: 'POST' }) } catch {}
    try { localStorage.removeItem('auth_token') } catch {}
  },
  topics: () => apiRequest<Topic[]>('/api/topics'),
  createTopic: (name: string) => apiRequest('/api/topics', { method: 'POST', body: JSON.stringify({ name, preferences: { bias_detection: true }, schedule_cron: '@daily' }) }),
  createTopicFull: (name: string, preferences: Record<string, any>, scheduleCron: string) => apiRequest('/api/topics', { method: 'POST', body: JSON.stringify({ name, preferences, schedule_cron: scheduleCron }) }),
  triggerRun: (id: string) => apiRequest(`/api/topics/${id}/trigger`, { method: 'POST' }),
  runs: (id: string) => apiRequest<Run[]>(`/api/topics/${id}/runs`),
  latestResult: (id: string) => apiRequest<RunResult>(`/api/topics/${id}/latest_result`),
  runResult: (topicId: string, runId: string) => apiRequest<RunResult>(`/api/topics/${topicId}/runs/${runId}/result`),
  runMarkdown: (topicId: string, runId: string) => apiRequest<string>(`/api/topics/${topicId}/runs/${runId}/markdown`),
  expand: (topicId: string, runId: string, payload: { highlight_index?: number; source_url?: string; focus?: string }) =>
    apiRequest<{ markdown: string }>(`/api/topics/${topicId}/runs/${runId}/expand`, { method: 'POST', body: JSON.stringify(payload) }),
  expandAll: (topicId: string, runId: string, payload: { group_by?: 'type'|'domain'|'none'|'taxonomy'; focus?: string }) =>
    apiRequest<{ markdown: string }>(`/api/topics/${topicId}/runs/${runId}/expand_all`, { method: 'POST', body: JSON.stringify(payload) }),
  chat: (id: string, message: string) => apiRequest<{ message: string; topic: any }>(`/api/topics/${id}/chat`, { method: 'POST', body: JSON.stringify({ message }) }),
  chatHistory: (id: string, opts: { limit?: number; before?: string } = {}) => {
    const p = new URLSearchParams()
    if (opts.limit) p.set('limit', String(opts.limit))
    if (opts.before) p.set('before', opts.before)
    const qs = p.toString()
    return apiRequest<ChatLogMessage[]>(`/api/topics/${id}/chat${qs ? `?${qs}` : ''}`)
  },
  getTopic: (id: string) => apiRequest<TopicDetail>(`/api/topics/${id}`),
  updateTopicName: (id: string, name: string) => apiRequest(`/api/topics/${id}`, { method: 'PATCH', body: JSON.stringify({ name }) }),
  updateTopicPrefs: (id: string, preferences: Record<string, any>, scheduleCron?: string) => apiRequest(`/api/topics/${id}/preferences`, { method: 'PATCH', body: JSON.stringify({ preferences, ...(scheduleCron ? { schedule_cron: scheduleCron } : {}) }) }),
  updateTopicBudget: (
    id: string,
    payload: { max_cost?: number; max_tokens?: number; max_time_seconds?: number; approval_threshold?: number; require_approval?: boolean; metadata?: Record<string, any> }
  ) => apiRequest(`/api/topics/${id}/budget`, { method: 'PUT', body: JSON.stringify(payload) }),
  decideBudget: (
    topicId: string,
    runId: string,
    payload: { approved: boolean; reason?: string }
  ) => apiRequest(`/api/topics/${topicId}/runs/${runId}/budget_decision`, { method: 'POST', body: JSON.stringify(payload) }),
  updateTopicPolicy: (
    id: string,
    payload: { repeat_mode: string; refresh_interval?: string; dedup_window?: string; freshness_threshold?: string; metadata?: Record<string, any> }
  ) => apiRequest(`/api/topics/${id}/policy`, { method: 'PUT', body: JSON.stringify(payload) }),
  assistChat: (payload: { message: string; name?: string; preferences?: Record<string, any>; schedule_cron?: string }) => apiRequest<{ message: string; topic: any }>(`/api/topics/assist/chat`, { method: 'POST', body: JSON.stringify(payload) }),
}

export function formatDate(ts?: string) {
  if (!ts) return '—'
  try { return new Date(ts).toLocaleString() } catch { return ts }
}

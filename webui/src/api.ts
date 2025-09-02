export type Me = { user_id: string }
export type Topic = { ID: string; Name: string; ScheduleCron: string }
export type Run = { ID: string; Status: string; StartedAt: string; FinishedAt?: string; Error?: string }
export type ChatMessage = { role: 'user' | 'assistant'; content: string }

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
  latestResult: (id: string) => apiRequest<any>(`/api/topics/${id}/latest_result`),
  chat: (id: string, message: string) => apiRequest<{ message: string; topic: any }>(`/api/topics/${id}/chat`, { method: 'POST', body: JSON.stringify({ message }) }),
  getTopic: (id: string) => apiRequest<any>(`/api/topics/${id}`),
  assistChat: (payload: { message: string; name?: string; preferences?: Record<string, any>; schedule_cron?: string }) => apiRequest<{ message: string; topic: any }>(`/api/topics/assist/chat`, { method: 'POST', body: JSON.stringify(payload) }),
}

export function formatDate(ts?: string) {
  if (!ts) return '—'
  try { return new Date(ts).toLocaleString() } catch { return ts }
}

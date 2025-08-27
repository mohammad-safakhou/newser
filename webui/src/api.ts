export type Me = { user_id: string }
export type Topic = { ID: string; Name: string; ScheduleCron: string }
export type Run = { ID: string; Status: string; StartedAt: string; FinishedAt?: string; Error?: string }
export type ChatMessage = { role: 'user' | 'assistant'; content: string }

// Enhanced request with abort support
export async function apiRequest<T = any>(path: string, options: RequestInit & { signal?: AbortSignal } = {}): Promise<T> {
  const res = await fetch(path, {
    credentials: 'include',
    headers: { 'Content-Type': 'application/json', ...(options.headers || {}) },
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
  login: (email: string, password: string) => apiRequest('/api/auth/login', { method: 'POST', body: JSON.stringify({ email, password }) }),
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

export type Me = { user_id: string }
export type Topic = { ID: string; Name: string; ScheduleCron: string }
export type Run = { ID: string; Status: string; StartedAt: string; FinishedAt?: string; Error?: string }

type Json = Record<string, unknown> | null

async function request(path: string, options: RequestInit = {}): Promise<Json> {
  const res = await fetch(path, {
    credentials: 'include',
    headers: { 'Content-Type': 'application/json', ...(options.headers || {}) },
    ...options,
  })

  const text = await res.text()
  const isJson = res.headers.get('content-type')?.includes('application/json')
  const data = isJson && text ? JSON.parse(text) : text || null

  if (!res.ok) {
    const message = typeof data === 'string' ? data : (data && (data.error || data.message)) || 'Request failed'
    throw new Error(message)
  }

  return data as Json
}

export const api = {
  me: () => request('/api/me') as Promise<Me>,
  signup: (email: string, password: string) => request('/api/auth/signup', { method: 'POST', body: JSON.stringify({ email, password }) }),
  login: (email: string, password: string) => request('/api/auth/login', { method: 'POST', body: JSON.stringify({ email, password }) }),
  topics: () => request('/api/topics') as Promise<Topic[]>,
  createTopic: (name: string) => request('/api/topics', { method: 'POST', body: JSON.stringify({ name, preferences: { bias_detection: true }, schedule_cron: '@daily' }) }),
  trigger: (id: string) => request(`/api/topics/${id}/trigger`, { method: 'POST' }),
  runs: (id: string) => request(`/api/topics/${id}/runs`) as Promise<Run[]>,
  latestResult: (id: string) => request(`/api/topics/${id}/latest_result`) as Promise<any>,
  chat: (id: string, message: string) => request(`/api/topics/${id}/chat`, { method: 'POST', body: JSON.stringify({ message }) }) as Promise<{ message: string, topic: any }>,
}



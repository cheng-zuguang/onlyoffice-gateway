const API_BASE = '/admin/api'

let authToken: string | null = localStorage.getItem('token')

export function setToken(token: string) {
  authToken = token
  localStorage.setItem('token', token)
}

export function clearToken() {
  authToken = null
  localStorage.removeItem('token')
}

export function getToken(): string | null {
  return authToken
}

async function request(
  method: string,
  path: string,
  body?: unknown,
): Promise<Response> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
  }
  if (authToken) {
    headers['Authorization'] = `Bearer ${authToken}`
  }
  const res = await fetch(`${API_BASE}${path}`, {
    method,
    headers,
    body: body ? JSON.stringify(body) : undefined,
  })
  if (res.status === 401 && path !== '/login') {
    clearToken()
    window.location.href = '/admin/login'
  }
  return res
}

export interface Service {
  id: string
  public_key: string
  allowed_webhook_domains: string[]
}

export interface LoginResponse {
  token: string
  token_type: string
}

export interface ApiError {
  error: string
}

export async function login(username: string, password: string): Promise<LoginResponse> {
  const res = await request('POST', '/login', { username, password })
  if (!res.ok) {
    const err: ApiError = await res.json()
    throw new Error(err.error || 'Login failed')
  }
  return res.json()
}

export async function listServices(): Promise<Service[]> {
  const res = await request('GET', '/services')
  if (!res.ok) throw new Error('Failed to list services')
  return res.json()
}

export async function createService(svc: {
  id: string
  public_key: string
  allowed_webhook_domains: string[]
}): Promise<Service> {
  const res = await request('POST', '/services', svc)
  if (!res.ok) {
    const err: ApiError = await res.json()
    throw new Error(err.error || 'Failed to create service')
  }
  return res.json()
}

export async function deleteService(id: string): Promise<void> {
  const res = await request('DELETE', `/services/${encodeURIComponent(id)}`)
  if (!res.ok) {
    const err: ApiError = await res.json()
    throw new Error(err.error || 'Failed to delete service')
  }
}

export async function updateService(svc: {
  id: string
  public_key: string
  allowed_webhook_domains: string[]
}): Promise<Service> {
  const res = await request('PUT', `/services/${encodeURIComponent(svc.id)}`, svc)
  if (!res.ok) {
    const err: ApiError = await res.json()
    throw new Error(err.error || 'Failed to update service')
  }
  return res.json()
}

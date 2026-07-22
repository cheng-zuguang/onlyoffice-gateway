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
  webhook_secret_configured: boolean
  webhook_secret_last_rotated_at?: string
  webhook_secret_pending: boolean
  webhook_secret_rollback_available?: boolean
}

export interface ServiceCredentialResponse {
  service: Service
  credentials: {
    webhook_secret: string
  }
}

export interface RotatedServiceCredentialResponse {
  service_id: string
  credentials: {
    webhook_secret: string
  }
}

export interface LoginResponse {
  token: string
  token_type: string
}

export interface ApiError {
  error: string
}

export interface Attachment { document_id: string; service_id: string; external_id?: string; file_name: string; document_type: string; created_at: string; expires_at: string; is_edited: boolean; direct_source: boolean; source_host?: string }
export interface AuditEvent { time: string; level: string; type: string; document_id?: string; request_id?: string; service_id?: string }
export interface Page<T> { items: T[]; next_cursor: string }

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
}): Promise<ServiceCredentialResponse> {
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

export async function rotateWebhookSecret(id: string): Promise<RotatedServiceCredentialResponse> {
  const res = await request('POST', `/services/${encodeURIComponent(id)}/webhook-secret/rotate`)
  if (!res.ok) {
    const err: ApiError = await res.json()
    throw new Error(err.error || '生成待切换 Webhook 凭证失败')
  }
  return res.json()
}

export async function activateWebhookSecret(id: string): Promise<Service> {
  const res = await request('POST', `/services/${encodeURIComponent(id)}/webhook-secret/activate`)
  if (!res.ok) {
    const err: ApiError = await res.json()
    throw new Error(err.error || '激活 Webhook 凭证失败')
  }
  return res.json()
}

export async function rollbackWebhookSecret(id: string): Promise<Service> {
  const res = await request('POST', `/services/${encodeURIComponent(id)}/webhook-secret/rollback`)
  if (!res.ok) {
    const err: ApiError = await res.json()
    throw new Error(err.error || '回滚 Webhook 凭证失败')
  }
  return res.json()
}

export async function listAttachments(cursor = ''): Promise<Page<Attachment>> { const query = new URLSearchParams({ limit: '50' }); if (cursor) query.set('cursor', cursor); const res = await request('GET', `/attachments?${query}`); if (!res.ok) throw new Error('加载临时附件失败'); return res.json() }
export async function deleteAttachment(id: string): Promise<void> { const res = await request('DELETE', `/attachments/${encodeURIComponent(id)}`, { confirm: true }); if (!res.ok) throw new Error('删除临时附件失败') }
export async function extendAttachmentTTL(id: string, hours: number): Promise<void> { const res = await request('POST', `/attachments/${encodeURIComponent(id)}/extend-ttl`, { hours }); if (!res.ok) throw new Error('延长有效期失败') }
export async function cleanupAttachments(): Promise<number> { const res = await request('POST', '/attachments/cleanup', {}); if (!res.ok) throw new Error('清理过期附件失败'); return (await res.json()).cleaned }
export async function downloadAttachment(id: string): Promise<{ blob: Blob; fileName: string }> { const res = await request('GET', `/attachments/${encodeURIComponent(id)}/download`); if (!res.ok) { const err: ApiError = await res.json(); throw new Error(err.error || '下载临时附件失败') }; const match = res.headers.get('Content-Disposition')?.match(/filename="?([^";]+)"?/i); return { blob: await res.blob(), fileName: match?.[1] || id } }
export async function listAuditEvents(cursor = ''): Promise<Page<AuditEvent>> { const query = new URLSearchParams({ limit: '50' }); if (cursor) query.set('cursor', cursor); const res = await request('GET', `/logs?${query}`); if (!res.ok) throw new Error('加载运行日志失败'); return res.json() }

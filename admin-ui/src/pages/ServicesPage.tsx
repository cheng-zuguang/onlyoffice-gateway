import { useEffect, useState } from 'react'
import { Button } from '../components/ui/button'
import { Input } from '../components/ui/input'
import { Label } from '../components/ui/label'
import { Textarea } from '../components/ui/textarea'
import { ConfirmDialog } from '../components/ui/confirm-dialog'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '../components/ui/dialog'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../components/ui/table'
import { Tooltip, TooltipContent, TooltipTrigger } from '../components/ui/tooltip'
import { listServices, createService, updateService, deleteService, rotateWebhookSecret, activateWebhookSecret, rollbackWebhookSecret, type Service } from '../lib/api'
import { Plus, Trash2, Server, Pencil, RefreshCw, Copy, KeyRound, BadgeCheck, RotateCcw } from 'lucide-react'
import { cn } from '../lib/utils'

export default function ServicesPage() {
  const [services, setServices] = useState<Service[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [showForm, setShowForm] = useState(false)
  const [editingSvc, setEditingSvc] = useState<Service | null>(null)
  const [formData, setFormData] = useState({ id: '', public_key: '', domains: '' })
  const [submitting, setSubmitting] = useState(false)
  const [formError, setFormError] = useState('')
  const [oneTimeCredential, setOneTimeCredential] = useState<{ serviceId: string; secret: string; pending: boolean } | null>(null)
  const [rotating, setRotating] = useState<string | null>(null)
  const [credentialAction, setCredentialAction] = useState<{
    open: boolean
    serviceId: string
    action: 'activate' | 'rollback'
  }>({ open: false, serviceId: '', action: 'activate' })
  const [deleting, setDeleting] = useState<string | null>(null)
  const [confirmDialog, setConfirmDialog] = useState<{
    open: boolean
    id: string
    name: string
  }>({ open: false, id: '', name: '' })

  const fetchServices = async () => {
    try {
      const data = await listServices()
      setServices(data)
    } catch {
      setError('加载服务列表失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchServices()
  }, [])

  const openCreateForm = () => {
    setEditingSvc(null)
    setFormData({ id: '', public_key: '', domains: '' })
    setFormError('')
    setShowForm(true)
  }

  const openEditForm = (svc: Service) => {
    setEditingSvc(svc)
    setFormData({
      id: svc.id,
      public_key: svc.public_key,
      domains: (svc.allowed_webhook_domains || []).join('\n'),
    })
    setFormError('')
    setShowForm(true)
  }

  const closeForm = () => {
    setShowForm(false)
    setEditingSvc(null)
  }

  const handleSubmit = async () => {
    setFormError('')
    if (!formData.public_key.trim()) {
      setFormError('Public key is required')
      return
    }
    setSubmitting(true)
    try {
      const payload = {
        id: editingSvc ? editingSvc.id : formData.id.trim(),
        public_key: formData.public_key.trim(),
        allowed_webhook_domains: formData.domains
          .split(/[,;\n]/)
          .map((d) => d.trim())
          .filter(Boolean),
      }
      if (editingSvc) {
        await updateService(payload)
      } else {
        if (!payload.id) {
          setFormError('服务标识不能为空')
          setSubmitting(false)
          return
        }
        const result = await createService(payload)
        setOneTimeCredential({
          serviceId: result.service.id,
          secret: result.credentials.webhook_secret,
          pending: false,
        })
      }
      closeForm()
      await fetchServices()
    } catch (err) {
      setFormError(err instanceof Error ? err.message : '保存服务失败')
    } finally {
      setSubmitting(false)
    }
  }

  const promptDelete = (svc: Service) => {
    setConfirmDialog({ open: true, id: svc.id, name: svc.id })
  }

  const handleRotate = async (svc: Service) => {
    setRotating(svc.id)
    setError('')
    try {
      const result = await rotateWebhookSecret(svc.id)
      setOneTimeCredential({
        serviceId: result.service_id,
        secret: result.credentials.webhook_secret,
        pending: true,
      })
      await fetchServices()
    } catch (err) {
      setError(err instanceof Error ? err.message : '生成待切换 Webhook 凭证失败')
    } finally {
      setRotating(null)
    }
  }

  const handleCredentialActionConfirm = async () => {
    const { serviceId, action } = credentialAction
    setCredentialAction({ open: false, serviceId: '', action: 'activate' })
    setError('')
    try {
      if (action === 'activate') {
        await activateWebhookSecret(serviceId)
      } else {
        await rollbackWebhookSecret(serviceId)
      }
      await fetchServices()
    } catch (err) {
      setError(err instanceof Error ? err.message : '更新 Webhook 凭证失败')
    }
  }

  const handleDeleteConfirm = async () => {
    const id = confirmDialog.id
    setConfirmDialog({ open: false, id: '', name: '' })
    setDeleting(id)
    try {
      await deleteService(id)
      await fetchServices()
    } catch (err) {
      setError(err instanceof Error ? err.message : '删除服务失败')
    } finally {
      setDeleting(null)
    }
  }

  return (
    <>
      {/* Delete confirmation dialog */}
      <ConfirmDialog
        open={confirmDialog.open}
        title="删除服务"
        message={`确定删除“${confirmDialog.name}”吗？此操作不可撤销。`}
        confirmLabel="删除"
        variant="destructive"
        onConfirm={handleDeleteConfirm}
        onCancel={() => setConfirmDialog({ open: false, id: '', name: '' })}
      />
      <ConfirmDialog
        open={credentialAction.open}
        title={credentialAction.action === 'activate' ? '激活待切换 Webhook 凭证' : '回滚 Webhook 凭证'}
        message={credentialAction.action === 'activate'
          ? '确认业务服务已配置刚生成的新凭证并重启。激活后 Gateway 将立即使用新凭证签名。'
          : '确认回滚到上一个 Webhook 凭证。业务服务必须仍保留旧凭证，否则回调会认证失败。'}
        confirmLabel={credentialAction.action === 'activate' ? '确认激活' : '确认回滚'}
        variant={credentialAction.action === 'activate' ? 'default' : 'destructive'}
        onConfirm={handleCredentialActionConfirm}
        onCancel={() => setCredentialAction({ open: false, serviceId: '', action: 'activate' })}
      />

      <Dialog
        open={oneTimeCredential !== null}
        onOpenChange={(open) => {
          if (!open) setOneTimeCredential(null)
        }}
      >
        <DialogContent showCloseButton={false}>
          <DialogHeader><DialogTitle>请立即保存 Webhook 凭证</DialogTitle></DialogHeader>
          <div className="space-y-3">
            <p className="text-sm text-muted-foreground">
              服务“{oneTimeCredential?.serviceId}”的凭证只展示这一次，关闭后无法再次查看。
            </p>
            {oneTimeCredential?.pending && (
              <p className="rounded-md bg-amber-500/10 px-3 py-2 text-sm text-amber-700">
                该凭证尚未激活。请先配置到业务服务并重启，再执行激活。
              </p>
            )}
            <div className="flex items-center gap-2 rounded-md border bg-muted/40 p-3">
              <code className="min-w-0 flex-1 break-all text-sm">{oneTimeCredential?.secret}</code>
              <Button
                type="button"
                variant="outline"
                size="icon"
                aria-label="复制 Webhook 凭证"
                onClick={() => oneTimeCredential && navigator.clipboard.writeText(oneTimeCredential.secret)}
              >
                <Copy className="h-4 w-4" />
              </Button>
            </div>
          </div>
          <DialogFooter>
            <Button onClick={() => setOneTimeCredential(null)}>我已保存，关闭</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Form modal — outside space-y-6 so fixed positioning isn't broken */}
      <Dialog open={showForm} onOpenChange={(open) => !open && closeForm()}>
        <DialogContent>
          <DialogHeader><DialogTitle>{editingSvc ? `编辑“${editingSvc.id}”` : '新增服务'}</DialogTitle></DialogHeader>
          <div className="min-w-0 space-y-4">

            {!editingSvc && (
              <div className="space-y-2">
                <Label htmlFor="svc-id">服务标识</Label>
                <Input
                  id="svc-id"
                  value={formData.id}
                  onChange={(e) => setFormData({ ...formData, id: e.target.value })}
                  placeholder="e.g. my-app"
                />
              </div>
            )}

            <div className="space-y-2">
              <Label htmlFor="svc-key">RSA Public Key (PEM)</Label>
              <Textarea
                id="svc-key"
                rows={20}
                value={formData.public_key}
                onChange={(e) => setFormData({ ...formData, public_key: e.target.value })}
                placeholder="-----BEGIN PUBLIC KEY-----
..."
              />
            </div>

            <div className="space-y-2">
              <Label htmlFor="svc-domains">
                Allowed Webhook Domains
                <span className="text-muted-foreground font-normal ml-1">
                  (one per line, or comma/semicolon separated)
                </span>
              </Label>
              <Textarea
                id="svc-domains"
                rows={6}
                value={formData.domains}
                onChange={(e) => setFormData({ ...formData, domains: e.target.value })}
                placeholder="example.com"
                className="font-sans"
              />
            </div>

            {formError && (
              <div className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
                {formError}
              </div>
            )}

            <DialogFooter>
              <Button variant="outline" onClick={closeForm}>取消</Button>
              <Button onClick={handleSubmit} disabled={submitting}>
                {submitting ? '保存中...' : editingSvc ? '保存修改' : '新增服务'}
              </Button>
            </DialogFooter>
          </div>
        </DialogContent>
      </Dialog>

      <div className="flex h-full min-h-0 w-full flex-col gap-6">
        <div className="flex items-center justify-between">
          <div>
            <h2 className="text-lg font-semibold">服务管理</h2>
            <p className="text-sm text-muted-foreground">
              管理获授权使用此 Gateway 的业务服务
            </p>
          </div>
          <Button onClick={openCreateForm} size="sm">
            <Plus className="h-4 w-4" />
            新增服务
          </Button>
        </div>

        {error && (
          <div className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
            {error}
            <Tooltip>
              <TooltipTrigger asChild>
                <Button variant="ghost" size="icon" className="ml-1 size-7" aria-label="重试加载" onClick={() => { setError(''); fetchServices() }}>
                  <RefreshCw className="h-4 w-4" />
                </Button>
              </TooltipTrigger>
              <TooltipContent>重试加载</TooltipContent>
            </Tooltip>
          </div>
        )}

        {loading ? (
          <div className="text-sm text-muted-foreground">加载中...</div>
        ) : services.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-16 text-center border rounded-lg">
            <Server className="h-10 w-10 text-muted-foreground mb-3" />
            <p className="text-sm font-medium">尚未配置服务</p>
            <p className="text-xs text-muted-foreground mt-1">
              新增服务后即可开始使用 Gateway
            </p>
          </div>
        ) : (
          <div className="min-h-[200px] flex-1 overflow-auto rounded-lg border">
            <Table>
              <TableHeader><TableRow>
                  <TableHead>服务标识</TableHead><TableHead>Webhook 域名</TableHead><TableHead className="text-right">操作</TableHead>
              </TableRow></TableHeader>
              <TableBody>
                {services.map((svc) => (
                  <TableRow key={svc.id}>
                    <TableCell className="font-medium">{svc.id}</TableCell>
                    <TableCell>
                      {svc.allowed_webhook_domains && svc.allowed_webhook_domains.length > 0 ? (
                        <div className="flex flex-wrap gap-1">
                          {svc.allowed_webhook_domains.map((d) => (
                            <span
                              key={d}
                              className="inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium"
                            >
                              {d}
                            </span>
                          ))}
                        </div>
                      ) : (
                        <span className="text-muted-foreground">—</span>
                      )}
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex items-center justify-end gap-1">
                        {svc.webhook_secret_pending && (
                          <Tooltip>
                            <TooltipTrigger asChild>
                              <Button
                                variant="ghost"
                                size="icon"
                                aria-label="激活待切换凭证"
                                onClick={() => setCredentialAction({ open: true, serviceId: svc.id, action: 'activate' })}
                              >
                                <BadgeCheck className="h-4 w-4" />
                              </Button>
                            </TooltipTrigger>
                            <TooltipContent>激活待切换凭证</TooltipContent>
                          </Tooltip>
                        )}
                        {svc.webhook_secret_rollback_available && (
                          <Tooltip>
                            <TooltipTrigger asChild>
                              <Button
                                variant="ghost"
                                size="icon"
                                aria-label="回滚 Webhook 凭证"
                                onClick={() => setCredentialAction({ open: true, serviceId: svc.id, action: 'rollback' })}
                              >
                                <RotateCcw className="h-4 w-4 text-destructive" />
                              </Button>
                            </TooltipTrigger>
                            <TooltipContent>在十分钟窗口内回滚凭证</TooltipContent>
                          </Tooltip>
                        )}
                        {!svc.webhook_secret_pending && (
                          <Tooltip>
                            <TooltipTrigger asChild>
                              <Button
                                variant="ghost"
                                size="icon"
                                aria-label="生成待切换凭证"
                                onClick={() => handleRotate(svc)}
                                disabled={rotating === svc.id}
                              >
                                <KeyRound className={cn('h-4 w-4', rotating === svc.id && 'animate-pulse')} />
                              </Button>
                            </TooltipTrigger>
                            <TooltipContent>生成待切换凭证</TooltipContent>
                          </Tooltip>
                        )}
                        <Tooltip>
                          <TooltipTrigger asChild>
                            <Button
                              variant="ghost"
                              size="icon"
                              aria-label="编辑服务"
                              onClick={() => openEditForm(svc)}
                            >
                              <Pencil className="h-4 w-4" />
                            </Button>
                          </TooltipTrigger>
                          <TooltipContent>编辑服务</TooltipContent>
                        </Tooltip>
                        <Tooltip>
                          <TooltipTrigger asChild>
                            <Button
                              variant="ghost"
                              size="icon"
                              aria-label="删除服务"
                              onClick={() => promptDelete(svc)}
                              disabled={deleting === svc.id}
                            >
                              <Trash2
                                className={cn(
                                  'h-4 w-4',
                                  deleting === svc.id ? 'animate-pulse text-muted-foreground' : 'text-destructive',
                                )}
                              />
                            </Button>
                          </TooltipTrigger>
                          <TooltipContent>删除服务</TooltipContent>
                        </Tooltip>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </div>
    </>
  )
}

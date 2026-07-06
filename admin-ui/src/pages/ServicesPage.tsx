import { useEffect, useState } from 'react'
import { Button } from '../components/ui/button'
import { Input } from '../components/ui/input'
import { Label } from '../components/ui/label'
import { Textarea } from '../components/ui/textarea'
import { ConfirmDialog } from '../components/ui/confirm-dialog'
import { listServices, createService, updateService, deleteService, type Service } from '../lib/api'
import { Plus, Trash2, Server, X, Pencil } from 'lucide-react'
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
      setError('Failed to load services')
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
          setFormError('Service ID is required')
          setSubmitting(false)
          return
        }
        await createService(payload)
      }
      closeForm()
      await fetchServices()
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Failed to save')
    } finally {
      setSubmitting(false)
    }
  }

  const promptDelete = (svc: Service) => {
    setConfirmDialog({ open: true, id: svc.id, name: svc.id })
  }

  const handleDeleteConfirm = async () => {
    const id = confirmDialog.id
    setConfirmDialog({ open: false, id: '', name: '' })
    setDeleting(id)
    try {
      await deleteService(id)
      await fetchServices()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete')
    } finally {
      setDeleting(null)
    }
  }

  return (
    <>
      {/* Delete confirmation dialog */}
      <ConfirmDialog
        open={confirmDialog.open}
        title="Delete Service"
        message={`Delete "${confirmDialog.name}"? This cannot be undone.`}
        confirmLabel="Delete"
        variant="destructive"
        onConfirm={handleDeleteConfirm}
        onCancel={() => setConfirmDialog({ open: false, id: '', name: '' })}
      />

      {/* Form modal — outside space-y-6 so fixed positioning isn't broken */}
      {showForm && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
          <div className="w-full max-w-lg rounded-lg border bg-card p-6 shadow-lg space-y-4">
            <div className="flex items-center justify-between">
              <h3 className="text-base font-semibold">
                {editingSvc ? `Edit "${editingSvc.id}"` : 'Add Service'}
              </h3>
              <button onClick={closeForm} className="rounded-md p-1 hover:bg-accent">
                <X className="h-4 w-4" />
              </button>
            </div>

            {!editingSvc && (
              <div className="space-y-2">
                <Label htmlFor="svc-id">Service ID</Label>
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
                rows={6}
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
                rows={3}
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

            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={closeForm}>Cancel</Button>
              <Button onClick={handleSubmit} disabled={submitting}>
                {submitting ? 'Saving...' : editingSvc ? 'Save Changes' : 'Create Service'}
              </Button>
            </div>
          </div>
        </div>
      )}

      <div className="space-y-6">
        <div className="flex items-center justify-between">
          <div>
            <h2 className="text-lg font-semibold">Services</h2>
            <p className="text-sm text-muted-foreground">
              Manage services authorized to use this gateway
            </p>
          </div>
          <Button onClick={openCreateForm} size="sm">
            <Plus className="h-4 w-4" />
            Add Service
          </Button>
        </div>

        {error && (
          <div className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
            {error}
            <button className="ml-2 underline" onClick={() => { setError(''); fetchServices() }}>
              Retry
            </button>
          </div>
        )}

        {loading ? (
          <div className="text-sm text-muted-foreground">Loading...</div>
        ) : services.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-16 text-center border rounded-lg">
            <Server className="h-10 w-10 text-muted-foreground mb-3" />
            <p className="text-sm font-medium">No services configured</p>
            <p className="text-xs text-muted-foreground mt-1">
              Add a service to start using the gateway
            </p>
          </div>
        ) : (
          <div className="border rounded-lg overflow-hidden">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b bg-muted/50">
                  <th className="px-4 py-3 text-left font-medium">Service ID</th>
                  <th className="px-4 py-3 text-left font-medium">Webhook Domains</th>
                  <th className="px-4 py-3 text-right font-medium w-24">Actions</th>
                </tr>
              </thead>
              <tbody>
                {services.map((svc) => (
                  <tr key={svc.id} className="border-b last:border-0 hover:bg-muted/30">
                    <td className="px-4 py-3 font-medium">{svc.id}</td>
                    <td className="px-4 py-3">
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
                    </td>
                    <td className="px-4 py-3 text-right">
                      <div className="flex items-center justify-end gap-1">
                        <Button
                          variant="ghost"
                          size="icon"
                          onClick={() => openEditForm(svc)}
                        >
                          <Pencil className="h-4 w-4" />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
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
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </>
  )
}

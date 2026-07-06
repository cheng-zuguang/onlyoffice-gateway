import { Button } from './button'
import { X } from 'lucide-react'

interface ConfirmDialogProps {
  open: boolean
  title: string
  message: string
  confirmLabel?: string
  variant?: 'default' | 'destructive'
  onConfirm: () => void
  onCancel: () => void
}

export function ConfirmDialog({
  open,
  title,
  message,
  confirmLabel = 'Delete',
  variant = 'destructive',
  onConfirm,
  onCancel,
}: ConfirmDialogProps) {
  if (!open) return null

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
      <div className="w-full max-w-md rounded-lg border bg-card p-6 shadow-lg space-y-4">
        <div className="flex items-center justify-between">
          <h3 className="text-base font-semibold">{title}</h3>
          <button onClick={onCancel} className="rounded-md p-1 hover:bg-accent">
            <X className="h-4 w-4" />
          </button>
        </div>
        <p className="text-sm text-muted-foreground">{message}</p>
        <div className="flex justify-end gap-2">
          <Button variant="outline" onClick={onCancel}>
            Cancel
          </Button>
          <Button variant={variant} onClick={onConfirm}>
            {confirmLabel}
          </Button>
        </div>
      </div>
    </div>
  )
}

import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from './alert-dialog'

interface ConfirmDialogProps { open: boolean; title: string; message: string; confirmLabel?: string; variant?: 'default' | 'destructive'; onConfirm: () => void; onCancel: () => void }

export function ConfirmDialog({ open, title, message, confirmLabel = '删除', variant = 'destructive', onConfirm, onCancel }: ConfirmDialogProps) {
  return <AlertDialog open={open} onOpenChange={(next) => !next && onCancel()}><AlertDialogContent><AlertDialogHeader><AlertDialogTitle>{title}</AlertDialogTitle><AlertDialogDescription>{message}</AlertDialogDescription></AlertDialogHeader><AlertDialogFooter><AlertDialogCancel onClick={onCancel}>取消</AlertDialogCancel><AlertDialogAction variant={variant} onClick={onConfirm}>{confirmLabel}</AlertDialogAction></AlertDialogFooter></AlertDialogContent></AlertDialog>
}

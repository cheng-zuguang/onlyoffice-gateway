import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ServicesPage from './ServicesPage'
import * as api from '../lib/api'

vi.mock('../lib/api', async () => {
  const actual = await vi.importActual<typeof import('../lib/api')>('../lib/api')
  return {
    ...actual,
    listServices: vi.fn(),
    createService: vi.fn(),
    rotateWebhookSecret: vi.fn(),
    activateWebhookSecret: vi.fn(),
    rollbackWebhookSecret: vi.fn(),
  }
})

describe('ServicesPage webhook credentials', () => {
  const clipboardWriteText = vi.fn()

  beforeEach(() => {
    clipboardWriteText.mockReset()
    clipboardWriteText.mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText: clipboardWriteText },
    })
    vi.mocked(api.listServices).mockResolvedValue([])
    vi.mocked(api.createService).mockResolvedValue({
      service: {
        id: 'doc',
        public_key: 'public-key',
        allowed_webhook_domains: ['doc.example.com'],
        webhook_secret_configured: true,
        webhook_secret_pending: false,
      },
      credentials: { webhook_secret: 'one-time-service-secret' },
    })
  })

  it('shows a created secret once and clears it when the dialog closes', async () => {
    const user = userEvent.setup()
    render(<ServicesPage />)
    await screen.findByText('尚未配置服务')

    await user.click(screen.getByRole('button', { name: '新增服务' }))
    const form = screen.getByRole('dialog')
    await user.type(within(form).getByLabelText('服务标识'), 'doc')
    await user.type(within(form).getByLabelText('RSA Public Key (PEM)'), 'public-key')
    await user.click(within(form).getByRole('button', { name: '新增服务' }))

    expect(await screen.findByText('one-time-service-secret')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '我已保存，关闭' }))
    await waitFor(() => expect(screen.queryByText('one-time-service-secret')).not.toBeInTheDocument())
  })

  it('copies the one-time secret and shows visible success feedback', async () => {
    const user = userEvent.setup()
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText: clipboardWriteText },
    })
    render(<ServicesPage />)
    await screen.findByText('尚未配置服务')

    await user.click(screen.getByRole('button', { name: '新增服务' }))
    const form = screen.getByRole('dialog')
    await user.type(within(form).getByLabelText('服务标识'), 'doc')
    await user.type(within(form).getByLabelText('RSA Public Key (PEM)'), 'public-key')
    await user.click(within(form).getByRole('button', { name: '新增服务' }))

    await user.click(await screen.findByRole('button', { name: '复制 Webhook 凭证' }))

    expect(clipboardWriteText).toHaveBeenCalledWith('one-time-service-secret')
    expect(await screen.findByRole('button', { name: '已复制 Webhook 凭证' })).toBeInTheDocument()
  })

  it('falls back to the legacy copy command when the Clipboard API is unavailable', async () => {
    const execCommand = vi.fn().mockReturnValue(true)
    Object.defineProperty(document, 'execCommand', { configurable: true, value: execCommand })
    const user = userEvent.setup()
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined })
    render(<ServicesPage />)
    await screen.findByText('尚未配置服务')

    await user.click(screen.getByRole('button', { name: '新增服务' }))
    const form = screen.getByRole('dialog')
    await user.type(within(form).getByLabelText('服务标识'), 'doc')
    await user.type(within(form).getByLabelText('RSA Public Key (PEM)'), 'public-key')
    await user.click(within(form).getByRole('button', { name: '新增服务' }))

    await user.click(await screen.findByRole('button', { name: '复制 Webhook 凭证' }))

    expect(execCommand).toHaveBeenCalledWith('copy')
    expect(await screen.findByRole('button', { name: '已复制 Webhook 凭证' })).toBeInTheDocument()
  })

  it('shows the pending secret returned by rotation without activating it', async () => {
    vi.mocked(api.listServices).mockResolvedValue([{
      id: 'doc',
      public_key: 'public-key',
      allowed_webhook_domains: ['doc.example.com'],
      webhook_secret_configured: true,
      webhook_secret_pending: false,
    }])
    vi.mocked(api.rotateWebhookSecret).mockResolvedValue({
      service_id: 'doc',
      credentials: { webhook_secret: 'pending-service-secret' },
    })
    const user = userEvent.setup()
    render(<ServicesPage />)

    await user.click(await screen.findByRole('button', { name: '生成待切换凭证' }))

    expect(await screen.findByText('pending-service-secret')).toBeInTheDocument()
    expect(api.rotateWebhookSecret).toHaveBeenCalledWith('doc')
    expect(screen.getByText(/尚未激活/)).toBeInTheDocument()
  })

  it('requires confirmation before activating a pending credential', async () => {
    vi.mocked(api.listServices).mockResolvedValue([{
      id: 'doc',
      public_key: 'public-key',
      allowed_webhook_domains: ['doc.example.com'],
      webhook_secret_configured: true,
      webhook_secret_pending: true,
      webhook_secret_rollback_available: false,
    }])
    vi.mocked(api.activateWebhookSecret).mockResolvedValue({
      id: 'doc',
      public_key: 'public-key',
      allowed_webhook_domains: ['doc.example.com'],
      webhook_secret_configured: true,
      webhook_secret_pending: false,
      webhook_secret_rollback_available: true,
    })
    const user = userEvent.setup()
    render(<ServicesPage />)

    await user.click(await screen.findByRole('button', { name: '激活待切换凭证' }))
    expect(api.activateWebhookSecret).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: '确认激活' }))

    await waitFor(() => expect(api.activateWebhookSecret).toHaveBeenCalledWith('doc'))
  })

  it('requires confirmation before rolling back during the recovery window', async () => {
    vi.mocked(api.listServices).mockResolvedValue([{
      id: 'doc',
      public_key: 'public-key',
      allowed_webhook_domains: ['doc.example.com'],
      webhook_secret_configured: true,
      webhook_secret_pending: false,
      webhook_secret_rollback_available: true,
    }])
    vi.mocked(api.rollbackWebhookSecret).mockResolvedValue({
      id: 'doc',
      public_key: 'public-key',
      allowed_webhook_domains: ['doc.example.com'],
      webhook_secret_configured: true,
      webhook_secret_pending: false,
      webhook_secret_rollback_available: false,
    })
    const user = userEvent.setup()
    render(<ServicesPage />)

    await user.click(await screen.findByRole('button', { name: '回滚 Webhook 凭证' }))
    expect(api.rollbackWebhookSecret).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: '确认回滚' }))

    await waitFor(() => expect(api.rollbackWebhookSecret).toHaveBeenCalledWith('doc'))
  })
})

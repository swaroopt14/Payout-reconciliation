import type { SupportTicket, SupportMessage } from '@/services/payout-command/support/supportTickets'

export type SupportSlackEvent =
  | { kind: 'new_ticket'; tenantId: string; ticket: SupportTicket }
  | { kind: 'chat_reply'; tenantId: string; ticket: SupportTicket; message: SupportMessage }
  | { kind: 'email'; tenantId: string; ticket: SupportTicket; message: SupportMessage }
  | { kind: 'manual_review'; tenantId: string; ticket: SupportTicket }
  | {
      kind: 'login'
      email: string
      name?: string
      tenantId?: string
      tenantName?: string
      surface: string
      demo?: boolean
    }

let warnedMissingWebhook = false

/**
 * Support Slack Incoming Webhook comes only from env (D32: no secrets in code).
 * Unset → returns null; callers skip sending. The URL itself is never logged.
 */
function resolveSupportWebhookUrl(): string | null {
  const url = process.env.SLACK_SUPPORT_WEBHOOK_URL?.trim()
  if (url) return url
  if (!warnedMissingWebhook) {
    warnedMissingWebhook = true
    console.warn('[support] SLACK_SUPPORT_WEBHOOK_URL is not set; skipping Slack notifications')
  }
  return null
}

function previewText(body: string, max = 400) {
  const trimmed = body.trim()
  if (trimmed.length <= max) return trimmed
  return `${trimmed.slice(0, max)}…`
}

function headerForEvent(event: SupportSlackEvent): string {
  switch (event.kind) {
    case 'new_ticket':
      return 'New Zord support ticket'
    case 'chat_reply':
      return 'Support ticket reply'
    case 'email':
      return 'Support ticket email'
    case 'manual_review':
      return 'Manual review escalated to support'
    case 'login':
      return 'Zord console sign-in'
    default:
      return 'Zord support update'
  }
}

function line(label: string, value: string | null | undefined) {
  return `${label}: ${value && value.trim().length ? value.trim() : '-'}`
}

function textForEvent(event: SupportSlackEvent): string {
  if (event.kind === 'login') {
    return [
      headerForEvent(event),
      line('Email', event.email),
      line('Name', event.name),
      line('Tenant', event.tenantName || event.tenantId),
      line('Surface', event.surface),
      line('Mode', event.demo ? 'Demo' : 'Password'),
      line('Time', new Date().toISOString()),
    ].join('\n')
  }

  const { ticket, tenantId } = event
  const rows = [
    headerForEvent(event),
    line('Ticket', `#${ticket.ticketNumber}`),
    line('Topic', ticket.topic),
    line('Category', ticket.category),
    line('Status', ticket.status),
    line('Tenant', tenantId),
    line('Contact', ticket.contactEmail),
  ]

  if (event.kind === 'email') {
    rows.push(
      line('To', event.message.emailTo),
      line('Subject', event.message.emailSubject),
      `Body: ${previewText(event.message.body)}`,
    )
  } else if (event.kind === 'chat_reply') {
    rows.push(`Reply: ${previewText(event.message.body)}`)
  } else if (ticket.messages[0]?.body) {
    rows.push(`Description: ${previewText(ticket.messages[0].body)}`)
  }

  rows.push(line('Id', ticket.id))
  return rows.join('\n')
}

/**
 * Incoming Webhooks accept a simple `{ text }` JSON body.
 * Block Kit `header` payloads often come back as HTTP 404 from Slack.
 */
async function postIncomingWebhook(webhook: string, text: string): Promise<boolean> {
  try {
    const controller = new AbortController()
    const timeout = setTimeout(() => controller.abort(), 4000)
    const res = await fetch(webhook, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ text }),
      signal: controller.signal,
    })
    const raw = (await res.text()).slice(0, 240)
    clearTimeout(timeout)
    if (!res.ok) {
      console.warn('[support] slack webhook rejected', res.status, raw)
      return false
    }
    console.info('[support] slack webhook delivered')
    return true
  } catch (err) {
    console.warn('[support] slack webhook failed', err instanceof Error ? err.message : 'unknown')
    return false
  }
}

/** Post support activity to Slack via Incoming Webhook. Resolves to false on any failure. */
export async function notifySupportSlack(event: SupportSlackEvent): Promise<boolean> {
  const webhook = resolveSupportWebhookUrl()
  if (!webhook) return false
  return postIncomingWebhook(webhook, textForEvent(event))
}

/** Login alerts must not delay sign-in if Slack is slow. */
export function notifyLoginSlack(event: Extract<SupportSlackEvent, { kind: 'login' }>) {
  void notifySupportSlack(event)
}

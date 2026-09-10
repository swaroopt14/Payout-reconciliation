export type RoutingScore = {
  authorization_rate: number
  health_score: number
  cost_score: number
  latency_score: number
  priority_score: number
  ml_score: number | null
  final_score: number
}

export type RoutingCandidate = {
  psp: string
  connector_id: string
  score: number
  breakdown: RoutingScore
}

export type RoutingDecision = {
  routing_id: string
  payment_id: string
  selected_processor: string
  psp: string
  connector_id: string
  rail: string
  direction: string
  routing_strategy: string
  algorithm: string
  score: number
  reason: RoutingScore
  candidates: RoutingCandidate[]
  fallback_processors: string[]
  rules_applied: string[]
  metrics_source: string
  rail_rewritten_from?: string
}

export type RoutingProcessor = {
  psp: string
  name: string
  enabled: boolean
  collect_rails: string[]
  payout_rails: string[]
  currencies: string[]
  priority: number
  authorization_rate: number
  health_score: number
  metrics_source: string
}

export async function getProcessors(): Promise<{
  ok: boolean
  status: number
  processors: RoutingProcessor[]
  errorText?: string
}> {
  try {
    const response = await fetch('/api/prod/routing/processors', {
      method: 'GET',
      credentials: 'include',
      cache: 'no-store',
    })
    const text = await response.text()
    if (!response.ok) {
      return { ok: false, status: response.status, processors: [], errorText: text }
    }
    const parsed = JSON.parse(text) as { processors?: RoutingProcessor[] }
    return { ok: true, status: response.status, processors: parsed.processors ?? [] }
  } catch (error) {
    return {
      ok: false,
      status: 0,
      processors: [],
      errorText: error instanceof Error ? error.message : 'network',
    }
  }
}

export async function postRouteDecision(body: {
  payment_id: string
  amount_minor: number
  currency?: string
  payment_method?: string
  rail?: string
  direction?: 'INBOUND' | 'OUTBOUND'
  country?: string
  ml_score?: number | null
}): Promise<{ ok: boolean; status: number; data: RoutingDecision | null; errorText?: string }> {
  const url = '/api/prod/routing/route'
  try {
    const response = await fetch(url, {
      method: 'POST',
      credentials: 'include',
      cache: 'no-store',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(body),
    })
    const text = await response.text()
    if (!response.ok) {
      return { ok: false, status: response.status, data: null, errorText: text }
    }
    return { ok: true, status: response.status, data: JSON.parse(text) as RoutingDecision }
  } catch (error) {
    return {
      ok: false,
      status: 0,
      data: null,
      errorText: error instanceof Error ? error.message : 'network',
    }
  }
}

export function processorLabel(psp: string) {
  const key = psp.trim().toLowerCase()
  if (key === 'razorpay') return 'Razorpay'
  if (key === 'cashfree') return 'Cashfree'
  if (key === 'payu') return 'PayU'
  if (key === 'stripe') return 'Stripe'
  return psp
}

export function pct(score: number) {
  return Math.round(Math.min(1, Math.max(0, score)) * 1000) / 10
}

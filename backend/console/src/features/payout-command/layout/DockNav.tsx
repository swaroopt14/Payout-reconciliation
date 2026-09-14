'use client'

import Link from 'next/link'
import { usePathname, useRouter } from 'next/navigation'
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'

import { ZordLogo } from '@/components/ZordLogo'
import { useEnvironment } from '@/services/auth/EnvironmentProvider'
import { persistEnvMode } from '@/services/auth/persistEnvMode'
import { payoutBatchCommandCenterHref } from '@/services/payout-command/batchCommandCenterHref'
import {
  DEMO_SMOKE_BATCH_ID,
  getActiveDemoBatchId,
  sandboxDockHref,
  setActiveDemoBatchId,
  withDemoBatchScope,
} from '@/services/payout-command/demo/ycDemoConstants'
import { type DockId, type GlyphName } from '@/services/payout-command/model'
import type { OpsInsightAlert } from '../command-center/types'
import { ModeTogglePill } from '../sandbox/ModeTogglePill'
import { TopbarUtilityCluster } from './TopbarUtilityCluster'
import { Glyph } from '../shared'
import { ScenarioSwitcher } from './ScenarioSwitcher'
import {
  CROSS_BORDER_TRACE_ID,
  SCENARIO_CROSS_BORDER,
  getStoredScenario,
  withScenarioScope,
  type ConsoleScenario,
} from '@/services/payout-command/demo/scenarioMode'

const SIDEBAR_COLLAPSE_KEY = 'zord-console-sidebar-collapsed'
const CROSS_BORDER_SETTINGS_KEY = 'zord-console-cb-settings-nav'

/** Compact top bar - only demo-critical destinations (rest live in the left rail). */
type TopProductLink = {
  id: string
  label: string
  icon: GlyphName
  dock?: DockId
  href?: string
  match: (dock: DockId, pathname: string) => boolean
}

function overviewTopLink(batchScoped: (path: string) => string): TopProductLink {
  return {
    id: 'home',
    label: 'Overview',
    icon: 'home',
    href: batchScoped('/overview'),
    match: (_d, pathname) => pathname.startsWith('/overview'),
  }
}

function uploadTopLink(uploadHref: string): TopProductLink {
  return {
    id: 'upload',
    label: 'Upload',
    icon: 'folder',
    href: uploadHref,
    match: (_d, pathname) =>
      pathname.includes('batch-command-center') || pathname.startsWith('/payouts/new'),
  }
}

function intentTopLink(intentHref: string): TopProductLink {
  return {
    id: 'intents',
    label: 'Intent',
    icon: 'banknote',
    href: intentHref,
    match: (d, pathname) =>
      pathname.startsWith('/payouts/intents') ||
      (pathname.startsWith('/sandbox') && d === 'grid') ||
      (d === 'grid' &&
        !pathname.startsWith('/connections') &&
        !pathname.startsWith('/controls') &&
        !pathname.startsWith('/actions/') &&
        pathname.endsWith('/contract') &&
        !pathname.startsWith('/overview') &&
        !pathname.includes('batch-command-center')),
  }
}

/** India top bar: compact Finance Controller destinations. */
function buildIndiaTopProductLinks(
  intentHref: string,
  batchScoped: (path: string) => string,
): TopProductLink[] {
  return [
    overviewTopLink(batchScoped),
    {
      id: 'intents',
      label: 'Transactions',
      icon: 'banknote',
      href: intentHref,
      match: (d, pathname) =>
        pathname.startsWith('/transactions') ||
        pathname.startsWith('/payouts/intents') ||
        (pathname.startsWith('/sandbox') && d === 'grid'),
    },
    {
      id: 'gaps',
      label: 'Exceptions',
      icon: 'gaps',
      href: batchScoped('/exceptions'),
      match: (_d, pathname) => pathname.startsWith('/exceptions'),
    },
    {
      id: 'settlement',
      label: 'Settlements',
      icon: 'settlement',
      href: batchScoped('/settlement/journal'),
      match: (_d, pathname) => pathname.startsWith('/settlement/journal'),
    },
    {
      id: 'proof',
      label: 'Evidence',
      icon: 'eye',
      href: batchScoped('/proof'),
      match: (_d, pathname) => pathname.startsWith('/proof'),
    },
  ]
}

/** Cross-border top bar: Overview · Upload · Intent · Settlement · Proof. */
function buildTopProductLinks(
  uploadHref: string,
  intentHref: string,
  batchScoped: (path: string) => string,
): TopProductLink[] {
  return [
    overviewTopLink(batchScoped),
    uploadTopLink(uploadHref),
    intentTopLink(intentHref),
    {
      id: 'settlement',
      label: 'Settlement',
      icon: 'settlement',
      href: batchScoped('/settlement/journal'),
      match: (_d, pathname) => pathname.startsWith('/settlement/journal'),
    },
    {
      id: 'proof',
      label: 'Proof',
      icon: 'eye',
      href: batchScoped('/proof'),
      match: (_d, pathname) => pathname.startsWith('/proof'),
    },
  ]
}

type NavLinkItem = {
  id: string
  label: string
  icon: GlyphName
  href?: string
  dock?: DockId
  match: boolean
  ariaLabel: string
}

function navItem(item: NavLinkItem): NavLinkItem {
  return item
}

type DockNavProps = {
  activeDock: DockId
  onDockChange: (id: DockId) => void
  alerts?: readonly OpsInsightAlert[]
  onActivateClick: () => void
  children?: ReactNode
  /** Pinned under the page (demo flow bar). */
  footer?: ReactNode
}

/**
  * Zord console chrome - left rail is primary IA (demo path + more).
  * Top bar keeps only the highest-signal demo destinations.
  */
export function DockNav({
  activeDock,
  onDockChange,
  alerts,
  onActivateClick,
  children,
  footer,
}: DockNavProps) {
  const pathname = usePathname() ?? ''
  const router = useRouter()
  const { mode, canSwitchToLive } = useEnvironment()
  /** Instant highlight while the destination page is still loading. */
  const [pendingHref, setPendingHref] = useState<string | null>(null)
  /** Admin `?tab=` without useSearchParams (avoids Suspense on the whole shell). */
  const [adminTab, setAdminTab] = useState('team')
  /** Active batch - kept in sync so every menu link opens the same batch. */
  const [activeBatchId, setActiveBatchId] = useState(DEMO_SMOKE_BATCH_ID)
  const [scenario, setScenario] = useState<ConsoleScenario>('inr')
  /** Cross-border: More + Workspace stay under Settings until expanded. */
  const [crossBorderSettingsOpen, setCrossBorderSettingsOpen] = useState(false)
  const stallNavTimerRef = useRef<number | null>(null)

  useEffect(() => {
    setPendingHref(null)
    setActiveBatchId(getActiveDemoBatchId())
    setScenario(getStoredScenario())
    try {
      setCrossBorderSettingsOpen(sessionStorage.getItem(CROSS_BORDER_SETTINGS_KEY) === '1')
    } catch {
      /* ignore */
    }
    if (stallNavTimerRef.current) {
      window.clearTimeout(stallNavTimerRef.current)
      stallNavTimerRef.current = null
    }
    if (pathname === '/admin' || pathname === '/admin/') {
      try {
        setAdminTab(new URLSearchParams(window.location.search).get('tab') ?? 'team')
      } catch {
        setAdminTab('team')
      }
    }
  }, [pathname])

  // Clear optimistic highlight if navigation stalls (same-route query changes included).
  useEffect(() => {
    if (!pendingHref) return
    const t = window.setTimeout(() => setPendingHref(null), 1200)
    return () => window.clearTimeout(t)
  }, [pendingHref])

  useEffect(
    () => () => {
      if (stallNavTimerRef.current) window.clearTimeout(stallNavTimerRef.current)
    },
    [],
  )

  const scopeHref = useCallback(
    (href: string) => withScenarioScope(withDemoBatchScope(href, activeBatchId), scenario),
    [activeBatchId, scenario],
  )

  const onOverview = pathname.startsWith('/overview')
  const onConnections = pathname.startsWith('/connections')
  const onPolicies = pathname.startsWith('/controls/policies')
  const onControlReview = pathname.startsWith('/controls/review')
  const onActionContract = pathname.includes("/actions/") && !pathname.includes("/new") && !pathname.endsWith("/authority") && !pathname.endsWith("/dispatch") && !pathname.endsWith("/signals") && !pathname.endsWith("/lifecycle")
  const onAgents = pathname.startsWith('/agents')
  const onProtocolInspector = pathname.startsWith('/build/protocol')
  const onCrossBorderDispatch = pathname.includes('/actions/') && pathname.endsWith('/dispatch')
  const onCrossBorderSignals = pathname.includes('/actions/') && pathname.endsWith('/signals')
  const onCrossBorderLifecycle = pathname.includes('/actions/') && pathname.endsWith('/lifecycle')
  const onCrossBorderAuthority = pathname.includes('/actions/') && pathname.endsWith('/authority')
  const onActionDesk = pathname === '/actions/new' || pathname.startsWith('/actions/new')
  const onDispatchRelay = pathname === '/payouts' || pathname.startsWith('/execution')
  const onPaymentTrace = pathname.startsWith('/payments')
  const onSettlementJournal = pathname.startsWith('/settlement/journal')
  const onSettlementsPage = pathname.startsWith('/settlements')
  const onOutcomeReview = pathname.startsWith('/settlement/review')
  const onPaymentGaps = pathname.startsWith('/settlement/gaps')
  const onExceptions = pathname.startsWith('/exceptions')
  const onReconciliation = pathname.startsWith('/reconciliation') || pathname.startsWith('/settlement/review')
  const onCashPosition = pathname.startsWith('/cash-position')
  const onBankStatements = pathname.startsWith('/bank-statements')
  const onInvestigations = pathname.startsWith('/investigations')
  const onEvaluation = pathname.startsWith('/evaluation')
  const onProofCenter = pathname.startsWith('/proof')
  const onDeveloper = pathname.startsWith('/developer') || pathname.startsWith('/connections')
  const onAskZord = pathname.startsWith('/ask') || (pathname.startsWith('/sandbox') && activeDock === 'workspace')
  const onWorkspaceAdmin = pathname === '/admin' || pathname === '/admin/'
  const onAdminTeam =
    onWorkspaceAdmin && (adminTab === 'team' || adminTab === 'roles' || adminTab === 'access')
  const onAdminAudit = onWorkspaceAdmin && adminTab === 'audit'
  const onAdminSupport = onWorkspaceAdmin && adminTab === 'support'

  const hrefIsPending = useCallback(
    (href: string | undefined) => {
      if (!pendingHref || !href) return false
      const [pPath, pQuery = ''] = pendingHref.split('?')
      const [hPath, hQuery = ''] = href.split('?')
      if (pPath !== hPath) return false
      const pq = new URLSearchParams(pQuery)
      const hq = new URLSearchParams(hQuery)
      if (hPath === '/admin') {
        return (pq.get('tab') ?? 'team') === (hq.get('tab') ?? 'team')
      }
      if (hPath === '/sandbox' || hPath.startsWith('/sandbox')) {
        return (pq.get('dock') ?? '') === (hq.get('dock') ?? '')
      }
      return true
    },
    [pendingHref],
  )

  const navActive = useCallback(
    (href: string | undefined, pathMatch: boolean) => {
      if (pendingHref) return hrefIsPending(href)
      return pathMatch
    },
    [pendingHref, hrefIsPending],
  )
  const onControls = pathname.startsWith('/controls') || onActionContract
  const onIntents =
    pathname.startsWith('/transactions') ||
    pathname.startsWith('/payouts/intents') ||
    (pathname.startsWith('/sandbox') && activeDock === 'grid') ||
    (activeDock === 'grid' &&
      !onConnections &&
      !onControls &&
      !onOverview &&
      !pathname.includes('batch-command-center'))
  const onNewPayout =
    pathname.includes('batch-command-center') || pathname.startsWith('/payouts/new')

  const batchCenterHref = payoutBatchCommandCenterHref(
    pathname.startsWith('/sandbox') ||
      mode === 'sandbox' ||
      pathname.startsWith('/overview') ||
      pathname.startsWith('/connections') ||
      pathname.startsWith('/controls') ||
      pathname.startsWith('/actions/') && pathname.endsWith('/contract') ||
      pathname.startsWith('/execution') ||
      pathname.startsWith('/payments') ||
      pathname.startsWith('/settlement') ||
      pathname.startsWith('/exceptions') ||
      pathname.startsWith('/reconciliation') ||
      pathname.startsWith('/cash-position') ||
      pathname.startsWith('/bank-statements') ||
      pathname.startsWith('/investigations') ||
      pathname.startsWith('/evaluation') ||
      pathname.startsWith('/transactions') ||
      pathname.startsWith('/proof') ||
      pathname.startsWith('/payouts') ||
      pathname.startsWith('/developer') ||
      pathname.startsWith('/admin'),
  )
  const uploadHref = scopeHref(`${batchCenterHref.split('?')[0]}?upload=1`)
  /** Journal list only — never deep-link a batch from the rail (that opens batch details). */
  const intentHref = '/transactions?demo=sandbox'
  const indiaDispatchHref = scopeHref('/payouts')
  const topProductLinks = useMemo(
    () =>
      scenario === SCENARIO_CROSS_BORDER
        ? buildTopProductLinks(uploadHref, intentHref, scopeHref)
        : buildIndiaTopProductLinks(intentHref, scopeHref),
    [scenario, uploadHref, intentHref, scopeHref],
  )

  const onDockSurface = (dock: DockId) =>
    !onConnections &&
    !onControls &&
    !onDispatchRelay &&
    !onPaymentTrace &&
    !onSettlementJournal &&
    !onOutcomeReview &&
    !onPaymentGaps &&
    !onExceptions &&
    !onReconciliation &&
    !onCashPosition &&
    !onBankStatements &&
    !onInvestigations &&
    !onEvaluation &&
    !onProofCenter &&
    !onOverview &&
    !onNewPayout &&
    !onIntents &&
    activeDock === dock

  /**
   * Cross-border rail: Overview → Workflow → Payment Signals → Execution → Protocol.
   * India rail: Razorpay-style primary links, collapsible Settings, pinned Sandbox + Workspace.
   */
  const overviewNav: NavLinkItem[] = useMemo(
    () => [
      {
        id: 'overview',
        label: 'Overview',
        icon: 'home',
        href: scopeHref('/overview'),
        match: onOverview,
        ariaLabel: 'Overview. Workspace health and attention queue.',
      },
    ],
    [scopeHref, onOverview],
  )

  const workflowNav: NavLinkItem[] = useMemo(() => {
    const isCrossBorder = scenario === SCENARIO_CROSS_BORDER
    /** One Dispatch entry: PAC control in cross-border, relay list in India/file. */
    const dispatchHref = isCrossBorder
      ? withScenarioScope(
          scopeHref(`/actions/${CROSS_BORDER_TRACE_ID}/dispatch`),
          SCENARIO_CROSS_BORDER,
        )
      : scopeHref('/execution/dispatches')
    const dispatchMatch = isCrossBorder
      ? onCrossBorderDispatch || onDispatchRelay
      : onDispatchRelay

    const items: NavLinkItem[] = [
      navItem({
        id: 'upload',
        label: 'Upload',
        icon: 'folder',
        href: uploadHref,
        match: onNewPayout,
        ariaLabel: 'Upload. Create payout obligations from file, form, or API.',
      }),
      navItem({
        id: 'intent',
        label: 'Intent',
        icon: 'banknote',
        href: intentHref,
        match: onIntents,
        ariaLabel: 'Intent Journal. Payment instructions before and after seal.',
      }),
      ...(isCrossBorder
        ? [
            navItem({
              id: 'policies',
              label: 'Policies',
              icon: 'shield',
              href: scopeHref('/controls/policies'),
              match: onPolicies,
              ariaLabel: 'Policy Studio. Rules a payout must satisfy before release.',
            }),
          ]
        : []),
      navItem({
        id: 'agents',
        label: 'Agents',
        icon: 'users',
        href: scopeHref('/agents'),
        match: onAgents,
        ariaLabel: 'Agent Registry. Tenant-owned governed workloads.',
      }),
      navItem({
        id: 'authority',
        label: 'Authority',
        icon: 'check',
        href: scopeHref(`/actions/${CROSS_BORDER_TRACE_ID}/authority`),
        match: onCrossBorderAuthority,
        ariaLabel: 'Authority Graph. Delegation chain for this financial action.',
      }),
      navItem({
        id: 'pac',
        label: 'PAC',
        icon: 'lock',
        href: scopeHref(`/actions/${CROSS_BORDER_TRACE_ID}`),
        match: onActionContract,
        ariaLabel: 'Payment Action Contract. The sealed, policy-bound instruction.',
      }),
      navItem({
        id: 'dispatch',
        label: 'Dispatch',
        icon: 'payout',
        href: dispatchHref,
        match: dispatchMatch,
        ariaLabel: isCrossBorder
          ? 'Dispatch. Gateway executes only after Payment Action Contract revalidation.'
          : 'Dispatch & Relay. Send sealed instructions through approved rails.',
      }),
    ]
    return items
  }, [
    scenario,
    scopeHref,
    uploadHref,
    intentHref,
    onNewPayout,
    onIntents,
    onPolicies,
    onAgents,
    onCrossBorderAuthority,
    onActionContract,
    onDispatchRelay,
    onCrossBorderDispatch,
  ])

  const signalsNav: NavLinkItem[] = useMemo(
    () => [
      {
        id: 'signals',
        label: 'Signals',
        icon: 'zap',
        href: scopeHref(`/actions/${CROSS_BORDER_TRACE_ID}/signals`),
        match: onCrossBorderSignals,
        ariaLabel: 'Signal Mesh. Raw provider evidence with provenance.',
      },
      {
        id: 'lifecycle',
        label: 'Lifecycle',
        icon: 'grid',
        href: scopeHref(`/actions/${CROSS_BORDER_TRACE_ID}/lifecycle`),
        match: onPaymentTrace || onCrossBorderLifecycle,
        ariaLabel: 'Lifecycle. Derived state from accepted signals for this payout.',
      },
      {
        id: 'connections',
        label: 'Connections',
        icon: 'link',
        href: scopeHref('/connections'),
        match: onConnections,
        ariaLabel: 'Connections. Source systems, rails, and outcome feeds.',
      },
    ],
    [scopeHref, onPaymentTrace, onCrossBorderLifecycle, onCrossBorderSignals, onConnections],
  )

  const executionNav: NavLinkItem[] = useMemo(
    () => [
      {
        id: 'settlement',
        label: 'Settlements',
        icon: 'settlement',
        href: scopeHref('/settlement/journal'),
        match: onSettlementJournal,
        ariaLabel: 'Settlements. Expected vs observed outcomes.',
      },
      {
        id: 'proof',
        label: 'Evidence',
        icon: 'eye',
        href: scopeHref('/proof'),
        match: onProofCenter,
        ariaLabel: 'Evidence. Proof packs and Merkle verification.',
      },
      {
        id: 'gaps',
        label: 'Exceptions',
        icon: 'gaps',
        href: scopeHref('/exceptions'),
        match: onExceptions || onPaymentGaps,
        ariaLabel: 'Exceptions. Finance operations inbox.',
      },
      {
        id: 'outcome',
        label: 'Reconciliation',
        icon: 'chart',
        href: scopeHref('/reconciliation'),
        match: onReconciliation,
        ariaLabel: 'Reconciliation. Matched vs unresolved outcomes.',
      },
    ],
    [scopeHref, onSettlementJournal, onProofCenter, onPaymentGaps, onExceptions, onReconciliation],
  )

  const protocolNav: NavLinkItem[] = useMemo(
    () => [
      {
        id: 'protocol',
        label: 'Protocol',
        icon: 'key',
        href: scopeHref('/build/protocol'),
        match: onProtocolInspector,
        ariaLabel: 'Protocol Inspector. Schemas, media types, and verification profile.',
      },
    ],
    [scopeHref, onProtocolInspector],
  )

  /** Settings — extra tools that sit outside the locked payout loop. */
  const moreNav: NavLinkItem[] = useMemo(() => {
    return [
      {
        id: 'action-desk',
        label: 'Action Desk',
        icon: 'document',
        href: scopeHref('/actions/new'),
        match: onActionDesk,
        ariaLabel: 'Action Desk. Propose a payout. Not authorized until a Payment Action Contract is sealed.',
      },
      {
        id: 'control-review',
        label: 'Control Review',
        icon: 'check',
        href: scopeHref('/controls/review'),
        match: onControlReview,
        ariaLabel: 'Control Review. Resolve blocked or warned payouts before money moves.',
      },
      {
        id: 'ask',
        label: 'Ask',
        icon: 'terminal',
        href: scopeHref('/ask'),
        match: onAskZord,
        ariaLabel: 'Ask. Investigate and navigate with citations.',
      },
      {
        id: 'billing',
        label: 'Billing',
        icon: 'billing',
        href: sandboxDockHref('billing', activeBatchId),
        match: onDockSurface('billing'),
        ariaLabel: 'Billing.',
      },
    ]
  }, [
    scopeHref,
    activeBatchId,
    onActionDesk,
    onControlReview,
    onAskZord,
    activeDock,
    pathname,
    onOverview,
    onNewPayout,
    onIntents,
    onConnections,
    onControls,
    onDispatchRelay,
    onPaymentTrace,
    onSettlementJournal,
    onProofCenter,
    onPaymentGaps,
    onExceptions,
  ])

  /** Spec secondary rail (bottom): Connections · Team & Access · Audit Log · Support. */
  const secondaryNav: NavLinkItem[] = useMemo(
    () => [
      {
        id: 'developer',
        label: 'Connections',
        icon: 'link',
        href: scopeHref('/connections'),
        match: onDeveloper || onConnections,
        ariaLabel: 'Connections. Razorpay, bank APIs, webhooks, and source freshness.',
      },
      {
        id: 'team-access',
        label: 'Team & Access',
        icon: 'users',
        href: scopeHref('/admin?tab=team'),
        match: onAdminTeam,
        ariaLabel: 'Workspace administration. Team, roles, and access policies.',
      },
      {
        id: 'audit-log',
        label: 'Audit Log',
        icon: 'copy',
        href: scopeHref('/admin?tab=audit'),
        match: onAdminAudit,
        ariaLabel: 'Sign-in log. Live emails and timestamps from the login-audit API.',
      },
      {
        id: 'support',
        label: 'Support',
        icon: 'support',
        href: scopeHref('/admin?tab=support'),
        match: onAdminSupport,
        ariaLabel: 'Support. Raise tickets with contract, batch, trace, or proof context.',
      },
    ],
    [scopeHref, onDeveloper, onConnections, onAdminTeam, onAdminAudit, onAdminSupport],
  )

  const indiaTransactionsNav: NavLinkItem[] = useMemo(
    () => [
      {
        id: 'transactions',
        label: 'Transactions',
        icon: 'banknote',
        href: intentHref,
        match: onIntents,
        ariaLabel: 'Transactions. Payment instructions from the intent journal.',
      },
      {
        id: 'reconciliation',
        label: 'Reconciliation',
        icon: 'chart',
        href: scopeHref('/reconciliation'),
        match: onReconciliation,
        ariaLabel: 'Reconciliation. Matched, ambiguous, unresolved, and conflicted records.',
      },
      {
        id: 'exceptions',
        label: 'Exceptions',
        icon: 'gaps',
        href: scopeHref('/exceptions'),
        match: onExceptions || onPaymentGaps,
        ariaLabel: 'Exceptions. Finance operations inbox.',
      },
      {
        id: 'settlements',
        label: 'Settlements',
        icon: 'settlement',
        href: scopeHref('/settlements'),
        match: onSettlementsPage || onSettlementJournal,
        ariaLabel: 'Settlements. Razorpay settlement cycles and UTR evidence.',
      },
      {
        id: 'payouts',
        label: 'Payouts',
        icon: 'payout',
        href: indiaDispatchHref,
        match: onDispatchRelay,
        ariaLabel: 'Payouts. Dispatched payouts and UTR outcomes.',
      },
    ],
    [
      intentHref,
      scopeHref,
      indiaDispatchHref,
      onIntents,
      onReconciliation,
      onExceptions,
      onPaymentGaps,
      onSettlementJournal,
      onSettlementsPage,
      onDispatchRelay,
    ],
  )

  const indiaFinanceNav: NavLinkItem[] = useMemo(
    () => [
      {
        id: 'cash-position',
        label: 'Cash Position',
        icon: 'bank',
        href: scopeHref('/cash-position'),
        match: onCashPosition,
        ariaLabel: 'Cash position. Derived settlement net vs bank credited vs unresolved exposure.',
      },
      {
        id: 'bank-statements',
        label: 'Bank statements',
        icon: 'bank',
        href: scopeHref('/bank-statements'),
        match: onBankStatements,
        ariaLabel: 'Bank statements. Per-row MATCHED or VARIANCE from recon.',
      },
      {
        id: 'investigations',
        label: 'Investigations',
        icon: 'search',
        href: scopeHref('/investigations'),
        match: onInvestigations,
        ariaLabel: 'Investigations. Agent traces for recon exceptions.',
      },
      {
        id: 'evidence',
        label: 'Evidence',
        icon: 'eye',
        href: scopeHref('/proof'),
        match: onProofCenter,
        ariaLabel: 'Evidence. Proof packs with Merkle root verification.',
      },
      {
        id: 'ask',
        label: 'Ask',
        icon: 'terminal',
        href: scopeHref('/ask'),
        match: onAskZord,
        ariaLabel: 'Ask. Finance operations copilot.',
      },
    ],
    [scopeHref, onCashPosition, onBankStatements, onInvestigations, onProofCenter, onAskZord],
  )

  const indiaDataNav: NavLinkItem[] = useMemo(
    () => [
      {
        id: 'uploads',
        label: 'Uploads',
        icon: 'folder',
        href: uploadHref,
        match: onNewPayout,
        ariaLabel: 'Uploads. Settlement, bank, and obligation files.',
      },
    ],
    [uploadHref, onNewPayout],
  )

  const indiaControlNav: NavLinkItem[] = useMemo(
    () => [
      {
        id: 'control-review',
        label: 'Control Review',
        icon: 'check',
        href: scopeHref('/controls/review'),
        match: onControlReview,
        ariaLabel: 'Control Review. Finance-control health.',
      },
      {
        id: 'evaluation',
        label: 'Evaluation',
        icon: 'grid',
        href: scopeHref('/evaluation'),
        match: onEvaluation,
        ariaLabel: 'Evaluation. Finance controller scoring.',
      },
    ],
    [scopeHref, onControlReview, onEvaluation],
  )

  const [mobileNavOpen, setMobileNavOpen] = useState(false)
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false)
  const [dismissed, setDismissed] = useState<Set<string>>(() => new Set())

  useEffect(() => {
    try {
      setSidebarCollapsed(window.localStorage.getItem(SIDEBAR_COLLAPSE_KEY) === '1')
    } catch {
      /* ignore */
    }
  }, [])

  const toggleSidebarCollapsed = useCallback(() => {
    setSidebarCollapsed((prev) => {
      const next = !prev
      try {
        window.localStorage.setItem(SIDEBAR_COLLAPSE_KEY, next ? '1' : '0')
      } catch {
        /* ignore */
      }
      return next
    })
  }, [])

  const visibleAlerts = useMemo(
    () => (alerts ?? []).filter((a) => !dismissed.has(a.id)),
    [alerts, dismissed],
  )
  const alertCount = visibleAlerts.length

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && mobileNavOpen) setMobileNavOpen(false)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [mobileNavOpen])

  const handleDockSelect = useCallback(
    (id: DockId) => {
      onDockChange(id)
      setMobileNavOpen(false)
    },
    [onDockChange],
  )

  /** Razorpay-style: rounded active pill only - no black edge rails. */
  const navItemClass = (active: boolean, collapsed: boolean) =>
    `group flex h-9 items-center rounded-[8px] text-left outline-none transition-colors focus-visible:ring-2 focus-visible:ring-[#2563EB]/30 ${
      collapsed ? 'justify-center px-0' : 'gap-2.5 px-2.5'
    } ${
      active
        ? 'bg-[#E8EAED] font-semibold text-[#0B1324]'
        : 'font-medium text-[#64748B] hover:bg-[#EEF1F4] hover:text-[#0F172A]'
    }`

  const markPendingNav = useCallback((href: string) => {
    setPendingHref(href)
    try {
      const q = new URLSearchParams(href.split('?')[1] ?? '')
      const batch = q.get('batch_id')?.trim() || q.get('client_batch_id')?.trim()
      if (batch) {
        setActiveDemoBatchId(batch)
        setActiveBatchId(batch)
      }
      if (href.startsWith('/admin')) {
        setAdminTab(q.get('tab') ?? 'team')
      }
    } catch {
      /* ignore */
    }
  }, [])

  /**
    * Soft nav from the heavy `/sandbox` client shell often stalls (highlight changes, URL does not).
    * Drive `router.push` ourselves and hard-assign if the location never moves.
    */
  const navigateTo = useCallback(
    (href: string) => {
      markPendingNav(href)
      setMobileNavOpen(false)
      if (stallNavTimerRef.current) {
        window.clearTimeout(stallNavTimerRef.current)
        stallNavTimerRef.current = null
      }
      // Intent list used to live in the sandbox dock shell — keep a hard nav
      // only for leftover `/sandbox?dock=` URLs that still redirect.
      if (href.startsWith('/sandbox?dock=')) {
        window.location.assign(href)
        return
      }
      const start = `${window.location.pathname}${window.location.search}`
      router.push(href)
      stallNavTimerRef.current = window.setTimeout(() => {
        stallNavTimerRef.current = null
        const now = `${window.location.pathname}${window.location.search}`
        if (now === start) {
          window.location.assign(href)
        }
      }, 700)
    },
    [markPendingNav, router],
  )

  const renderNavItem = (item: NavLinkItem, collapsed: boolean) => {
    const active = navActive(item.href, item.match)
    const iconClass = `h-4 w-4 shrink-0 ${
      active ? 'text-[#0B1324]' : 'text-[#94A3B8] group-hover:text-[#64748B]'
    }`
    const label = !collapsed ? (
      <span className="truncate text-[13px] tracking-[-0.01em]">{item.label}</span>
    ) : null

    if (item.href) {
      return (
        <Link
          key={item.id}
          href={item.href}
          prefetch
          title={item.label}
          aria-label={item.ariaLabel}
          aria-current={active ? 'page' : undefined}
          className={navItemClass(active, collapsed)}
          onClick={(e) => {
            e.preventDefault()
            navigateTo(item.href!)
          }}
        >
          <Glyph name={item.icon} className={iconClass} />
          {label}
        </Link>
      )
    }

    return (
      <button
        key={item.id}
        type="button"
        title={item.label}
        aria-label={item.ariaLabel}
        aria-current={active ? 'page' : undefined}
        className={navItemClass(active, collapsed)}
        onClick={() => {
          if (item.dock) handleDockSelect(item.dock)
        }}
      >
        <Glyph name={item.icon} className={iconClass} />
        {label}
      </button>
    )
  }

  const sectionLabel = (text: string, collapsed: boolean) =>
    collapsed ? null : (
      <p className="mb-1 mt-1 px-2.5 text-[10px] font-semibold uppercase tracking-[0.08em] text-[#94A3B8]">
        {text}
      </p>
    )

  const renderSidebarNav = (collapsed: boolean) => {
    const onMoreOrWorkspacePage =
      moreNav.some((item) => item.match) || secondaryNav.some((item) => item.match)
    const showMoreWorkspace =
      scenario !== SCENARIO_CROSS_BORDER || crossBorderSettingsOpen || onMoreOrWorkspacePage

    const toggleCrossBorderSettings = () => {
      setCrossBorderSettingsOpen((prev) => {
        const next = !prev
        try {
          sessionStorage.setItem(CROSS_BORDER_SETTINGS_KEY, next ? '1' : '0')
        } catch {
          /* ignore */
        }
        return next
      })
    }

    const isIndia = scenario !== SCENARIO_CROSS_BORDER
    const indiaSandboxOn = pathname.startsWith('/sandbox') || mode === 'sandbox'

    return (
      <nav
        className={`flex flex-col gap-0.5 pb-3 pt-1 ${collapsed ? 'px-1.5' : 'px-2.5'} ${
          isIndia ? '' : 'min-h-full flex-1'
        }`}
        aria-label="Workspace navigation"
      >
        {isIndia ? (
          <>
            {overviewNav.map((item) => renderNavItem(item, collapsed))}
            {sectionLabel('Transactions', collapsed)}
            {indiaTransactionsNav.map((item) => renderNavItem(item, collapsed))}
            {sectionLabel('Finance Control', collapsed)}
            {indiaFinanceNav.map((item) => renderNavItem(item, collapsed))}
            {sectionLabel('Data', collapsed)}
            {indiaDataNav.map((item) => renderNavItem(item, collapsed))}
            {sectionLabel('Control', collapsed)}
            {indiaControlNav.map((item) => renderNavItem(item, collapsed))}

            <div className={`mt-2 ${collapsed ? 'mx-1' : 'mx-0'} border-t border-[#E2E8F0] pt-2`}>
              <button
                type="button"
                onClick={() => {
                  if (indiaSandboxOn) {
                    if (canSwitchToLive) {
                      persistEnvMode('live')
                      router.push('/overview')
                    } else onActivateClick()
                    return
                  }
                  persistEnvMode('sandbox')
                  router.push('/overview?demo=sandbox')
                }}
                className={navItemClass(false, collapsed)}
                aria-label={indiaSandboxOn ? 'Sandbox on. Switch to live.' : 'Sandbox off. Switch to sandbox.'}
              >
                <Glyph name="zap" className="h-4 w-4 shrink-0 text-[#94A3B8]" />
                {collapsed ? null : (
                  <>
                    <span className="min-w-0 flex-1 truncate text-[13px] tracking-[-0.01em] text-[#334155]">
                      Sandbox
                    </span>
                    <span
                      className={`relative h-5 w-9 shrink-0 rounded-full transition-colors ${
                        indiaSandboxOn ? 'bg-[#2E5BFF]' : 'bg-[#CBD5E1]'
                      }`}
                      aria-hidden
                    >
                      <span
                        className={`absolute top-0.5 h-4 w-4 rounded-full bg-white shadow-sm transition-[left] ${
                          indiaSandboxOn ? 'left-[18px]' : 'left-0.5'
                        }`}
                      />
                    </span>
                  </>
                )}
              </button>
              {sectionLabel('Workspace', collapsed)}
              {secondaryNav
                .filter((item) => item.id !== 'support')
                .map((item) => renderNavItem(item, collapsed))}
            </div>
          </>
        ) : (
          <>
            {overviewNav.map((item) => renderNavItem(item, collapsed))}
            <div className={`my-2 ${collapsed ? 'mx-1' : 'mx-2.5'} border-t border-[#E2E8F0]`} />
            {sectionLabel('Workflow', collapsed)}
            {workflowNav.map((item) => renderNavItem(item, collapsed))}
            <div className={`my-2 ${collapsed ? 'mx-1' : 'mx-2.5'} border-t border-[#E2E8F0]`} />
            {sectionLabel('Payment Signals', collapsed)}
            {signalsNav.map((item) => renderNavItem(item, collapsed))}
            <div className={`my-2 ${collapsed ? 'mx-1' : 'mx-2.5'} border-t border-[#E2E8F0]`} />
            {sectionLabel('Execution', collapsed)}
            {executionNav.map((item) => renderNavItem(item, collapsed))}
            <div className={`my-2 ${collapsed ? 'mx-1' : 'mx-2.5'} border-t border-[#E2E8F0]`} />
            {protocolNav.map((item) => renderNavItem(item, collapsed))}
          </>
        )}

        {isIndia ? null : <div className="mt-auto" />}
        {isIndia ? null : (
          <div className={`my-2 ${collapsed ? 'mx-1' : 'mx-2.5'} border-t border-[#E2E8F0]`} />
        )}

        {scenario === SCENARIO_CROSS_BORDER ? (
          <>
            <button
              type="button"
              title="Settings"
              aria-label="Settings. Reveal More and Workspace pages."
              aria-expanded={showMoreWorkspace}
              className={navItemClass(showMoreWorkspace, collapsed)}
              onClick={toggleCrossBorderSettings}
            >
              <Glyph
                name="menu-dots"
                className={`h-4 w-4 shrink-0 ${
                  showMoreWorkspace ? 'text-[#0B1324]' : 'text-[#94A3B8]'
                }`}
              />
              {collapsed ? null : (
                <span className="flex min-w-0 flex-1 items-center justify-between gap-2">
                  <span className="truncate text-[13px] tracking-[-0.01em]">Settings</span>
                  <span
                    className="text-[10px] font-semibold text-[#94A3B8]"
                    aria-hidden
                  >
                    {showMoreWorkspace ? '−' : '+'}
                  </span>
                </span>
              )}
            </button>
            {showMoreWorkspace ? (
              <>
                {sectionLabel('More', collapsed)}
                {moreNav.map((item) => renderNavItem(item, collapsed))}
                <div className={`my-2 ${collapsed ? 'mx-1' : 'mx-2.5'} border-t border-[#E2E8F0]`} />
                {sectionLabel('Workspace', collapsed)}
                {secondaryNav.map((item) => renderNavItem(item, collapsed))}
              </>
            ) : null}
          </>
        ) : null}
      </nav>
    )
  }

  return (
    /* Black top bar + white workspace with soft top corners into the black. */
    <div className="flex h-[100dvh] min-h-0 w-full flex-col overflow-hidden bg-[#000000]">
      <header className="payout-command-topbar z-50 shrink-0 text-white">
        <div className="flex h-[56px] w-full items-center gap-4 px-4 sm:px-5">
          <button
            type="button"
            onClick={() => setMobileNavOpen(true)}
            className="flex h-10 w-10 shrink-0 items-center justify-center rounded-[10px] border border-[#2F2F2F] bg-[#1C1C1C] text-[#D4D4D4] hover:bg-[#242424] lg:hidden"
            aria-label="Open navigation"
          >
            <svg className="h-4 w-4" viewBox="0 0 16 16" fill="none" aria-hidden>
              <path
                d="M2.5 4h11M2.5 8h11M2.5 12h11"
                stroke="currentColor"
                strokeWidth="1.6"
                strokeLinecap="round"
              />
            </svg>
          </button>

          <Link
            href="/"
            className="flex shrink-0 items-center focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-white/40"
            aria-label="Console home"
          >
            <ZordLogo size="lg" variant="dark" withWordmark />
          </Link>

          <nav
            className="hidden min-w-0 items-center gap-1 overflow-x-auto md:flex"
            aria-label="Product navigation"
          >
            {topProductLinks.map((link) => {
              const active = navActive(link.href, link.match(activeDock, pathname))
              const className = `zord-top-link relative flex h-[56px] shrink-0 items-center gap-1.5 px-2.5 text-[13px] transition-colors ${
                active
                  ? 'zord-top-link--active font-semibold text-white'
                  : 'font-medium text-[#A1A1AA] hover:text-white'
              }`
              const inner = (
                <>
                  <Glyph
                    name={link.icon}
                    className={`relative z-[1] h-4 w-4 ${active ? 'text-white' : 'text-[#71717A]'}`}
                  />
                  <span className="relative z-[1] whitespace-nowrap tracking-[-0.01em]">
                    {link.label}
                  </span>
                </>
              )

              if (link.href) {
                return (
                  <Link
                    key={link.id}
                    href={link.href}
                    prefetch
                    className={className}
                    aria-current={active ? 'page' : undefined}
                    onClick={(e) => {
                      e.preventDefault()
                      navigateTo(link.href!)
                    }}
                  >
                    {inner}
                  </Link>
                )
              }

              return (
                <button
                  key={link.id}
                  type="button"
                  onClick={() => {
                    if (link.dock) onDockChange(link.dock)
                  }}
                  className={className}
                  aria-current={active ? 'page' : undefined}
                >
                  {inner}
                </button>
              )
            })}
          </nav>

          <div className="ml-auto flex min-w-0 flex-1 items-center justify-end gap-2.5 pl-4">
            <ScenarioSwitcher />
            <div className="hidden shrink-0 lg:block">
              <ModeTogglePill onActivateClick={onActivateClick} compact />
            </div>
            <TopbarUtilityCluster
              alerts={visibleAlerts}
              alertCount={alertCount}
              deskRole="Ops supervisor"
              onDismissAlert={(id) => setDismissed((s) => new Set(s).add(id))}
            />
          </div>
        </div>
      </header>

      {/* Rounded top only - black shows in the corner cutouts, no side gutters. */}
      <div className="flex min-h-0 min-w-0 flex-1 overflow-hidden rounded-t-[20px] bg-[#FFFFFF]">
        <aside
          className={`payout-command-sidebar z-40 hidden h-full shrink-0 flex-col bg-[#FFFFFF] transition-[width] duration-200 ease-out lg:flex ${
            sidebarCollapsed ? 'w-[68px]' : scenario === SCENARIO_CROSS_BORDER ? 'w-[220px]' : 'w-[240px]'
          }`}
          aria-label="Console sidebar"
        >
          <div className="pc-nav-scroll flex min-h-0 flex-1 flex-col overflow-y-auto">
            {renderSidebarNav(sidebarCollapsed)}
          </div>
          <div
            className={`shrink-0 border-t border-[#E2E8F0] py-2 ${
              sidebarCollapsed ? 'flex justify-center' : 'px-2.5'
            }`}
          >
            <button
              type="button"
              onClick={toggleSidebarCollapsed}
              className="flex h-8 w-full items-center justify-center gap-2 rounded-[8px] text-[#94A3B8] transition hover:bg-[#E8EAED] hover:text-[#334155]"
              aria-label={sidebarCollapsed ? 'Expand sidebar' : 'Collapse sidebar'}
            >
              <svg className="h-4 w-4" viewBox="0 0 16 16" fill="none" aria-hidden>
                {sidebarCollapsed ? (
                  <path
                    d="M6 3.5 10.5 8 6 12.5"
                    stroke="currentColor"
                    strokeWidth="1.6"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  />
                ) : (
                  <path
                    d="M10 3.5 5.5 8 10 12.5"
                    stroke="currentColor"
                    strokeWidth="1.6"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  />
                )}
              </svg>
              {!sidebarCollapsed ? <span className="text-[12px] font-medium">Collapse</span> : null}
            </button>
          </div>
        </aside>

        {mobileNavOpen ? (
          <>
            <button
              type="button"
              className="fixed inset-0 z-[70] bg-slate-900/45 lg:hidden"
              aria-label="Close navigation"
              onClick={() => setMobileNavOpen(false)}
            />
            <aside className="fixed inset-y-0 left-0 z-[75] flex w-[min(18rem,88vw)] flex-col bg-white shadow-2xl lg:hidden">
              <div className="flex h-16 items-center justify-between bg-[linear-gradient(125deg,#050816_0%,#0A1628_55%,#0F3A4A_100%)] px-4">
                <ZordLogo size="lg" variant="dark" withWordmark />
                <button
                  type="button"
                  onClick={() => setMobileNavOpen(false)}
                  className="flex h-9 w-9 items-center justify-center rounded-none border border-[#0B1324]/20/35 bg-[#0B1F33]/60 text-[#0B1324] hover:border-[#0B1324]/20/55"
                  aria-label="Close menu"
                >
                  <span className="text-[18px] leading-none">×</span>
                </button>
              </div>
              <div className="flex min-h-0 flex-1 flex-col overflow-y-auto">
                {renderSidebarNav(false)}
              </div>
            </aside>
          </>
        ) : null}

        <div className="relative flex min-w-0 flex-1 flex-col overflow-hidden bg-[#FFFFFF]">
          <div className="min-h-0 flex-1 overflow-y-auto">{children}</div>
          {footer ? <div className="shrink-0">{footer}</div> : null}
        </div>
      </div>
    </div>
  )
}

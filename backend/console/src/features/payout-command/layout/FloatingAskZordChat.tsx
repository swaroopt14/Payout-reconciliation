'use client'

import Link from 'next/link'
import { useCallback, useEffect, useState } from 'react'
import { usePathname } from 'next/navigation'
import {
  ASK_ZORD_EXAMPLE_QUESTIONS,
  resolveAskZordDemo,
  SLASH_COMMANDS,
  type AskReply,
} from '@/services/payout-command/demo/askZordDemo'
import { isDemoQuery, isDemoSession } from '@/services/payout-command/demo/ycDemoConstants'

type Turn = { id: string; role: 'user' | 'assistant'; text?: string; reply?: AskReply }

/**
 * Scripted Ask Zord answers stay on demo/sandbox only.
 * Live finance routes must not show fixture numbers from this launcher.
 */
export function FloatingAskZordChat() {
  const pathname = usePathname()
  const [demoOnly, setDemoOnly] = useState(false)
  const [open, setOpen] = useState(false)
  const [input, setInput] = useState('')
  const [turns, setTurns] = useState<Turn[]>([])
  const [thinking, setThinking] = useState(false)
  const [step, setStep] = useState('')

  // Hide launcher on the full Ask Zord page (already has the main chat)
  const hideLauncher = pathname?.startsWith('/ask')

  useEffect(() => {
    const demoParam = new URLSearchParams(window.location.search).get('demo')
    setDemoOnly(isDemoSession() || isDemoQuery(demoParam))
  }, [pathname])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [])

  const run = useCallback((raw: string) => {
    const prompt = raw.trim()
    if (!prompt || thinking) return
    setTurns((prev) => [...prev, { id: `u_${Date.now()}`, role: 'user', text: prompt }])
    setInput('')
    setThinking(true)
    setStep('Retrieving structured finance sources…')
    const timers = [
      window.setTimeout(() => setStep('Comparing expected vs actual…'), 350),
      window.setTimeout(() => setStep('Validating evidence citations…'), 700),
      window.setTimeout(() => {
        const reply = resolveAskZordDemo(prompt, 'ask')
        setTurns((prev) => [...prev, { id: reply.id, role: 'assistant', reply }])
        setThinking(false)
        setStep('')
      }, 1100),
    ]
    return () => timers.forEach((t) => window.clearTimeout(t))
  }, [thinking])

  if (hideLauncher || !demoOnly) return null

  return (
    <>
      {!open ? (
        <button
          type="button"
          onClick={() => setOpen(true)}
          className="fixed bottom-6 right-6 z-[80] flex h-14 items-center gap-2 rounded-full bg-[#0B1324] px-4 text-white shadow-[0_12px_40px_rgba(15,23,42,0.35)] transition hover:scale-[1.02]"
          aria-label="Open Ask Zord"
        >
          <span className="flex h-8 w-8 items-center justify-center rounded-full bg-[#0066FF] text-[13px] font-bold">
            Z
          </span>
          <span className="pr-1 text-[13px] font-semibold">Ask Zord</span>
        </button>
      ) : null}

      {open ? (
        <div
          className="fixed bottom-6 right-6 z-[80] flex h-[min(640px,78vh)] w-[min(420px,calc(100vw-1.5rem))] flex-col overflow-hidden rounded-[16px] border border-[#E6E8EB] bg-white shadow-[0_20px_60px_rgba(15,23,42,0.25)]"
          role="dialog"
          aria-label="Ask Zord chat"
        >
          <div className="flex items-center justify-between border-b border-[#EEF0F3] bg-[#0B1324] px-4 py-3 text-white">
            <div>
              <p className="text-[14px] font-semibold">Ask Zord</p>
              <p className="text-[11px] text-white/60">Finance Controller · citations · no auto-dispatch</p>
            </div>
            <div className="flex items-center gap-1">
              <Link
                href="/ask?demo=sandbox"
                className="rounded-md px-2 py-1 text-[11px] font-medium text-white/80 hover:bg-white/10"
              >
                Full page
              </Link>
              <button
                type="button"
                onClick={() => setOpen(false)}
                className="flex h-8 w-8 items-center justify-center rounded-md text-[18px] text-white/80 hover:bg-white/10"
                aria-label="Close Ask Zord"
              >
                ×
              </button>
            </div>
          </div>

          <div className="min-h-0 flex-1 space-y-3 overflow-y-auto px-3 py-3">
            {turns.length === 0 && !thinking ? (
              <div className="space-y-3">
                <p className="text-[13px] font-medium text-[#1A1A1A]">What do you want to investigate?</p>
                <div className="flex flex-wrap gap-1.5">
                  {SLASH_COMMANDS.slice(0, 6).map((s) => (
                    <button
                      key={s.cmd}
                      type="button"
                      onClick={() => run(s.example)}
                      className="rounded-full border border-[#E8ECF4] bg-[#FAFBFC] px-2.5 py-1 text-[11px] font-semibold text-[#0B1324] hover:border-[#0066FF]/40 hover:text-[#0066FF]"
                    >
                      {s.cmd}
                    </button>
                  ))}
                </div>
                <ul className="space-y-1.5">
                  {ASK_ZORD_EXAMPLE_QUESTIONS.slice(0, 4).map((q) => (
                    <li key={q}>
                      <button
                        type="button"
                        onClick={() => run(q)}
                        className="w-full rounded-[8px] border border-[#EEF0F3] px-3 py-2 text-left text-[12px] text-[#334155] hover:border-[#DBEAFE] hover:bg-[#F8FBFF]"
                      >
                        “{q}”
                      </button>
                    </li>
                  ))}
                </ul>
              </div>
            ) : null}

            {turns.map((t) =>
              t.role === 'user' ? (
                <div key={t.id} className="flex justify-end">
                  <div className="max-w-[90%] rounded-2xl rounded-br-md bg-[#0B1324] px-3 py-2 text-[12px] leading-relaxed text-white">
                    {t.text}
                  </div>
                </div>
              ) : t.reply ? (
                <article key={t.id} className="max-w-[95%] space-y-2">
                  <div className="rounded-2xl rounded-bl-md border border-[#DBEAFE] bg-[#F8FBFF] px-3 py-2.5">
                    <p className="text-[10px] font-semibold uppercase tracking-[0.08em] text-[#0066FF]">Zord</p>
                    <pre className="mt-1 whitespace-pre-wrap font-sans text-[12px] leading-relaxed text-[#0B1324]">
                      {t.reply.finding}
                    </pre>
                    {t.reply.caveat ? (
                      <p className="mt-2 rounded-[6px] bg-white/80 px-2 py-1.5 text-[11px] text-[#64748B]">
                        {t.reply.caveat}
                      </p>
                    ) : null}
                  </div>
                  {t.reply.activity && t.reply.activity.length > 0 ? (
                    <div className="rounded-[8px] border border-[#EEF0F3] bg-[#FAFBFC] px-2.5 py-2">
                      <p className="text-[10px] font-semibold uppercase tracking-[0.08em] text-[#8F8F8F]">
                        Agent activity
                      </p>
                      <ul className="mt-1 space-y-0.5">
                        {t.reply.activity.map((a) => (
                          <li key={a} className="text-[11px] text-[#475569]">
                            ✓ {a}
                          </li>
                        ))}
                      </ul>
                    </div>
                  ) : null}
                  <div className="flex flex-wrap gap-1.5">
                    {t.reply.suggestedActions.slice(0, 4).map((a) =>
                      a.href ? (
                        <Link
                          key={a.label}
                          href={a.href}
                          className="rounded-full border border-[#E8ECF4] bg-white px-2.5 py-1 text-[11px] font-semibold text-[#0066FF]"
                        >
                          {a.label}
                        </Link>
                      ) : (
                        <button
                          key={a.label}
                          type="button"
                          onClick={() => run(a.label.replace(/^Run\s+/i, ''))}
                          className="rounded-full border border-[#E8ECF4] bg-white px-2.5 py-1 text-[11px] font-semibold text-[#0066FF]"
                        >
                          {a.label}
                        </button>
                      ),
                    )}
                  </div>
                </article>
              ) : null,
            )}

            {thinking ? (
              <div className="rounded-[8px] border border-[#DBEAFE] bg-[#F8FBFF] px-3 py-2 text-[12px] font-medium text-[#0066FF]">
                <span className="mr-2 inline-block h-1.5 w-1.5 animate-pulse rounded-full bg-[#0066FF]" />
                {step || 'Investigating…'}
              </div>
            ) : null}
          </div>

          <form
            className="border-t border-[#EEF0F3] p-3"
            onSubmit={(e) => {
              e.preventDefault()
              run(input)
            }}
          >
            <div className="flex gap-2">
              <input
                value={input}
                onChange={(e) => setInput(e.target.value)}
                placeholder="Ask about settlement, variance, cash…"
                className="h-10 min-w-0 flex-1 rounded-[8px] border border-[#E6E8EB] px-3 text-[13px] outline-none focus:border-[#0066FF]"
              />
              <button
                type="submit"
                disabled={thinking || !input.trim()}
                className="h-10 rounded-[8px] bg-[#0066FF] px-3 text-[13px] font-semibold text-white disabled:opacity-40"
              >
                Send
              </button>
            </div>
          </form>
        </div>
      ) : null}
    </>
  )
}

# Clearline decisions log

Every decision the Clearline team has made, with the reason behind it. Every bot and every human reads this before building or reviewing. If you want to change a decision, add a new entry that replaces the old one. Never quietly edit an old entry.

Source tags: when a decision rests on provider, bank, NPCI or RBI behavior, its reason is tagged like the Research casebook. **[official]** means a regulator, network, PSP or bank primary document, with a link. **[practice]** means a reputable secondary source, with a link. **[unknown]** means we couldn't verify it, so it's treated as merchant or PSP policy and is configurable, never hardcoded as fact.

Format: **D-number, title** (date, who decided). Then the decision, the reason, and what it rules out.

---

## Why this file exists

Decisions used to live only in chat rooms and in individual bots' memory. That caused three problems:

1. A bot that joined later, or one that lost context, rebuilt things that had already been ruled out (a second payout table, amount-only matching).
2. Reviewers had no single place to check a diff against, so HOLDs depended on who happened to remember the rule.
3. Swaroop had no way to see *why* the system is shaped the way it is without reading every chat.

A markdown file in the repo fixes all three. It goes through the same review and PR as the code, it shows up in `git blame`, any tool can read it, and it never expires the way chat context does. `docs/clearline-lessons.md` is its partner: decisions say what we chose, and lessons say what went wrong and the test that stops it happening again.

---

## Product

**D1. Clearline is merchant payment intelligence for finance ops** (2026-09-24, Swaroop and Architect)
- Clearline automates finance-ops work that sits on top of the PSP, bank, marketplace and ERP. The work is multi-way reconciliation, expected cash, refund and marketplace patterns, and plain-language counsel.
- Why: merchants already have systems that move money. What they're missing is the truth *across* those systems, plus early warnings.
- Rules out: Clearline becoming a ledger, a PSP, or a system of record for money movement.

**D2. First customers are in India: PSP plus UTR first, then card funding reports and marketplace sellers** (2026-09-24, Swaroop)
- Why: UTR (the unique bank transfer reference) gives a strong join key between PSP settlements and bank lines. Marketplaces have the biggest refund pain.
- Rules out: building for other countries' rails before India works end to end.

**D3. No Zord branding. The product name is Clearline** (2026-09-24, Swaroop)

## Architecture

**D4. The existing PSP, bank, ERP and marketplace systems stay the source of truth for money movement** (2026-09-24, Architect)
- Clearline reads from them and never becomes the book of record.
- Why: if two systems both claim to be the truth, reconciliation stops meaning anything.

**D5. The system has five planes: connectors, signal scheduler, recon truth and multi-way close, refund and marketplace graph, and insights and agents** (2026-09-24, Architect)
- Why: each plane has one job and one owner, so a change in one doesn't leak into the others.

**D6. There are two clocks: `psp_settled` is not `bank_credited`** (2026-09-24, Architect, Research and Finance)
- A provider saying "settled" is a promise. Bank cash is the credit on the bank statement. They're stored and shown as separate times.
- Why: settled money can take days to land, or fail to land. Treating it as cash overstates the balance.

**D7. The recon kernel's verdicts stay at six: MATCHED, AMBIGUOUS, UNRESOLVED, CONFLICTED, VARIANCE, ORPHAN** (Staff)
- New situations go into a separate `reason_code` field, not a new verdict.
- Why: every screen, report and test depends on these six. Adding more splits the truth.

**D8. There is only one payments table, one settlements table, one exceptions table and one close table** (Staff)
- New needs are met by adding columns or rows to the existing tables, or by migrating them in place.
- Why: two tables for the same thing drift apart, and then recon has to reconcile itself.

**D9. Agents and AI never write match results and never move money** (Architect and Staff)
- There is no LLM inside `ReconcilePayment`. Briefing and Ask numbers come only from recon and evidence.
- Why: a match or payout must be explainable and repeatable. A model's output is neither.

**D10. All amounts are int64 minor units (paise) end to end** (Staff and Finance)
- No float anywhere in a money path, and no rupees stored as NUMERIC.
- Why: floats round. ₹0.01 errors add up across thousands of payouts and break exact matching.

**D11. Data is scoped by `tenant_id` plus `connector_id`** (Staff)
- This holds until a phase explicitly adds `merchant_id`.
- Why: one scoping rule everywhere prevents cross-tenant leaks.

## Money rules (Finance)

**D12. `refund.processed` is an expected outflow, not cash** (Finance)
- It stays expected until a bank debit or a matched settlement refund line appears. Failed or cancelled refunds never reduce cash.
- Why: a provider status is not bank movement.

**D13. Each payment and amount is counted once** (Finance)
- An invoice due and an in-flight settlement for the same money must not both appear as inflow.
- Why: counting it twice doubles the expected cash.

**D14. Expected cash is a projection. It is never MATCHED** (Architect, mega-slice 2–6)
- Projections are labeled as projections everywhere, including the briefing.
- Why: a forecast shown as a fact leads a CFO to spend money that hasn't arrived.

**D15. A reversed or failed payout is never an expected debit** (Finance, Slice 8)
- A returned ₹1 is not new inflow and must not clear an invoice or an expected settlement.
- Why: a reversed payout moves no net money, so counting it either way distorts cash.

**D16. Pending is not failed** (Finance and Research, Slice 8)
- A pending or "deemed approved" payout stays expected until a bank debit or a confirmed failure or reversal. It is never re-sent on another rail.
- Why: IMPS code 91 (deemed approved) can still settle up to T+3 working days later. Re-sending it pays twice.

## Matching

**D17. MATCHED needs a strong key plus the amount** (Staff ruling, Slice 8)
- A strong key is a UTR or a bank or provider reference.
- An amount-only match is AMBIGUOUS at most, even when it's the only candidate and falls inside the date window. No candidate means UNRESOLVED.
- The confidence score can rank candidates, but it can never promote anything to MATCHED.
- Why: two different payouts of the same amount are common. The old 0.99 amount-only MATCHED at `financial_service.go:809` could pair the wrong money.

**D18. De-duplicate money items by their own id** (Finance, Slice 8)
- Payouts use the payout ref and refunds use the refund id, never the amount.
- Why: including the amount in the key lets a corrected amount create a second copy of the same item.

## Marketplace and refunds

**D19. `seller_id` is stored on refund facts** (Slice 1, PR #21)
- Why: marketplace refund patterns are meaningless without knowing which seller they belong to.

**D20. A marketplace refund is a graph event** (Architect and Marketplace)
- A refund should come with a transfer reversal. A refund without a reverse is an exception.
- Transfer edges carry no status of their own. Status comes from recon.
- Planned guard: only flag refund-without-reverse when a forward transfer edge exists for that payment and seller.
- Why: without the guard, direct (non-marketplace) refunds would be flagged wrongly.

**D21. Refund abuse is always an ops flag, with an optional checkout hold only if the merchant turns it on** (Architect and Marketplace)
- Agents never auto-block payouts. A pattern produces a recommendation plus an audit entry, or a hold the merchant configured.
- Why: blocking a seller's money is a business decision with legal weight. Clearline advises and the merchant decides.

**D22. Refund velocity is separate from payment fraud** (Research)
- Why: they have different signals and different owners. Mixing them hides both.

## Scheduling and calendar

**D23. The scheduler is driven by signals, with timers as backup** (mega-slice 2–6)
- Signals are ERP commits, refunds, bank file arrival, holidays and PSP settlement.
- Why: running recon when the data actually lands is faster and cheaper than polling. Timers catch missed signals.

**D24. The banking calendar is reference data only** (mega-slice 2–6)
- It has no money columns. The real RBI holiday list goes into a `bank_holiday` table with date, centre, name, source_url and fetched_at.
- Why: holidays shift expected dates. They never create or change money.

**D25. The scheduler never writes MATCHED** (Architect gate, mega-slice 2–6)
- Why: only the recon kernel decides verdicts (D9).

**D26. A backup pull of payouts and settlements runs on a timer** (Research and Cloud, Slice 8)
- It feeds the same de-duplicating intake as webhooks.
- Why: Razorpay turns a webhook off after 24 hours of failed deliveries. A timer pull keeps data flowing, and one shared intake means nothing gets counted twice.

## Payment path (Slice 8)

**D27. Dispatch stays off until Swaroop turns it on** (EM)
- Why: turning it on sends real money. That's the owner's call, not a bot's.

**D28. The router chooses the rail and relay never silently falls back** (EM and Architect)
- Relay sends the router the beneficiary type, amount and tenant, and stores the returned rail and reason on the existing dispatch row.
- If the router can't be reached, the payout is held as "needs rail". That is dispatch state only, never a recon verdict or a new table.
- Only payouts that never reached a rail can be re-routed.
- Why: the old silent IMPS fallback sent money on a rail nobody chose.

**D29. The ₹2 lakh switch from IMPS to NEFT is merchant policy, not law** (Research)
- The official limits are an RTGS minimum of ₹2 lakh, an IMPS maximum of ₹5 lakh per transaction, and no RBI cap on NEFT.
- Why: labeling policy as law stops merchants from changing it.

**D30. Every payout carries an `X-Payout-Idempotency` key** (Research, Go and Staff)
- There is one key per intent, taken from the intent id. It's saved as a column on the existing intent or dispatch row before the first call, and reused on every retry.
- It is required before dispatch is ever turned on.
- Why: RazorpayX requires it (since 15 Mar 2025), and a retry without it can pay twice.

**D31. Webhook replay protection uses processed event ids** (Research, Connectors and Staff)
- It reuses the existing idempotency store. If a new table is needed, it holds only event id, tenant, connector and received_at, with no amount or status.
- Why: Razorpay's HMAC signature has no timestamp, so a valid event can be replayed.

**D32. No secrets in code** (Slice 8)
- Secrets live in env with placeholders. Webhook secrets are encrypted, and there's no shared Razorpay fallback across tenants.
- Why: a leaked Slack webhook and shared secrets were found in the audit.

## How we work

**D33. Bots edit locally and Swaroop does all the git work** (Swaroop)
- Swaroop does every `git add`, commit, push and PR.
- Why: a human stays in control of what reaches the repo.

**D34. Only EM assigns work, and the review chain stops at the first HOLD** (Swaroop and EM)
- The order is Research, Architect, the language owner, Finance, Test, then Staff.
- Why: domain truth first, then design, then code, then money, tests and kernel.

**D35. Slices are product-sized, with one PR per slice and tests as a pack at the end** (Swaroop)
- Why: tiny cuts produced review overhead without shipping anything usable.

**D36. The CLI comes last** (Swaroop)

**D37. Every HOLD or bug adds a line to `docs/clearline-lessons.md`** (EM, 2026-09-25)
- Each line gives the owner, the rule, and the test that guards it. Every bot reads the lessons and this file before building or reviewing.
- Why: the same class of mistake kept coming back. A written rule with a test stops it for good.

## Slice 8 additions (2026-09-25, EM)

**D38. Paise storage is added alongside the old columns, not converted in place** (EM)
- Each money column gets a `<col>_minor BIGINT` twin on the same table. The code writes both, an exact backfill fills the old rows, and reads use the minor column first with a metric that counts mismatches.
- NUMERIC columns are dropped only in a later, approved slice.
- This refines D10 and replaces the "convert in place" wording from Staff's Slice 8 limit. There is still no second table.
- Why: `ALTER TYPE` rewrites and locks `canonical_intents`. Adding a column still keeps one table per thing (D8).

**D39. Only a new `PAYOUT_APPROVER` role can approve held payouts** (EM)
- `CUSTOMER_ADMIN` and API keys can't approve them.
- Why: every signup gets `CUSTOMER_ADMIN`, so that role gives no real control.

**D40. Rail limits live in the router's code** (EM)
- RTGS needs at least ₹2 lakh. IMPS allows up to ₹5 lakh. UPI defaults to ₹1 lakh (the NPCI general limit, configurable). NEFT has no cap.
- A tenant's force rule applies only to that tenant's own rules. A force outside the limits returns 422.
- Why: a legal limit must never be overridden without anyone noticing. This works alongside D29, where the ₹2 lakh IMPS-to-NEFT switch is policy.

**D41. A file intent is an instruction, not money** (EM and Finance)
- It counts only once a provider payout links to it by `reference_id`. A provider payout with no intent still counts.
- Why: each payment is counted once (D13).

**D42. Cash-schedule bank drops are reference first** (EM)
- An amount-only drop happens only when there's exactly one candidate in the window, and it's labelled as amount-only. With two or more candidates, the line stays.
- Why: this is the expected list, not a verdict. D17 still decides MATCHED.

**D43. Conditions for turning dispatch on** (EM)
- Dispatch can't be turned on until all of these are in place:
  - the paise fix is in
  - the idempotency key is in place (D30)
  - `router_url` is set, with no fallback (D28)
  - an intent that was sent but not yet observed shows as an expected outflow, keyed by intent id and replaced when its payout links
- Why: Go found relay treating rupees as paise, and Finance's rule is that a gap must never make cash look higher than it is.

**D44. `dispatch_index` must record every relay dispatch** (EM)
- The `connector_id` fix is additive.
- Why: the UUID column rejected `'razorpayx-v1'`, so every insert failed and recon couldn't link dispatches.

## Slice 8 amendments (2026-09-25, EM with Staff, Research and Marketplace)

**D45. Rail limits are tagged by source (amends D40)** (EM and Research)
- RTGS minimum ₹2 lakh [official] (RBI RTGS FAQ: https://www.rbi.org.in/Scripts/FAQView.aspx?Id=65).
- IMPS up to ₹5 lakh per transaction [official] (RBI statement, Oct 2021: https://www.rbi.org.in/Scripts/BS_PressReleaseDisplay.aspx?prid=52368).
- NEFT has no RBI cap [official] (RBI NEFT FAQ: https://www.rbi.org.in/Scripts/FAQView.aspx?Id=60), but a bank may set its own limit.
- The UPI limit for payouts is [unknown] as a single rule. It's merchant or PSP policy, set per connector, with a default of ₹1 lakh, and labelled as policy.
- A PSP account cap shows up at runtime as a reason code (for example `amount_limit_exhausted_neft`). It is never hardcoded.
- Why: only [official] limits belong in router code as hard limits. Everything else is configuration, so it can change without a deploy and is never passed off as law.

**D46. `_minor` columns are the only source of truth for money (amends D38)** (EM, with Staff and Research agreeing)
- Recon matching, expected cash and every API total read only the `_minor` columns, with no fallback to NUMERIC.
- `_minor` becomes NOT NULL after the backfill.
- NUMERIC columns are read by nothing and are dropped only after the mismatch metric reads zero, in an approved slice.
- The migration test proves exact conversion for 0.01 (1 paise) and 100000.10 (10000010 paise).
- Why: while two columns exist, only one can be the truth. A fallback would bring float or rounding errors back in without anyone noticing (see L3 and L6).

## Marketplace decisions (2026-09-25, Marketplace, recorded by EM)

**D47. Refund-without-reversal is worked out at read time through the existing exceptions path** (Marketplace)
- There's no stored exception table or new verdict for it.
- It shows as UNRESOLVED with 0 variance and 0 financial impact, and is never MATCHED.
- Why: one table per thing (D8) and six verdicts (D7). A stored row would go stale because nothing deletes it, while a derived one clears on its own when the reversal lands.
- Guarding tests: `TestRefundWithoutReverse_ThroughExceptionsPath`, `TestRefundGraphException_CashUnchanged`.

**D48. Never guess a seller** (Marketplace)
- A refund with no `seller_id` is never given one based on amount, timing or likely matches. It stays without a seller and is checked at payment level.
- When a payment went to two or more sellers, the refund stays out of seller patterns and the reversal check.
- Why: the old code picked the first seller and put refunds on the wrong one.
- Guarding test: `TestSellerIDFromTransfers_MultiSellerLeavesEmpty`.

**D49. A reversal proves only its own seller** (Marketplace)
- A transfer reversal from seller A proves the refund was recovered from seller A only. It says nothing about other sellers on the same payment.
- Why: split payments have several sellers. One reversal must not clear the others' exposure. Go found split-payment stamping was hiding real missing reversals. See Razorpay Route reversals [official]: https://razorpay.com/docs/build/llm-docs/payments/route/reversal.md
- Guarding test: `TestRefundGraph_SplitPaymentOtherSellerReversedStillFlags`.

**D50. The reversal grace period is merchant policy** (Marketplace and Research)
- The window before a missing reversal is flagged is [unknown] from the provider, because Razorpay doesn't document reversal timing and has no reversal webhook. It's a tenant+connector setting, default 0, labelled as merchant policy.
- Why: we don't invent provider behavior. The merchant knows their own reversal habits.

**D51. An empty paise value is never summed as zero (amends D46)** (Finance)
- The backfill must finish before anything reads the `_minor` columns.
- Until then, a row with an empty `_minor` value raises an error or counts as a mismatch. It is never treated as 0 in a total.
- Why: a ₹1 payout with an empty paise value would drop out of the total and make cash look ₹1 higher than it is.
- Guarding test: TODO (Slice 8), a total over one row that hasn't been copied fails or reports a mismatch.

**D52. Recon gets each tenant's PSP credentials from edge over an internal endpoint** (Architect, Slice 8, proposed by Connectors)
- Edge serves `GET /internal/v1/tenants/:tenant_id/connectors/:connector_id/razorpay-credentials`. It's protected by a dedicated `RECON_CREDENTIALS_TOKEN` (never the relay token) plus a tenant header that must match the path. It returns `Cache-Control: no-store`, and each fetch writes an audit line with ids only.
- Recon never reads edge's database. It caches credentials in memory only, for 5 minutes, and drops them on a Razorpay 401. With no tenant secret it fails closed, and it never falls back to the platform key.
- If credentials are missing or edge is down, recon skips enrichment but still stores the refund fact. A skipped refund stays out of seller patterns and is never flagged refund-without-reversal. The D26 pull re-enriches it later, and the skip count is visible as a metric and a briefing data-gap line.
- TLS on `/internal/v1/*` is a condition for live mode (adds to D43). mTLS is recommended.
- Why: edge stays the one source of truth for secrets. Env-per-tenant spreads secrets and needs a redeploy for each tenant, and pushing them through Kafka puts them in broker logs. Degrading instead of blocking means a credentials outage can't stall the refund consumer.

**D53. Narrows D44: `dispatch_index` records every dispatch that reaches a PSP call** (EM, Slice 8)
- Every dispatch that reaches a PSP call has exactly one `dispatch_index` row.
- A `CONNECTOR_UNRESOLVED` hold never reaches a PSP, so it lives only in relay's dispatch row and the HELD event.
- `connector_id` stays a UUID and is NOT NULL. The slug (`con_razorpay_<mode>_<8hex>`) goes only in `connector_ref`.
- Relay resolves the UUID per tenant through edge's internal route `GET /internal/v1/tenants/:tenant_id/connectors/resolve` and fails closed on 404. That route returns ids and mode only, never a secret or a ref. It accepts the relay or recon token. The credentials route (D52) accepts only the recon token, and a test proves the relay token gets 401 there. Both routes check that the tenant header matches the path and write an audit line with ids only.
- Why: an index row for a payout that never reached a PSP would make recon expect a provider record that will never exist. A slug in a UUID column is what left `dispatch_index` empty (L7).

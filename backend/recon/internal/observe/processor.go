package observe

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"zord-outcome-engine/internal/paymenttruth"
	"zord-outcome-engine/internal/payouttruth"
	"zord-outcome-engine/internal/poll"
	"zord-outcome-engine/internal/poll/providers/razorpay"
	"zord-outcome-engine/internal/recon"
)

type ResultKind string

const (
	ResultSkipped   ResultKind = "skipped"
	ResultInserted  ResultKind = "inserted"
	ResultUpdated   ResultKind = "updated"
	ResultDuplicate ResultKind = "duplicate"
	ResultIgnored   ResultKind = "ignored"
)

type Result struct {
	Kind      ResultKind
	PaymentID string
	PayoutID  string
	RefundID  string
	EventType string
}

type RefundSink interface {
	UpsertRefund(ctx context.Context, tenantID, connectorID string, r recon.RefundFact) (recon.RefundFact, error)
}

// TransferLookup optionally resolves payment→transfers for seller_id enrichment
// on the API/backfill observe path. Webhook-only leaves SellerID null unless
// already present on the envelope.
type TransferLookup interface {
	ListTransfersForPayment(ctx context.Context, paymentID string) ([]razorpay.TransferResponse, error)
}

// TransferReversalLookup optionally resolves transfer→reversals so observe can
// set reverse_transfer_id on the same marketplace_transfer_edges row.
type TransferReversalLookup interface {
	ListReversalsForTransfer(ctx context.Context, transferID string) ([]razorpay.ReversalResponse, error)
}

// TransferEdgeSink persists reference-only marketplace transfer graph edges.
// amount_minor is correlation only — never bank cash / never MATCHED.
type TransferEdgeSink interface {
	UpsertTransferEdge(ctx context.Context, tenantID, connectorID string, e recon.MarketplaceTransferEdge) (recon.MarketplaceTransferEdge, error)
}

type Processor struct {
	store   poll.Store
	truth   *paymenttruth.Processor
	payouts *payouttruth.Processor
	Refunds RefundSink
	// Transfers is a tenant-agnostic lookup kept as a test seam only.
	// Production wires TransfersFor (D52: each tenant's own key).
	Transfers TransferLookup
	// TransfersFor returns the lookup for one tenant connector, built from
	// that tenant's own credentials (D52). poll.ErrNoTenantSecret means the
	// tenant has none; any other error means the source is unavailable.
	// Either way enrichment is skipped, the refund still lands, and the row
	// is marked skipped_* so it is not mistaken for "no marketplace".
	TransfersFor func(ctx context.Context, tenantID, connectorID, mode string) (TransferLookup, error)
	// CredentialInvalidator drops a tenant's cached key pair on Razorpay 401.
	CredentialInvalidator poll.CredentialInvalidator
	Edges                 TransferEdgeSink
}

// SkippedRefundLister lists refunds whose enrichment was skipped (D52).
type SkippedRefundLister interface {
	ListSkippedEnrichmentRefunds(ctx context.Context, tenantID, connectorID string, limit int) ([]recon.RefundFact, error)
}

func NewProcessor(store poll.Store) *Processor {
	p := &Processor{store: store}
	if ts, ok := store.(paymenttruth.Store); ok {
		p.truth = paymenttruth.NewProcessor(ts)
	}
	if ps, ok := store.(payouttruth.Store); ok {
		p.payouts = payouttruth.NewProcessor(ps)
	}
	return p
}

func (p *Processor) ApplyBytes(ctx context.Context, raw []byte) (Result, error) {
	env, err := ParseEnvelope(raw)
	if err != nil {
		return Result{Kind: ResultIgnored}, nil
	}
	return p.Apply(ctx, env)
}

func (p *Processor) Apply(ctx context.Context, env Envelope) (Result, error) {
	if p == nil || p.store == nil {
		return Result{}, fmt.Errorf("observation processor not configured")
	}
	if !env.IsProviderObservation() {
		return Result{Kind: ResultIgnored}, nil
	}
	item, ok, err := NormalizePayment(env)
	if err != nil {
		return Result{Kind: ResultSkipped, EventType: env.ProviderEventType}, nil
	}
	if !ok {
		if edgeRes, handled, err := p.applyTransferEdge(ctx, env); handled {
			return edgeRes, err
		}
		return p.applyRefundOrPayout(ctx, env)
	}
	if strings.TrimSpace(env.TenantID) == "" || strings.TrimSpace(env.ConnectorID) == "" {
		return Result{}, fmt.Errorf("missing tenant_id or connector_id")
	}
	mode := env.ProviderMode
	if mode != "live" {
		mode = "test"
	}
	provider := env.Provider
	if provider == "" {
		provider = "razorpay"
	}
	if p.truth != nil {
		obs, err := paymenttruth.MapNeutral(env.TenantID, env.ConnectorID, provider, mode, SourceWebhook, env.ProviderEventID, env.ReceiptID, item, false, time.Now().UTC())
		if err != nil {
			return Result{}, err
		}
		res, err := p.truth.Process(ctx, obs)
		if err != nil {
			return Result{}, err
		}
		out := Result{PaymentID: item.PaymentID, EventType: env.ProviderEventType, Kind: mapTruthKind(res.Kind)}
		return out, nil
	}
	return p.applyLegacy(ctx, env, item, provider, mode)
}

func (p *Processor) applyRefundOrPayout(ctx context.Context, env Envelope) (Result, error) {
	item, ok, err := NormalizeRefund(env)
	if err != nil {
		return Result{Kind: ResultSkipped, EventType: env.ProviderEventType}, nil
	}
	if ok {
		if p.Refunds == nil {
			return Result{Kind: ResultSkipped, EventType: env.ProviderEventType, RefundID: item.RefundID}, nil
		}
		if strings.TrimSpace(env.TenantID) == "" || strings.TrimSpace(env.ConnectorID) == "" {
			return Result{}, fmt.Errorf("missing tenant_id or connector_id")
		}
		status, err := p.enrichRefundAndUpsertEdges(ctx, env.TenantID, env.ConnectorID, env.ProviderMode, &item)
		if err != nil {
			return Result{}, err
		}
		fact := MapRefundFact(item)
		fact.EnrichmentStatus = status
		saved, err := p.Refunds.UpsertRefund(ctx, env.TenantID, env.ConnectorID, fact)
		if err != nil {
			return Result{}, err
		}
		return Result{Kind: ResultInserted, RefundID: saved.RefundID, PaymentID: saved.PaymentID, EventType: env.ProviderEventType}, nil
	}
	return p.applyPayout(ctx, env)
}

// enrichRefundAndUpsertEdges lists payment→transfers when seller_id is empty
// and/or Edges sink is wired. Seller enrichment never invents; edges are
// reference-only (amount_minor correlation). Reverse ids are filled when the
// TransferLookup also implements TransferReversalLookup.
//
// It returns the refund's enrichment_status (D52):
//   - "" when enrichment was not needed or no lookup is wired at all
//   - enriched / no_transfers after a successful lookup
//   - skipped_no_creds / skipped_unavailable when the tenant's lookup could
//     not be built; the refund still lands (degrade, don't block).
//
// A Razorpay API error keeps the old behaviour (returned, so the message is
// retried); a 401 also drops the tenant's cached credentials.
func (p *Processor) enrichRefundAndUpsertEdges(ctx context.Context, tenantID, connectorID, mode string, item *reconRefund) (string, error) {
	if item == nil {
		return "", nil
	}
	paymentID := strings.TrimSpace(item.PaymentID)
	needSeller := strings.TrimSpace(item.SellerID) == ""
	needEdges := p.Edges != nil
	if paymentID == "" || (!needSeller && !needEdges) {
		return "", nil
	}
	if mode != "live" {
		mode = "test"
	}
	lookup := p.Transfers
	if p.TransfersFor != nil {
		l, err := p.TransfersFor(ctx, tenantID, connectorID, mode)
		if err != nil || l == nil {
			status := recon.EnrichmentSkippedUnavailable
			if errors.Is(err, poll.ErrNoTenantSecret) {
				status = recon.EnrichmentSkippedNoCreds
			}
			poll.ObserveEnrichmentSkipped(status)
			// ids + reason code only; never a key or an upstream message.
			log.Printf("WARN observe: transfer enrichment skipped tenant_id=%s connector_id=%s mode=%s refund_id=%s reason=%s",
				tenantID, connectorID, mode, item.RefundID, status)
			return status, nil
		}
		lookup = l
	}
	if lookup == nil {
		return "", nil
	}
	transfers, err := lookup.ListTransfersForPayment(ctx, paymentID)
	if err != nil {
		p.invalidateOnUnauthorized(err, tenantID, connectorID, mode)
		return "", err
	}
	if needSeller {
		EnrichRefundWithTransfers(item, transfers)
	}
	if needEdges {
		if err := p.upsertEdgesFromTransfersWith(ctx, lookup, tenantID, connectorID, item.PaymentID, item.RefundID, item.SellerID, transfers); err != nil {
			p.invalidateOnUnauthorized(err, tenantID, connectorID, mode)
			return "", err
		}
	}
	if len(transfers) == 0 {
		return recon.EnrichmentNoTransfers, nil
	}
	return recon.EnrichmentEnriched, nil
}

// CredentialInvalidator (optional) is told when Razorpay returns 401 for a
// tenant, so its cached key pair is dropped (D52).
func (p *Processor) invalidateOnUnauthorized(err error, tenantID, connectorID, mode string) {
	var pErr *razorpay.ProviderError
	if p.CredentialInvalidator != nil && errors.As(err, &pErr) && pErr.Kind == razorpay.ErrUnauthorized {
		p.CredentialInvalidator.Invalidate(tenantID, connectorID, mode)
	}
}

// ReEnrichSkipped re-runs transfer enrichment for up to limit refunds in one
// tenant+connector whose enrichment was skipped (D52). Called by the D26
// pull once that tenant's credentials work. Rows that are still skipped keep
// their status; enriched rows get seller_id (when single-seller), edges and
// status enriched / no_transfers. Returns how many rows left skipped_*.
func (p *Processor) ReEnrichSkipped(ctx context.Context, tenantID, connectorID, mode string, limit int) (int, error) {
	if p == nil || p.Refunds == nil {
		return 0, nil
	}
	lister, ok := p.Refunds.(SkippedRefundLister)
	if !ok {
		return 0, nil
	}
	skipped, err := lister.ListSkippedEnrichmentRefunds(ctx, tenantID, connectorID, limit)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, f := range skipped {
		item := reconRefund{
			RefundID: f.RefundID, PaymentID: f.PaymentID, AmountMinor: f.AmountMinor,
			Currency: f.Currency, ProviderStatus: f.ProviderStatus, Source: f.Source, SellerID: f.SellerID,
		}
		status, err := p.enrichRefundAndUpsertEdges(ctx, tenantID, connectorID, mode, &item)
		if err != nil {
			return done, err
		}
		if status == "" || strings.HasPrefix(status, "skipped_") {
			continue
		}
		fact := MapRefundFact(item)
		fact.ID = f.ID
		fact.EnrichmentStatus = status
		if _, err := p.Refunds.UpsertRefund(ctx, tenantID, connectorID, fact); err != nil {
			return done, err
		}
		done++
	}
	return done, nil
}

func (p *Processor) upsertEdgesFromTransfers(ctx context.Context, tenantID, connectorID, paymentID, refundID, refundSellerID string, transfers []razorpay.TransferResponse) error {
	return p.upsertEdgesFromTransfersWith(ctx, p.Transfers, tenantID, connectorID, paymentID, refundID, refundSellerID, transfers)
}

func (p *Processor) upsertEdgesFromTransfersWith(ctx context.Context, lookup TransferLookup, tenantID, connectorID, paymentID, refundID, refundSellerID string, transfers []razorpay.TransferResponse) error {
	if p == nil || p.Edges == nil {
		return nil
	}
	edges := MarketplaceEdgesFromTransfers(paymentID, refundID, refundSellerID, transfers)
	var revLookup TransferReversalLookup
	if r, ok := lookup.(TransferReversalLookup); ok {
		revLookup = r
	}
	for _, e := range edges {
		if revLookup != nil && strings.TrimSpace(e.TransferID) != "" {
			revs, err := revLookup.ListReversalsForTransfer(ctx, e.TransferID)
			if err != nil {
				return err
			}
			if len(revs) > 0 {
				r0 := revs[0]
				e.ReverseTransferID = strings.TrimSpace(r0.ID)
				e.ReverseAt = razorpay.TransferCreatedAt(r0.CreatedAt)
				// Razorpay customer_refund_id on the reversal is authoritative.
				if crid := strings.TrimSpace(r0.CustomerRefundID); crid != "" {
					e.RefundID = crid
					e.RefundIDFromReversal = true
				}
			}
		}
		if _, err := p.Edges.UpsertTransferEdge(ctx, tenantID, connectorID, e); err != nil {
			return err
		}
	}
	return nil
}

func (p *Processor) applyTransferEdge(ctx context.Context, env Envelope) (Result, bool, error) {
	e, ok, err := NormalizeTransferEdge(env)
	if err != nil {
		if isReversalEvent(env.ProviderEventType, env.ProviderEntityType) {
			log.Printf("WARN observe: skipping reversal without transfer_id/reverse id tenant=%s connector=%s entity_id=%q err=%v",
				env.TenantID, env.ConnectorID, env.ProviderEntityID, err)
		}
		return Result{Kind: ResultSkipped, EventType: env.ProviderEventType}, true, nil
	}
	if !ok {
		return Result{}, false, nil
	}
	if p.Edges == nil {
		return Result{Kind: ResultSkipped, EventType: env.ProviderEventType, PaymentID: e.PaymentID}, true, nil
	}
	if strings.TrimSpace(env.TenantID) == "" || strings.TrimSpace(env.ConnectorID) == "" {
		return Result{}, true, fmt.Errorf("missing tenant_id or connector_id")
	}
	saved, err := p.Edges.UpsertTransferEdge(ctx, env.TenantID, env.ConnectorID, e)
	if err != nil {
		return Result{}, true, err
	}
	kind := ResultInserted
	if strings.TrimSpace(saved.ReverseTransferID) != "" {
		kind = ResultUpdated
	}
	return Result{Kind: kind, PaymentID: saved.PaymentID, EventType: env.ProviderEventType}, true, nil
}

func (p *Processor) applyPayout(ctx context.Context, env Envelope) (Result, error) {
	item, ok, err := NormalizePayout(env)
	if err != nil {
		return Result{Kind: ResultSkipped, EventType: env.ProviderEventType}, nil
	}
	if !ok {
		return Result{Kind: ResultSkipped, EventType: env.ProviderEventType}, nil
	}
	if strings.TrimSpace(env.TenantID) == "" || strings.TrimSpace(env.ConnectorID) == "" {
		return Result{}, fmt.Errorf("missing tenant_id or connector_id")
	}
	if p.payouts == nil {
		return Result{Kind: ResultSkipped, EventType: env.ProviderEventType, PayoutID: item.PayoutID}, nil
	}
	provider := env.Provider
	if provider == "" {
		provider = "razorpay"
	}
	obs, err := payouttruth.MapNeutral(env.TenantID, env.ConnectorID, provider, SourceWebhook, env.ProviderEventID, item, time.Now().UTC())
	if err != nil {
		return Result{}, err
	}
	res, err := p.payouts.Process(ctx, obs)
	if err != nil {
		return Result{}, err
	}
	return Result{PayoutID: item.PayoutID, PaymentID: item.PayoutID, EventType: env.ProviderEventType, Kind: mapPayoutKind(res.Kind)}, nil
}

func mapPayoutKind(kind payouttruth.Kind) ResultKind {
	switch kind {
	case payouttruth.KindDuplicate:
		return ResultDuplicate
	case payouttruth.KindUpdated, payouttruth.KindObserved:
		return ResultUpdated
	default:
		return ResultInserted
	}
}

func mapTruthKind(kind paymenttruth.Kind) ResultKind {
	switch kind {
	case paymenttruth.KindDuplicate:
		return ResultDuplicate
	case paymenttruth.KindUpdated, paymenttruth.KindObserved:
		return ResultUpdated
	default:
		return ResultInserted
	}
}

func (p *Processor) applyLegacy(ctx context.Context, env Envelope, item razorpay.NeutralPayment, provider, mode string) (Result, error) {
	var result Result
	err := p.store.RunInTx(ctx, func(ctx context.Context) error {
		upsert, err := p.store.UpsertPayment(ctx, poll.PaymentObservation{
			TenantID:     env.TenantID,
			ConnectorID:  env.ConnectorID,
			Provider:     provider,
			ProviderMode: mode,
			Item:         item,
			ReceiptID:    env.ReceiptID,
			Source:       SourceWebhook,
			Sources:      []string{SourceWebhook},
		})
		if err != nil {
			return err
		}
		result = Result{PaymentID: item.PaymentID, EventType: env.ProviderEventType}
		switch upsert {
		case poll.UpsertDuplicate:
			result.Kind = ResultDuplicate
			return nil
		case poll.UpsertUpdated:
			result.Kind = ResultUpdated
		default:
			result.Kind = ResultInserted
		}
		row, err := poll.PaymentOutboxRow(env.TenantID, env.ConnectorID, item.PaymentID, SourceWebhook, item)
		if err != nil {
			return err
		}
		return p.store.InsertOutbox(ctx, row)
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

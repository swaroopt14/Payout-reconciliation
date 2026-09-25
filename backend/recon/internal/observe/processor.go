package observe

import (
	"context"
	"fmt"
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
	store     poll.Store
	truth     *paymenttruth.Processor
	payouts   *payouttruth.Processor
	Refunds   RefundSink
	Transfers TransferLookup
	Edges     TransferEdgeSink
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
		if err := p.enrichRefundAndUpsertEdges(ctx, env.TenantID, env.ConnectorID, &item); err != nil {
			return Result{}, err
		}
		saved, err := p.Refunds.UpsertRefund(ctx, env.TenantID, env.ConnectorID, MapRefundFact(item))
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
func (p *Processor) enrichRefundAndUpsertEdges(ctx context.Context, tenantID, connectorID string, item *reconRefund) error {
	if item == nil {
		return nil
	}
	paymentID := strings.TrimSpace(item.PaymentID)
	needSeller := strings.TrimSpace(item.SellerID) == ""
	needEdges := p.Edges != nil
	if paymentID == "" || p.Transfers == nil || (!needSeller && !needEdges) {
		return nil
	}
	transfers, err := p.Transfers.ListTransfersForPayment(ctx, paymentID)
	if err != nil {
		return err
	}
	if needSeller {
		EnrichRefundWithTransfers(item, transfers)
	}
	if needEdges {
		return p.upsertEdgesFromTransfers(ctx, tenantID, connectorID, item.PaymentID, item.RefundID, transfers)
	}
	return nil
}

func (p *Processor) upsertEdgesFromTransfers(ctx context.Context, tenantID, connectorID, paymentID, refundID string, transfers []razorpay.TransferResponse) error {
	if p == nil || p.Edges == nil {
		return nil
	}
	edges := MarketplaceEdgesFromTransfers(paymentID, refundID, transfers)
	var revLookup TransferReversalLookup
	if r, ok := p.Transfers.(TransferReversalLookup); ok {
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
				if strings.TrimSpace(e.RefundID) == "" {
					e.RefundID = strings.TrimSpace(r0.CustomerRefundID)
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

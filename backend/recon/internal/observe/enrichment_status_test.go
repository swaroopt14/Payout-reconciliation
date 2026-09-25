package observe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"zord-outcome-engine/internal/poll"
	"zord-outcome-engine/internal/poll/providers/razorpay"
	"zord-outcome-engine/internal/recon"
)

const leakSecret = "fake_tenant_secret_DO_NOT_LEAK_91c2"

func refundEnv(t *testing.T, refundID, paymentID string) Envelope {
	t.Helper()
	env := capturedEnvelope(t)
	env.ProviderEventType = "refund.created"
	env.ProviderEntityType = "refund"
	env.ProviderEntityID = refundID
	env.PaymentID = paymentID
	env.Amount = 900
	env.SellerID = ""
	return env
}

func logsTo(t *testing.T) *bytes.Buffer {
	t.Helper()
	var b bytes.Buffer
	log.SetOutput(&b)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &b
}

func onlyRefund(t *testing.T, sink *recon.MemoryFinancialStore, env Envelope) recon.RefundFact {
	t.Helper()
	got, err := sink.ListRefunds(context.Background(), env.TenantID, env.ConnectorID, env.PaymentID)
	if err != nil || len(got) != 1 {
		t.Fatalf("refund must land exactly once: %+v err=%v", got, err)
	}
	return got[0]
}

// D52 / 6a: no creds -> refund lands, marked skipped_no_creds (not "no
// marketplace"), no seller, no edges, metric + per-tenant WARN, no secret.
func TestApplyRefund_NoTenantCredsSkipsEnrichmentAndMarksRow(t *testing.T) {
	logs := logsTo(t)
	sink := recon.NewMemoryFinancialStore()
	p := NewProcessor(poll.NewMemoryStore())
	p.Refunds, p.Edges = sink, sink
	p.Transfers = &stubTransfers{items: []razorpay.TransferResponse{{ID: "trf_platform", Recipient: "acc_platform"}}}
	var gotMode string
	p.TransfersFor = func(_ context.Context, _, _, mode string) (TransferLookup, error) {
		gotMode = mode
		return nil, poll.ErrNoTenantSecret
	}
	before := poll.EnrichmentSkippedTotal(recon.EnrichmentSkippedNoCreds)
	env := refundEnv(t, "rfnd_nocreds", "pay_nocreds")
	env.ProviderMode = "live"
	if _, err := p.Apply(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	r := onlyRefund(t, sink, env)
	if r.EnrichmentStatus != recon.EnrichmentSkippedNoCreds || r.SellerID != "" {
		t.Fatalf("status=%q seller=%q", r.EnrichmentStatus, r.SellerID)
	}
	if gotMode != "live" {
		t.Fatalf("mode must pass through from the envelope, got %q", gotMode)
	}
	edges, _ := sink.ListTransferEdges(context.Background(), env.TenantID, env.ConnectorID)
	if len(edges) != 0 {
		t.Fatal("no edges may be written when enrichment is skipped (and never via the platform seam)")
	}
	if poll.EnrichmentSkippedTotal(recon.EnrichmentSkippedNoCreds) != before+1 {
		t.Fatal("observe_enrichment_skipped_total{reason=skipped_no_creds} must increment")
	}
	l := logs.String()
	if !strings.Contains(l, "transfer enrichment skipped") || !strings.Contains(l, "tenant_id="+env.TenantID) || !strings.Contains(l, "reason=skipped_no_creds") {
		t.Fatalf("per-tenant WARN missing: %q", l)
	}
}

func TestApplyRefund_CredSourceDownSkipsUnavailableWithoutLeak(t *testing.T) {
	logs := logsTo(t)
	sink := recon.NewMemoryFinancialStore()
	p := NewProcessor(poll.NewMemoryStore())
	p.Refunds, p.Edges = sink, sink
	p.TransfersFor = func(context.Context, string, string, string) (TransferLookup, error) {
		// Even if an upstream error text carried a secret, it is never logged.
		return nil, fmt.Errorf("%w: upstream said %s", poll.ErrCredentialSourceUnavailable, leakSecret)
	}
	env := refundEnv(t, "rfnd_down", "pay_down")
	res, err := p.Apply(context.Background(), env)
	if err != nil {
		t.Fatalf("a credentials outage must not block the refund: %v", err)
	}
	if r := onlyRefund(t, sink, env); r.EnrichmentStatus != recon.EnrichmentSkippedUnavailable {
		t.Fatalf("status=%q", r.EnrichmentStatus)
	}
	if strings.Contains(logs.String(), leakSecret) || strings.Contains(fmt.Sprint(res), leakSecret) {
		t.Fatal("secret leaked into logs/result")
	}
}

func TestApplyRefund_EnrichedVsNoTransfersStatus(t *testing.T) {
	sink := recon.NewMemoryFinancialStore()
	p := NewProcessor(poll.NewMemoryStore())
	p.Refunds, p.Edges = sink, sink
	tenantLookup := &stubTransfers{items: []razorpay.TransferResponse{{ID: "trf_t", Recipient: "acc_t", Amount: 900, Currency: "INR"}}}
	p.TransfersFor = func(context.Context, string, string, string) (TransferLookup, error) { return tenantLookup, nil }
	env := refundEnv(t, "rfnd_en", "pay_en")
	if _, err := p.Apply(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	if r := onlyRefund(t, sink, env); r.EnrichmentStatus != recon.EnrichmentEnriched || r.SellerID != "acc_t" {
		t.Fatalf("status=%q seller=%q", r.EnrichmentStatus, r.SellerID)
	}
	tenantLookup.items = nil
	env2 := refundEnv(t, "rfnd_none", "pay_none")
	if _, err := p.Apply(context.Background(), env2); err != nil {
		t.Fatal(err)
	}
	if r := onlyRefund(t, sink, env2); r.EnrichmentStatus != recon.EnrichmentNoTransfers {
		t.Fatalf("status=%q", r.EnrichmentStatus)
	}
}

func TestApplyRefund_Razorpay401InvalidatesTenantCreds(t *testing.T) {
	sink := recon.NewMemoryFinancialStore()
	p := NewProcessor(poll.NewMemoryStore())
	p.Refunds, p.Edges = sink, sink
	p.TransfersFor = func(context.Context, string, string, string) (TransferLookup, error) {
		return &stubTransfers{err: &razorpay.ProviderError{Kind: razorpay.ErrUnauthorized, HTTPStatus: 401, Code: "BAD_REQUEST_ERROR"}}, nil
	}
	inv := &recordInvalidator{}
	p.CredentialInvalidator = inv
	env := refundEnv(t, "rfnd_401", "pay_401")
	if _, err := p.Apply(context.Background(), env); err == nil {
		t.Fatal("razorpay API error keeps retry semantics")
	}
	if inv.calls != 1 || inv.tenant != env.TenantID {
		t.Fatalf("401 must invalidate the tenant's cached creds: %+v", inv)
	}
}

type recordInvalidator struct {
	calls  int
	tenant string
}

func (r *recordInvalidator) Invalidate(tenantID, _, _ string) { r.calls++; r.tenant = tenantID }

type noPaymentsProvider struct{}

func (noPaymentsProvider) ListPaymentsPage(context.Context, time.Time, time.Time, int, int) (razorpay.NeutralPage[razorpay.NeutralPayment], error) {
	return razorpay.NeutralPage[razorpay.NeutralPayment]{Meta: razorpay.ResponseMeta{Status: 200}}, nil
}
func (noPaymentsProvider) ListSettlementDay(context.Context, razorpay.CivilDate, int, int) (razorpay.NeutralPage[razorpay.NeutralSettlementLine], error) {
	return razorpay.NeutralPage[razorpay.NeutralSettlementLine]{Meta: razorpay.ResponseMeta{Status: 200}}, nil
}

type switchableCreds struct{ has bool }

func (s *switchableCreds) TenantCredentials(context.Context, string, string, string) (string, string, error) {
	if !s.has {
		return "", "", poll.ErrNoTenantSecret
	}
	return "rzp_test_FAKETENANT", leakSecret, nil
}

// D52 / 6b: skipped for no creds -> creds become available -> the next D26
// pull re-enriches: seller_id + edges + status enriched.
func TestD26Pull_ReEnrichesSkippedRefundOnceCredsReturn(t *testing.T) {
	sink := recon.NewMemoryFinancialStore()
	p := NewProcessor(poll.NewMemoryStore())
	p.Refunds, p.Edges = sink, sink
	creds := &switchableCreds{}
	resolver := poll.EnvCredentialResolver{Tenant: creds}
	tenantLookup := &stubTransfers{items: []razorpay.TransferResponse{{ID: "trf_back", Recipient: "acc_back", Amount: 900, Currency: "INR"}}}
	tt := &TenantTransfers{Resolver: resolver, NewLookup: func(cfg razorpay.Config) (TransferLookup, error) {
		if cfg.KeySecret != leakSecret {
			return nil, errors.New("wrong key")
		}
		return tenantLookup, nil
	}}
	p.TransfersFor = tt.For

	env := refundEnv(t, "rfnd_back", "pay_back")
	if _, err := p.Apply(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	if r := onlyRefund(t, sink, env); r.EnrichmentStatus != recon.EnrichmentSkippedNoCreds {
		t.Fatalf("first pass must be skipped, got %q", r.EnrichmentStatus)
	}

	bstore := poll.NewMemoryStore()
	svc := poll.NewBackfillService(bstore, poll.NewFreshnessService(bstore, poll.MemoryWebhookIndex{}), resolver,
		func(razorpay.Config) (poll.BackfillProvider, error) { return noPaymentsProvider{}, nil })
	svc.ReEnrichSkipped = p.ReEnrichSkipped
	run := func() poll.BackfillSummary {
		now := time.Now().UTC()
		job, err := svc.CreateJob(context.Background(), poll.CreateBackfillRequest{
			TenantID: env.TenantID, ConnectorID: env.ConnectorID, Mode: "test",
			ResourceType: poll.ResourcePayments, WindowFrom: now.Add(-3 * time.Hour), WindowTo: now.Add(-2 * time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		sum, _ := svc.RunPayments(context.Background(), job.ID)
		return sum
	}
	// Pull while creds are still missing: visible failure, still skipped.
	if sum := run(); sum.Status == poll.JobSucceeded || sum.LastErrorCode != "NO_CREDENTIALS" {
		t.Fatalf("pull without creds must report NO_CREDENTIALS, got %+v", sum)
	}
	if r := onlyRefund(t, sink, env); r.EnrichmentStatus != recon.EnrichmentSkippedNoCreds {
		t.Fatal("still skipped")
	}

	creds.has = true
	if sum := run(); sum.Status != poll.JobSucceeded {
		t.Fatalf("pull with creds must succeed: %+v", sum)
	}
	r := onlyRefund(t, sink, env)
	if r.EnrichmentStatus != recon.EnrichmentEnriched || r.SellerID != "acc_back" {
		t.Fatalf("after re-enrich: status=%q seller=%q", r.EnrichmentStatus, r.SellerID)
	}
	edges, _ := sink.ListTransferEdges(context.Background(), env.TenantID, env.ConnectorID)
	if len(edges) != 1 || edges[0].TransferID != "trf_back" || edges[0].RefundID != "rfnd_back" {
		t.Fatalf("edges=%+v", edges)
	}
	gap, _ := sink.CountSkippedEnrichment(context.Background(), env.TenantID, "")
	if gap.Total != 0 {
		t.Fatalf("data gap must clear, got %+v", gap)
	}
}

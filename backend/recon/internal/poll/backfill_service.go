package poll

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"zord-outcome-engine/internal/paymenttruth"
	"zord-outcome-engine/internal/poll/providers/razorpay"

	"github.com/google/uuid"
)

type BackfillProvider interface {
	ListPaymentsPage(ctx context.Context, from, to time.Time, skip, count int) (razorpay.NeutralPage[razorpay.NeutralPayment], error)
	ListSettlementDay(ctx context.Context, day razorpay.CivilDate, skip, count int) (razorpay.NeutralPage[razorpay.NeutralSettlementLine], error)
}

type CredentialResolver interface {
	Resolve(ctx context.Context, tenantID, connectorID, mode string) (razorpay.Config, error)
}

type ProviderFactory func(cfg razorpay.Config) (BackfillProvider, error)

type WebhookIndex interface {
	ListReceipts(ctx context.Context, tenantID, connectorID string, from, to time.Time) ([]WebhookReceiptRef, error)
}

type BackfillService struct {
	store     Store
	freshness *FreshnessService
	creds     CredentialResolver
	factory   ProviderFactory
	now       func() time.Time
	owner     string
	truth     *paymenttruth.Processor

	// ReEnrichSkipped (optional) re-runs transfer enrichment for refunds whose
	// enrichment was skipped (D52). Wired to observe.Processor.ReEnrichSkipped.
	ReEnrichSkipped func(ctx context.Context, tenantID, connectorID, mode string, limit int) (int, error)
}

func NewBackfillService(store Store, freshness *FreshnessService, creds CredentialResolver, factory ProviderFactory) *BackfillService {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "outcome-engine"
	}
	s := &BackfillService{
		store:     store,
		freshness: freshness,
		creds:     creds,
		factory:   factory,
		now:       func() time.Time { return time.Now().UTC() },
		owner:     hostname,
	}
	if ts, ok := store.(paymenttruth.Store); ok {
		s.truth = paymenttruth.NewProcessor(ts)
	}
	return s
}

func (s *BackfillService) CreateJob(ctx context.Context, req CreateBackfillRequest) (BackfillJob, error) {
	now := s.now()
	if req.Provider == "" {
		req.Provider = "razorpay"
	}
	if req.TriggerType == "" {
		req.TriggerType = TriggerManual
	}
	if req.TraceID == "" {
		req.TraceID = uuid.Must(uuid.NewV7()).String()
	}
	req.WindowFrom = ApplyOverlap(req.WindowFrom, req.OverlapMinutes)
	from, to := FreezeWindow(req.WindowFrom, req.WindowTo, now)
	req.WindowFrom, req.WindowTo = from, to
	if err := req.Validate(now); err != nil {
		return BackfillJob{}, err
	}
	if existing, err := s.store.FindActiveJob(ctx, req.TenantID, req.ConnectorID, req.ResourceType, from, to); err != nil {
		return BackfillJob{}, err
	} else if existing != nil {
		return *existing, nil
	}

	job := BackfillJob{
		TenantID:     req.TenantID,
		ConnectorID:  req.ConnectorID,
		Provider:     req.Provider,
		ProviderMode: req.Mode,
		ResourceType: req.ResourceType,
		WindowFrom:   from,
		WindowTo:     to,
		TriggerType:  req.TriggerType,
		Status:       JobQueued,
		RequestedBy:  req.RequestedBy,
		TraceID:      req.TraceID,
	}
	created, err := s.store.CreateJob(ctx, job)
	if err != nil {
		if existing, findErr := s.store.FindActiveJob(ctx, req.TenantID, req.ConnectorID, req.ResourceType, from, to); findErr == nil && existing != nil {
			return *existing, nil
		}
		return BackfillJob{}, err
	}
	pageCount := DefaultPaymentCount
	if req.ResourceType == ResourceSettlements {
		pageCount = DefaultReconCount
	}
	cursor, err := s.store.EnsureCursor(ctx, BackfillCursor{
		TenantID:     req.TenantID,
		ConnectorID:  req.ConnectorID,
		ResourceType: req.ResourceType,
		WindowFrom:   from,
		WindowTo:     to,
		PageSkip:     0,
		PageCount:    pageCount,
		Status:       CursorActive,
	})
	if err != nil {
		return created, err
	}
	if cursor.Status == CursorComplete || cursor.Status == CursorFailed {
		cursor.PageSkip = 0
		cursor.PagesCompleted = 0
		cursor.Status = CursorActive
		cursor.LastProviderID = ""
		cursor.LastResponseHash = ""
		if err := s.store.AdvanceCursor(ctx, cursor); err != nil {
			return created, err
		}
	}
	return created, nil
}

func (s *BackfillService) RunPayments(ctx context.Context, jobID string) (BackfillSummary, error) {
	return s.run(ctx, jobID, ResourcePayments)
}

func (s *BackfillService) RunSettlements(ctx context.Context, jobID string) (BackfillSummary, error) {
	return s.run(ctx, jobID, ResourceSettlements)
}

func (s *BackfillService) Resume(ctx context.Context, jobID string) (BackfillSummary, error) {
	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		return BackfillSummary{}, err
	}
	if job.Status == JobCancelled {
		return SummaryFromJob(job, BackfillCursor{}), fmt.Errorf("job cancelled")
	}
	return s.run(ctx, jobID, job.ResourceType)
}

func (s *BackfillService) Cancel(ctx context.Context, jobID string) error {
	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		return err
	}
	if job.Status == JobSucceeded {
		return fmt.Errorf("cannot cancel a succeeded job")
	}
	job.Status = JobCancelled
	now := s.now()
	job.CompletedAt = &now
	return s.store.UpdateJob(ctx, job)
}

func (s *BackfillService) GetJob(ctx context.Context, jobID string) (BackfillJob, error) {
	return s.store.GetJob(ctx, jobID)
}

func (s *BackfillService) GetJobWithCursor(ctx context.Context, jobID string) (BackfillJob, BackfillCursor, error) {
	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		return job, BackfillCursor{}, err
	}
	cursor, err := s.store.GetCursor(ctx, job.TenantID, job.ConnectorID, job.ResourceType, job.WindowFrom, job.WindowTo)
	if err != nil {
		return job, BackfillCursor{}, nil
	}
	return job, cursor, nil
}

func (s *BackfillService) run(ctx context.Context, jobID, expectedResource string) (BackfillSummary, error) {
	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		return BackfillSummary{}, err
	}
	if expectedResource != "" && job.ResourceType != expectedResource {
		return BackfillSummary{}, fmt.Errorf("job resource_type is %s", job.ResourceType)
	}
	if job.Status == JobCancelled {
		return SummaryFromJob(job, BackfillCursor{}), fmt.Errorf("job cancelled")
	}
	if job.Status == JobSucceeded {
		return SummaryFromJob(job, BackfillCursor{Status: CursorComplete, PageSkip: int(job.FetchedCount)}), nil
	}

	now := s.now()
	if job.StartedAt == nil {
		job.StartedAt = &now
	}
	job.Status = JobRunning
	if err := s.store.UpdateJob(ctx, job); err != nil {
		return BackfillSummary{}, err
	}

	cursor, err := s.store.AcquireCursorLease(ctx, job.TenantID, job.ConnectorID, job.ResourceType, job.WindowFrom, job.WindowTo, s.owner, DefaultLeaseTTL)
	if err != nil {
		job.Status = JobPartial
		job.LastErrorCode = "LEASE_HELD"
		job.LastErrorMessage = redactError(err)
		_ = s.store.UpdateJob(ctx, job)
		return SummaryFromJob(job, cursor), err
	}
	defer func() { _ = s.store.ReleaseCursorLease(ctx, cursor.ID, s.owner) }()

	cfg, err := s.creds.Resolve(ctx, job.TenantID, job.ConnectorID, job.ProviderMode)
	if err != nil {
		// D52 + EM: never a silent success. The job ends FAILED with a
		// visible reason (NO_CREDENTIALS / CREDENTIALS_UNAVAILABLE), a
		// per-tenant WARN (ids only) and backfill_credentials_missing_total.
		code := CredentialFailureCode(err)
		backfillCredentialsMissingTotal.Inc(code)
		log.Printf("WARN backfill: no usable credentials tenant_id=%s connector_id=%s mode=%s resource=%s job_id=%s reason=%s",
			job.TenantID, job.ConnectorID, job.ProviderMode, job.ResourceType, job.ID, code)
		return s.failJob(ctx, job, cursor, code, err)
	}
	// Tenant key material is redacted from every job error string below.
	tenantSecrets := []string{cfg.KeySecret, cfg.KeyID}
	provider, err := s.factory(cfg)
	if err != nil {
		return s.failJob(ctx, job, cursor, "PROVIDER", errors.New(redactSecrets(err.Error(), tenantSecrets...)))
	}

	observeBackfillRun()
	started := s.now()
	var runErr error
	if job.ResourceType == ResourceSettlements {
		runErr = s.paginateSettlements(ctx, &job, &cursor, provider)
	} else if job.ResourceType == ResourcePayouts {
		runErr = s.paginatePayouts(ctx, &job, &cursor, provider)
	} else {
		runErr = s.paginatePayments(ctx, &job, &cursor, provider)
	}
	observeBackfillAPILatency(s.now().Sub(started))
	observeCursorAge(s.now().Sub(job.WindowTo))
	observeBackfillLag(s.now().Sub(job.WindowTo))

	completed := s.now()
	job.CompletedAt = &completed
	if runErr != nil {
		var pErr *razorpay.ProviderError
		if errors.As(runErr, &pErr) {
			job.LastErrorCode = pErr.Code
			job.LastErrorMessage = redactSecrets(redactError(pErr), tenantSecrets...)
			if pErr.Kind == razorpay.ErrUnauthorized {
				// D52: a rotated key -> drop the cached pair so the next run refetches.
				if inv, ok := s.creds.(CredentialInvalidator); ok {
					inv.Invalidate(job.TenantID, job.ConnectorID, job.ProviderMode)
				}
			}
			if pErr.Kind == razorpay.ErrUnauthorized || pErr.Kind == razorpay.ErrForbidden || pErr.Kind == razorpay.ErrDecode {
				job.Status = JobFailed
			} else {
				job.Status = JobPartial
				job.ErrorCount++
			}
		} else {
			job.Status = JobPartial
			job.LastErrorCode = "BACKFILL_ERROR"
			job.LastErrorMessage = redactSecrets(redactError(runErr), tenantSecrets...)
			job.ErrorCount++
		}
	} else if cursor.Status == CursorComplete {
		job.Status = JobSucceeded
	} else {
		job.Status = JobPartial
	}
	if err := s.store.UpdateJob(ctx, job); err != nil {
		observeBackfillFailure()
		return SummaryFromJob(job, cursor), err
	}
	if job.Status == JobSucceeded {
		observeBackfillSuccess()
	} else if job.Status == JobFailed {
		observeBackfillFailure()
	}
	// D52: creds work for this tenant again -> re-enrich refunds whose
	// transfer enrichment was skipped (bounded batch). Best effort: a
	// re-enrich error never changes the job result.
	if runErr == nil && s.ReEnrichSkipped != nil {
		if n, err := s.ReEnrichSkipped(ctx, job.TenantID, job.ConnectorID, job.ProviderMode, ReEnrichBatchSize); err != nil {
			log.Printf("WARN backfill: re-enrich skipped refunds failed tenant_id=%s connector_id=%s reenriched=%d", job.TenantID, job.ConnectorID, n)
		} else if n > 0 {
			log.Printf("backfill: re-enriched skipped refunds tenant_id=%s connector_id=%s count=%d", job.TenantID, job.ConnectorID, n)
		}
	}
	return SummaryFromJob(job, cursor), runErr
}

// ReEnrichBatchSize bounds one post-pull re-enrich pass per tenant+connector.
const ReEnrichBatchSize = 200

func (s *BackfillService) paginatePayments(ctx context.Context, job *BackfillJob, cursor *BackfillCursor, provider BackfillProvider) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		jobRow, err := s.store.GetJob(ctx, job.ID)
		if err == nil && jobRow.Status == JobCancelled {
			return fmt.Errorf("job cancelled")
		}

		page, err := provider.ListPaymentsPage(ctx, job.WindowFrom, job.WindowTo, cursor.PageSkip, cursor.PageCount)
		if err != nil {
			return err
		}
		snapshot := *job
		cursorSnap := *cursor
		if err := s.store.RunInTx(ctx, func(ctx context.Context) error {
			if err := s.persistPaymentPage(ctx, &snapshot, &cursorSnap, page); err != nil {
				return err
			}
			if !page.HasMore || len(page.Items) == 0 {
				cursorSnap.Status = CursorComplete
			} else {
				cursorSnap.PageSkip += len(page.Items)
				cursorSnap.PagesCompleted++
			}
			exp := s.now().Add(DefaultLeaseTTL)
			cursorSnap.LeaseExpiresAt = &exp
			return s.store.AdvanceCursor(ctx, cursorSnap)
		}); err != nil {
			return err
		}
		*job = snapshot
		*cursor = cursorSnap
		if cursor.Status == CursorComplete {
			return nil
		}
	}
}

func (s *BackfillService) persistPaymentPage(ctx context.Context, job *BackfillJob, cursor *BackfillCursor, page razorpay.NeutralPage[razorpay.NeutralPayment]) error {
	receiptID := uuid.Must(uuid.NewV7()).String()
	if err := s.store.InsertResponseReceipt(ctx, ResponseReceipt{
		ID:                receiptID,
		TenantID:          job.TenantID,
		ConnectorID:       job.ConnectorID,
		BackfillJobID:     job.ID,
		Provider:          job.Provider,
		ResourceType:      job.ResourceType,
		RequestPath:       page.Meta.Path,
		RequestQueryHash:  page.Meta.QueryHash,
		ResponseStatus:    page.Meta.Status,
		ResponseHash:      page.Meta.Hash,
		PageSkip:          cursor.PageSkip,
		PageCount:         cursor.PageCount,
		ProviderItemCount: len(page.Items),
	}); err != nil {
		return err
	}

	webhookIDs := map[string]struct{}{}
	if s.freshness != nil && s.freshness.Index != nil {
		refs, err := s.freshness.Index.ListReceipts(ctx, job.TenantID, job.ConnectorID, job.WindowFrom, job.WindowTo)
		if err == nil {
			for _, r := range refs {
				if r.ProviderEntityID != "" {
					webhookIDs[r.ProviderEntityID] = struct{}{}
				}
			}
		}
	}

	var lastID string
	for _, item := range page.Items {
		job.FetchedCount++
		backfillFetchedTotal.Inc()
		_, hadWebhook := webhookIDs[item.PaymentID]
		missingWebhook := !hadWebhook
		if s.truth != nil {
			res, err := s.truth.ProcessNeutral(ctx, job.TenantID, job.ConnectorID, job.Provider, job.ProviderMode, SourceAPIBackfill, "", receiptID, item, missingWebhook)
			if err != nil {
				return err
			}
			switch res.Kind {
			case paymenttruth.KindInserted:
				job.InsertedCount++
				backfillInsertedTotal.Inc()
				if missingWebhook {
					job.MissingWebhookCount++
					backfillMissingWebhook.Inc()
				}
				observeObservationSource(SourceAPIBackfill)
			case paymenttruth.KindUpdated, paymenttruth.KindObserved:
				job.UpdatedCount++
				backfillUpdatedTotal.Inc()
				observeObservationSource(SourceAPIBackfill)
			case paymenttruth.KindDuplicate:
				job.DuplicateCount++
				backfillDuplicatesTotal.Inc()
			}
			lastID = item.PaymentID
			continue
		}
		res, err := s.store.UpsertPayment(ctx, PaymentObservation{
			TenantID:       job.TenantID,
			ConnectorID:    job.ConnectorID,
			Provider:       job.Provider,
			ProviderMode:   job.ProviderMode,
			Item:           item,
			ReceiptID:      receiptID,
			Source:         SourceAPIBackfill,
			Sources:        []string{SourceAPIBackfill},
			WebhookMissing: missingWebhook,
		})
		if err != nil {
			return err
		}
		switch res {
		case UpsertInserted:
			job.InsertedCount++
			backfillInsertedTotal.Inc()
			row, err := PaymentOutboxRow(job.TenantID, job.ConnectorID, item.PaymentID, SourceAPIBackfill, item)
			if err != nil {
				return err
			}
			if err := s.store.InsertOutbox(ctx, row); err != nil {
				return err
			}
			if missingWebhook {
				job.MissingWebhookCount++
				backfillMissingWebhook.Inc()
			}
			observeObservationSource(SourceAPIBackfill)
		case UpsertUpdated:
			job.UpdatedCount++
			backfillUpdatedTotal.Inc()
			row, err := PaymentOutboxRow(job.TenantID, job.ConnectorID, item.PaymentID, SourceAPIBackfill, item)
			if err != nil {
				return err
			}
			if err := s.store.InsertOutbox(ctx, row); err != nil {
				return err
			}
			observeObservationSource(SourceAPIBackfill)
		case UpsertDuplicate:
			job.DuplicateCount++
			backfillDuplicatesTotal.Inc()
		}
		lastID = item.PaymentID
	}
	cursor.LastProviderID = lastID
	cursor.LastResponseHash = page.Meta.Hash
	if err := s.store.UpdateJob(ctx, *job); err != nil {
		return err
	}
	return nil
}

func (s *BackfillService) paginateSettlements(ctx context.Context, job *BackfillJob, cursor *BackfillCursor, provider BackfillProvider) error {
	days := CivilDays(job.WindowFrom, job.WindowTo)
	for _, d := range days {
		daySkip := 0
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			page, err := provider.ListSettlementDay(ctx, razorpay.CivilDate{Year: d.Year, Month: d.Month, Day: d.Day}, daySkip, cursor.PageCount)
			if err != nil {
				return err
			}
			snapshot := *job
			cursorSnap := *cursor
			if err := s.store.RunInTx(ctx, func(ctx context.Context) error {
				if err := s.persistSettlementPage(ctx, &snapshot, &cursorSnap, page); err != nil {
					return err
				}
				if page.HasMore && len(page.Items) > 0 {
					cursorSnap.PageSkip += len(page.Items)
					cursorSnap.PagesCompleted++
					exp := s.now().Add(DefaultLeaseTTL)
					cursorSnap.LeaseExpiresAt = &exp
					return s.store.AdvanceCursor(ctx, cursorSnap)
				}
				return nil
			}); err != nil {
				return err
			}
			*job = snapshot
			*cursor = cursorSnap
			if !page.HasMore || len(page.Items) == 0 {
				break
			}
			daySkip += len(page.Items)
		}
	}
	cursor.Status = CursorComplete
	return s.store.RunInTx(ctx, func(ctx context.Context) error {
		return s.store.AdvanceCursor(ctx, *cursor)
	})
}

func (s *BackfillService) persistSettlementPage(ctx context.Context, job *BackfillJob, cursor *BackfillCursor, page razorpay.NeutralPage[razorpay.NeutralSettlementLine]) error {
	receiptID := uuid.Must(uuid.NewV7()).String()
	if err := s.store.InsertResponseReceipt(ctx, ResponseReceipt{
		ID:                receiptID,
		TenantID:          job.TenantID,
		ConnectorID:       job.ConnectorID,
		BackfillJobID:     job.ID,
		Provider:          job.Provider,
		ResourceType:      job.ResourceType,
		RequestPath:       page.Meta.Path,
		RequestQueryHash:  page.Meta.QueryHash,
		ResponseStatus:    page.Meta.Status,
		ResponseHash:      page.Meta.Hash,
		PageSkip:          cursor.PageSkip,
		PageCount:         cursor.PageCount,
		ProviderItemCount: len(page.Items),
	}); err != nil {
		return err
	}
	var lastID string
	for _, item := range page.Items {
		job.FetchedCount++
		res, err := s.store.UpsertSettlementLine(ctx, SettlementLineObservation{
			TenantID:     job.TenantID,
			ConnectorID:  job.ConnectorID,
			Provider:     job.Provider,
			ProviderMode: job.ProviderMode,
			Item:         item,
			ReceiptID:    receiptID,
			Source:       "razorpay_settlement_recon",
		})
		if err != nil {
			return err
		}
		switch res {
		case UpsertInserted:
			job.InsertedCount++
			row, err := SettlementOutboxRow(job.TenantID, job.ConnectorID, item)
			if err != nil {
				return err
			}
			if err := s.store.InsertOutbox(ctx, row); err != nil {
				return err
			}
		case UpsertUpdated:
			job.UpdatedCount++
			row, err := SettlementOutboxRow(job.TenantID, job.ConnectorID, item)
			if err != nil {
				return err
			}
			if err := s.store.InsertOutbox(ctx, row); err != nil {
				return err
			}
		case UpsertDuplicate:
			job.DuplicateCount++
		}
		lastID = item.EntityID
	}
	cursor.LastProviderID = lastID
	cursor.LastResponseHash = page.Meta.Hash
	return s.store.UpdateJob(ctx, *job)
}

func (s *BackfillService) failJob(ctx context.Context, job BackfillJob, cursor BackfillCursor, code string, err error) (BackfillSummary, error) {
	job.Status = JobFailed
	job.LastErrorCode = code
	job.LastErrorMessage = redactError(err)
	now := s.now()
	job.CompletedAt = &now
	_ = s.store.UpdateJob(ctx, job)
	return SummaryFromJob(job, cursor), err
}

func redactError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	msg = strings.ReplaceAll(msg, os.Getenv("RAZORPAY_KEY_SECRET"), "[redacted]")
	msg = strings.ReplaceAll(msg, os.Getenv("RAZORPAY_KEY_ID"), "[redacted]")
	if strings.Contains(strings.ToLower(msg), "authorization") {
		return "provider request failed"
	}
	if len(msg) > 300 {
		return msg[:300]
	}
	return msg
}

// ErrNoTenantSecret means no per-tenant Razorpay credentials exist for the
// requested tenant/connector. D32: recon never falls back to the shared,
// process-wide RAZORPAY_KEY_* env for a tenant.
var ErrNoTenantSecret = errors.New("no per-tenant Razorpay credentials configured for this tenant/connector")

// TenantCredentialSource returns one tenant connector's own Razorpay key pair.
// Recon has no such store wired yet; when one exists, plug it in here.
type TenantCredentialSource interface {
	TenantCredentials(ctx context.Context, tenantID, connectorID, mode string) (keyID, keySecret string, err error)
}

// EnvCredentialResolver resolves Razorpay API credentials.
//
//   - Tenant scope (tenantID or connectorID set, e.g. backfill jobs): only the
//     tenant's own credentials from Tenant. With no Tenant source, or no
//     credentials for that tenant, it fails closed with ErrNoTenantSecret, even
//     when RAZORPAY_KEY_ID/RAZORPAY_KEY_SECRET are set. The old behaviour
//     silently used those shared keys for every tenant.
//   - Platform scope (tenantID == "" and connectorID == ""): the process env
//     key pair. Only the boot-time transfers enrichment client in cmd/main.go
//     asks for this; it never serves a tenant-scoped request.
type EnvCredentialResolver struct {
	Tenant TenantCredentialSource
}

func (r EnvCredentialResolver) Resolve(ctx context.Context, tenantID, connectorID, mode string) (razorpay.Config, error) {
	if mode == "live" && os.Getenv("RAZORPAY_ALLOW_LIVE") != "true" {
		return razorpay.Config{}, fmt.Errorf("live mode is disabled")
	}
	if strings.TrimSpace(tenantID) == "" && strings.TrimSpace(connectorID) == "" {
		return PlatformEnvCredentials(mode)
	}
	if r.Tenant == nil {
		return razorpay.Config{}, ErrNoTenantSecret
	}
	keyID, keySecret, err := r.Tenant.TenantCredentials(ctx, tenantID, connectorID, mode)
	if err != nil {
		return razorpay.Config{}, err
	}
	if strings.TrimSpace(keyID) == "" || strings.TrimSpace(keySecret) == "" {
		return razorpay.Config{}, ErrNoTenantSecret
	}
	cfg := razorpay.DefaultConfig()
	cfg.Mode = razorpay.Mode(mode)
	cfg.KeyID = keyID
	cfg.KeySecret = keySecret
	if err := cfg.Validate(); err != nil {
		return razorpay.Config{}, err
	}
	return cfg, nil
}

// PlatformEnvCredentials reads the process-wide Test/Live key pair. It is for
// platform-scope callers only (boot-time transfers enrichment), never for a
// tenant's request.
func PlatformEnvCredentials(mode string) (razorpay.Config, error) {
	if mode == "live" && os.Getenv("RAZORPAY_ALLOW_LIVE") != "true" {
		return razorpay.Config{}, fmt.Errorf("live mode is disabled")
	}
	cfg := razorpay.DefaultConfig()
	cfg.Mode = razorpay.Mode(mode)
	cfg.KeyID = os.Getenv("RAZORPAY_KEY_ID")
	cfg.KeySecret = os.Getenv("RAZORPAY_KEY_SECRET")
	if mode == "live" {
		if v := os.Getenv("RAZORPAY_LIVE_KEY_ID"); v != "" {
			cfg.KeyID = v
		}
		if v := os.Getenv("RAZORPAY_LIVE_KEY_SECRET"); v != "" {
			cfg.KeySecret = v
		}
	}
	if err := cfg.Validate(); err != nil {
		return razorpay.Config{}, err
	}
	return cfg, nil
}

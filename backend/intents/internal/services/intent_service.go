package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"

	"zord-intent-engine/db"
	"zord-intent-engine/internal/auth"
	"zord-intent-engine/internal/canonicalizer"
	"zord-intent-engine/internal/models"
	"zord-intent-engine/internal/normalizer"
	"zord-intent-engine/kafka"

	// "zord-intent-engine/internal/pii"
	"zord-intent-engine/internal/guards"
	"zord-intent-engine/internal/persistence"
	"zord-intent-engine/internal/validator"
	"zord-intent-engine/internal/vault"
	"zord-intent-engine/storage"

	"github.com/shopspring/decimal"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// Score field weights — all values sum to 100 within their score.
// Expressed as float64 for direct arithmetic.
const (
	// schema_completeness_score weights
	wSchemaAmount          = 10.0
	wSchemaCurrency        = 5.0
	wSchemaBeneficiary     = 15.0
	wSchemaClientPayoutRef = 15.0
	wSchemaSourceSystem    = 5.0
	wSchemaSourceRowRef    = 5.0
	wSchemaClientBatchRef  = 8.0
	wSchemaExecutionAt     = 7.0
	wSchemaPayoutType      = 5.0
	wSchemaVendorRef       = 8.0
	wSchemaPurpose         = 7.0
	wSchemaMappingProfile  = 5.0
	wSchemaTokenization    = 5.0
	wSchemaTotalMax        = 100.0

	// reference_quality_score weights
	wRefClientPayoutRef = 25.0
	wRefClientBatchRef  = 10.0
	wRefSourceRowRef    = 10.0
	wRefBeneficiaryFP   = 15.0
	wRefBusinessIdemKey = 15.0
	wRefExecutionAt     = 5.0
	wRefProviderHint    = 5.0
	wRefTotalMax        = 85.0 // Zord signature bonus (+15) applied separately, capped at 100

	// matchability_score sub-weights (each sub-score is 0–100)
	wMatchExternalRef  = 0.30
	wMatchPartyAmount  = 0.20
	wMatchBatchContext = 0.15
	wMatchTiming       = 0.15
	wMatchSourceSystem = 0.10
	wMatchMappingConf  = 0.10

	// proof_readiness_score weights
	wProofRawEnvelope      = 0.15
	wProofNIRProvenance    = 0.15
	wProofCanonicalHash    = 0.15
	wProofGovernance       = 0.15
	wProofTokenization     = 0.10
	wProofBusinessIdem     = 0.10
	wProofReferenceQuality = 0.10
	wProofMappingProfile   = 0.05
	wProofBatchContext     = 0.05

	// intent_quality_score weights
	wQualitySchema       = 0.20
	wQualityMapping      = 0.20
	wQualityReference    = 0.20
	wQualityMatchability = 0.15
	wQualityProof        = 0.15
	wQualityDupSafety    = 0.10

	// Duplicate risk thresholds
	dupRiskLow      = 30.0
	dupRiskMedium   = 60.0
	dupRiskHigh     = 80.0
	dupRiskCritical = 100.0
)

var batchAggregateGroup singleflight.Group

type IntentService struct {
	validator          *validator.Validator
	repo               CanonicalIntentRepository
	s3                 *storage.S3Store
	tokenizeQueue      *KafkaTokenizeQueue
	db                 *sql.DB
	tenantDailyUsage   persistence.TenantDailyUsageRepository
	tenantBusinessDate persistence.TenantBusinessDateRepository
	vectorPublisher    VectorIndexPublisher
}
type VectorIndexPublisher interface {
	PublishVectorIndexRequest(ctx context.Context, event kafka.VectorIndexRequestEvent) error
}

func (s *IntentService) SetVectorIndexPublisher(p VectorIndexPublisher) {
	s.vectorPublisher = p
}

var enclaveHTTPClient = &http.Client{
	Timeout:   10 * time.Second,
	Transport: otelhttp.NewTransport(http.DefaultTransport),
}

// getTenantSynonyms returns tenant-specific synonym overrides from DB.
// Returns an empty map if the tenant has no custom synonyms configured — the
// global synonym dict still applies. Returns a non-nil error only on a real
// DB failure (see LoadTenantSynonyms / INT-06); the caller must fail the row
// rather than normalize it without the tenant's real overrides.
func (s *IntentService) getTenantSynonyms(ctx context.Context, tenantID uuid.UUID) (map[string]string, error) {
	return LoadTenantSynonyms(ctx, s.db, tenantID)
}

// Repository abstraction
type CanonicalIntentRepository interface {
	Save(
		ctx context.Context,
		nir *models.NormalizedIngestRecord,
		intent models.CanonicalIntent,
		outbox models.OutboxEvent,
		registry *models.BusinessIdempotencyEntry,
		policyDecision *models.IntentPolicyDecision,
		duplicateDecision *models.DuplicateDecision,
	) (models.CanonicalIntent, error)

	SaveBatch(
		ctx context.Context,
		items []models.SaveBatchItem,
	) ([]models.CanonicalIntent, []models.DLQEntry, error)

	FindByEnvelope(
		ctx context.Context,
		tenantID string,
		envelopeID string,
	) (*models.CanonicalIntent, error)

	// R-05: minimal approval primitive for a held (REQUIRES_REVIEW) intent.
	GetHeldIntentForApproval(ctx context.Context, tenantID, intentID string) (amount decimal.Decimal, currency string, governanceState string, err error)
	ApproveHeldIntent(ctx context.Context, tenantID, intentID, approvedBy string) error

	UpdateSnapshotRefs(
		ctx context.Context,
		tenantID string,
		intentID string,
		canonicalRef string,
		nirRef string,
		govRef string,
		hash string,
		prevHash string,
		governanceHash string,
	) error

	GetPreviousTenantCanonicalHash(
		ctx context.Context,
		tenantID string,
		intentID string,
	) (string, error)

	FindByBusinessIdempotencyKey(
		ctx context.Context,
		tenantID string,
		key string,
	) (*models.CanonicalIntent, error)

	CheckIdempotencyRegistry(
		ctx context.Context,
		tenantID string,
		key string,
	) (*models.BusinessIdempotencyEntry, error)

	FindIntentIDByIdempotencyKey(ctx context.Context, tenantID, idempotencyKey string) (string, error)
	FindIntentIDByClientPayoutRef(ctx context.Context, tenantID, clientPayoutRef string) (string, error)

	UpdateBatchAggregateConfidence(
		ctx context.Context,
		tenantID string,
		batchID string,
	) (float64, error)
}

func NewIntentService(
	v *validator.Validator,
	r CanonicalIntentRepository,
	s3 *storage.S3Store,
	q *KafkaTokenizeQueue,
	db *sql.DB,
	tenantDailyUsage persistence.TenantDailyUsageRepository,
	tenantBusinessDate persistence.TenantBusinessDateRepository,
) *IntentService {
	return &IntentService{
		validator:          v,
		repo:               r,
		s3:                 s3,
		tokenizeQueue:      q,
		db:                 db,
		tenantDailyUsage:   tenantDailyUsage,
		tenantBusinessDate: tenantBusinessDate,
	}
}
func (s *IntentService) emitVectorIndexRequest(
	sourceEventType string,
	tenantID string,
	entityType string,
	entityID string,
	batchID string,
	metadata map[string]string,
) {
	if s == nil || s.vectorPublisher == nil {
		return
	}

	tenantID = strings.TrimSpace(tenantID)
	entityID = strings.TrimSpace(entityID)
	batchID = strings.TrimSpace(batchID)

	if tenantID == "" || entityID == "" {
		return
	}

	if metadata == nil {
		metadata = map[string]string{}
	}

	event := kafka.VectorIndexRequestEvent{
		EventID:         uuid.NewString(),
		SchemaVersion:   SchemaVersionV1,
		EventType:       kafka.VectorIndexEventRequested,
		SourceService:   "zord-intent-engine",
		SourceEventType: sourceEventType,
		TenantID:        tenantID,
		EntityType:      entityType,
		EntityID:        entityID,
		BatchID:         batchID,
		Operation:       kafka.VectorIndexOperationUpsert,
		OccurredAt:      time.Now().UTC(),
		ContentVersion:  "v1",
		Metadata:        metadata,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := s.vectorPublisher.PublishVectorIndexRequest(ctx, event); err != nil {
		log.Printf("[intent-engine][vector-index] publish failed tenant=%s entity=%s id=%s err=%v", tenantID, entityType, entityID, err)
		return
	}

	log.Printf("[intent-engine][vector-index] publish ok tenant=%s entity=%s id=%s", tenantID, entityType, entityID)
}

func (s *IntentService) EmitDLQVectorIndexRequest(dlq models.DLQEntry) {
	batchID := strings.TrimSpace(dlq.BatchID)
	if batchID == "" {
		batchID = strings.TrimSpace(dlq.ClientBatchRef)
	}

	s.emitVectorIndexRequest(
		"intent_dlq.saved.v1",
		dlq.TenantID,
		"intent_dlq",
		dlq.DLQID,
		batchID,
		map[string]string{
			"stage":       dlq.Stage,
			"reason_code": dlq.ReasonCode,
			"dlq_status":  dlq.DLQStatus,
		},
	)
}
func (s *IntentService) maybeEmitCompletedBatchVectorIndex(ctx context.Context, tenantID, batchID string) {
	tenantID = strings.TrimSpace(tenantID)
	batchID = strings.TrimSpace(batchID)

	if s == nil || s.vectorPublisher == nil || tenantID == "" || batchID == "" {
		return
	}

	var totalRows, acceptedRows, failedRows, duplicateRows int
	var status string

	err := s.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(total_rows, 0),
			COALESCE(accepted_rows, 0),
			COALESCE(failed_rows, 0),
			COALESCE(duplicate_rows, 0),
			COALESCE(status, '')
		FROM intent_ingest_runs
		WHERE tenant_id = $1::uuid
		  AND batch_id = $2
	`, tenantID, batchID).Scan(
		&totalRows,
		&acceptedRows,
		&failedRows,
		&duplicateRows,
		&status,
	)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("[intent-engine][vector-index] completion check failed tenant=%s batch_id=%s err=%v", tenantID, batchID, err)
		}
		return
	}

	processedRows := acceptedRows + failedRows + duplicateRows
	if totalRows <= 0 || processedRows < totalRows || !strings.EqualFold(status, "COMPLETED") {
		log.Printf("[intent-engine][vector-index] batch not complete tenant=%s batch_id=%s processed=%d total=%d status=%s", tenantID, batchID, processedRows, totalRows, status)
		return
	}

	log.Printf("[intent-engine][vector-index] final batch emit tenant=%s entity=intent_batch id=%s processed=%d total=%d accepted=%d failed=%d duplicate=%d",
		tenantID,
		batchID,
		processedRows,
		totalRows,
		acceptedRows,
		failedRows,
		duplicateRows,
	)

	s.emitVectorIndexRequest(
		"intent_batch.updated.v1",
		tenantID,
		"intent_batch",
		batchID,
		batchID,
		map[string]string{
			"vector_summary_scope": "batch",
			"total_rows":           strconv.Itoa(totalRows),
			"accepted_rows":        strconv.Itoa(acceptedRows),
			"failed_rows":          strconv.Itoa(failedRows),
			"duplicate_rows":       strconv.Itoa(duplicateRows),
		},
	)
}

// resolveBusinessDate returns tenantID's business_date for now, via the
// per-tenant timezone configured in tenant_business_date_config (4.2.7),
// falling back to persistence.BusinessDateUTC when no resolver is wired —
// defensive only; NewIntentService always supplies one in production.
func (s *IntentService) resolveBusinessDate(ctx context.Context, tenantID string) string {
	if s.tenantBusinessDate == nil {
		return persistence.BusinessDateUTC(time.Now())
	}
	return s.tenantBusinessDate.ResolveBusinessDate(ctx, tenantID, time.Now())
}

// ErrIntentNotHeld is returned by ApproveHeldIntent when the target intent's
// governance_state isn't REQUIRES_REVIEW — there's nothing to approve.
var ErrIntentNotHeld = errors.New("intent is not currently held for review")

// ApproveHeldIntent is R-05's minimal approval primitive: re-run the same
// atomic ReserveIfWithinLimit check against TODAY's usage (not the total
// that was current when the intent was originally held — "approval
// rechecks the latest total instead of trusting the original calculation")
// and, only if it now fits, flip the intent to ACCEPTED. This is
// deliberately not a review UI/audit-trail/permissions system — just the
// atomic re-check the doc's acceptance tests require to exist at all.
//
// approvedBy is the verified PAYOUT_APPROVER user id (Slice 8, D39), stored
// as payment_intents.approved_by alongside approved_at.
func (s *IntentService) ApproveHeldIntent(ctx context.Context, tenantID, intentID, approvedBy string) (string, error) {
	amount, currency, governanceState, err := s.repo.GetHeldIntentForApproval(ctx, tenantID, intentID)
	if err != nil {
		return "", fmt.Errorf("approve held intent: %w", err)
	}
	if governanceState != "REQUIRES_REVIEW" {
		return "", ErrIntentNotHeld
	}

	businessDate := s.resolveBusinessDate(ctx, tenantID)
	decision, _, err := s.tenantDailyUsage.ReserveIfWithinLimit(
		ctx, tenantID, businessDate, currency, amount, guards.DailyLimitForCurrency(currency),
	)
	if err != nil {
		return "", fmt.Errorf("approve held intent: daily usage reservation: %w", err)
	}
	recordDailyLimitApproval(ctx, tenantID, intentID, currency, businessDate, decision)
	if decision != persistence.DailyLimitDecisionAccept {
		// Still over today's limit — remains FLAGGED_FOR_REVIEW, no change.
		return decision, nil
	}

	if err := s.repo.ApproveHeldIntent(ctx, tenantID, intentID, approvedBy); err != nil {
		return "", fmt.Errorf("approve held intent: %w", err)
	}
	return decision, nil
}

/* ---------------- Helpers ---------------- */

func parseAmount(value string) (decimal.Decimal, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return decimal.Zero, errors.New("amount is required")
	}
	d, err := decimal.NewFromString(v) // exact decimal, no rounding
	if err != nil {
		return decimal.Zero, err
	}
	// Every downstream amount_minor (idempotency key, row hash, registry,
	// governance hash) is derived from this value; reject sub-paise and
	// int64 overflow here so none of them can silently truncate.
	if _, err := amountMinorExact(d); err != nil {
		return decimal.Zero, err
	}
	return d, nil
}

// TOK-04: tenant_id is intentionally NOT part of this request body anymore
// -- the enclave now derives it solely from the verified service JWT minted
// below (kept as a plain function argument to MintServiceToken instead).
// object_ref/correlation_id are new mandatory fields the enclave enforces.
type enclaveTokenizeRequest struct {
	TenantID      string            `json:"-"`
	TraceID       string            `json:"trace_id"`
	ObjectRef     string            `json:"object_ref"`
	CorrelationID string            `json:"correlation_id"`
	PII           map[string]string `json:"pii"`
}

// enclavePurposeCode is the one purpose_code this service is allowed to use
// against zord-token-enclave's TOK-04 issuer allow-list -- must match the
// enclave's own internal/serviceauth allowedPurposeCodes entry for
// "zord-intent-engine" exactly.
const enclavePurposeCode = "INTENT_PROCESSING"

func callEnclaveTokenize(ctx context.Context, req enclaveTokenizeRequest) (map[string]string, error) {
	var lastErr error
	for i := 0; i < 3; i++ {
		tokens, err := callEnclaveTokenizeOnce(ctx, req)
		if err == nil {
			return tokens, nil
		}
		lastErr = err
		backoff := time.Duration(100*(1<<i)) * time.Millisecond
		log.Printf("⚠️ Token enclave call failed (attempt %d/3), retrying in %v: %v", i+1, backoff, err)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
	}
	return nil, fmt.Errorf("enclave tokenize failed after 3 attempts: %w", lastErr)
}

func callEnclaveTokenizeOnce(ctx context.Context, req enclaveTokenizeRequest) (map[string]string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("ZORD_PII_ENCLAVE_URL")), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("ZORD_PII_ENCLAVE_URL is not set")
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/tokenize", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	// TOK-04: a short-lived, signed service JWT scoped to exactly this
	// tenant/purpose replaces the old static ENCLAVE_INTERNAL_TOKEN shared
	// secret -- the enclave derives tenant_id/caller/purpose_code from this
	// verified claim, never from the request body above.
	serviceTok, err := auth.MintServiceToken(req.TenantID, enclavePurposeCode)
	if err != nil {
		return nil, fmt.Errorf("mint service token: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Zord-Internal-Token", serviceTok)

	resp, err := enclaveHTTPClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("status=%d body=%s", resp.StatusCode, string(raw))
	}

	var out struct {
		Tokens map[string]string `json:"tokens"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Tokens, nil
}

// computeBeneficiaryFingerprint hashes the raw TOKEN VALUES zord-token-
// enclave returned (not the underlying PII itself) into a stable
// fingerprint, used by business_idempotency_registry to detect duplicate
// payment intents for the same beneficiary (SAME_BENEFICIARY_AMOUNT_TIME).
//
// TOK-08 dependency (found during zord-token-enclave's "field-kind-specific
// normalization and versioned token semantics" ticket): this function's
// correctness depends entirely on the enclave computing the SAME token for
// the SAME real-world account_number/ifsc/vpa every time. The enclave now
// has real, explicit normalization versioning (internal/crypto/
// deterministic.go's CurrentNormalizationVersion), but keeps its ACTIVE
// version ("v1") bit-for-bit identical to its original behavior specifically
// so this function's existing fingerprints stay valid. If that ever changes
// -- i.e. a future ticket activates a new normalization version as the
// default -- every fingerprint computed from tokens produced under the new
// version will differ from what's already stored here for the same real
// beneficiary, and this table's lookup will silently stop finding genuine
// duplicates. business_idempotency_registry.token_normalization_version
// (added by db/migrations/20260817120000_...) exists as a forward-
// compatible hook for that future ticket to populate/reconcile against --
// it is not populated by this code path today.
func (s *IntentService) computeBeneficiaryFingerprint(tokens map[string]string) string {
	// FIX: deterministic fingerprint using tokens
	// beneficiary_fingerprint = SHA256(account_number_token + ifsc_token + vpa_token)
	raw := tokens["account_number"] + tokens["ifsc"] + tokens["vpa"]
	hash := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(hash[:])
}

func (s *IntentService) isAbnormalAmount(amount decimal.Decimal, currency string) bool {
	threshold := decimal.NewFromInt(1000000) // Default 1M
	if strings.ToUpper(currency) == "INR" {
		threshold = decimal.NewFromInt(10000000) // 10M for INR (1 Crore)
	}
	return amount.GreaterThan(threshold)
}

// computeBusinessIdempotencyKey returns the key stored going forward (v2):
// the preferred formula (tenant_id + source_system + client_payout_ref +
// currency, NO amount — D18) when clientPayoutRef is a reliable reference,
// else business_idempotency_fallback_hash (beneficiary_fingerprint + amount +
// currency + execution_date + invoice_ref + purpose_code). invoiceRef has no
// ingestion pipeline yet, so it hashes as "" — see canonical_row_hash for the
// same treatment.
func (s *IntentService) computeBusinessIdempotencyKey(
	tenantID string,
	sourceSystem string,
	clientPayoutRef string,
	fingerPrint string,
	amount decimal.Decimal,
	currency string,
	intendedExecutionAt string,
	purposeCode string,
) string {
	// Slice 8: returns the v2 (stored) key; see computeBusinessIdempotencyKeys.
	return s.computeBusinessIdempotencyKeys(tenantID, sourceSystem, clientPayoutRef,
		fingerPrint, amount, currency, intendedExecutionAt, purposeCode).V2
}

func (s *IntentService) computeRequestFingerprint(beneficiaryName string, amount decimal.Decimal, accountNumber string, vpa string, currency string) string {
	// fingerprint must be deterministic hash of: beneficiary, amount, account_number, currency
	raw := strings.TrimSpace(beneficiaryName) +
		amount.String() +
		strings.TrimSpace(accountNumber) +
		strings.TrimSpace(vpa) +
		strings.ToUpper(strings.TrimSpace(currency))

	hash := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(hash[:])
}

// computeCanonicalRowHash sets intent.CanonicalRowHash =
// SHA-256(JCS_Canonicalize(interpreted business fields)) using fields already
// present on intent. PaymentRail is sourced from BeneficiaryType (the
// normalized beneficiary.instrument.kind rail enum); InvoiceRef has no
// ingestion pipeline yet, so it hashes as an empty string until one exists.
func (s *IntentService) computeCanonicalRowHash(intent *models.CanonicalIntent) string {
	hash, err := canonicalizer.ComputeCanonicalRowHash(canonicalizer.CanonicalRowHashInput{
		SourceRowRef:           intent.SourceRowRef,
		ClientPayoutRef:        intent.ClientPayoutRef,
		BeneficiaryFingerprint: intent.BeneficiaryFingerprint,
		AmountMinor:            intent.Amount.Mul(decimal.NewFromInt(100)).IntPart(),
		Currency:               intent.Currency,
		IntendedExecutionAt:    intent.IntendedExecutionAt,
		PaymentRail:            intent.BeneficiaryType,
		InvoiceRef:             "",
	})
	if err != nil {
		log.Printf("⚠️ Failed to compute canonical_row_hash for intent %s: %v", intent.IntentID, err)
		return ""
	}
	return hash
}

// tokenizedDataHashMasterSecret is the master secret TOKENIZED_DATA_HASH_MASTER_SECRET
// used to derive a per-tenant HMAC key for tokenized_data_hash — see
// canonicalizer.DeriveTenantScopedKey. No per-tenant key store exists yet.
var tokenizedDataHashMasterSecret = os.Getenv("TOKENIZED_DATA_HASH_MASTER_SECRET")

// InitTokenizedDataHashMasterSecret fails closed if the master secret is
// unset. DeriveTenantScopedKey and the underlying HMAC accept an empty key
// without error, so without this check a missing secret wouldn't crash
// anything — it would silently derive tokenized_data_hash from a known
// empty key, a value anyone could reproduce, defeating the point of a
// tenant-scoped secret hash.
func InitTokenizedDataHashMasterSecret() error {
	if tokenizedDataHashMasterSecret == "" {
		return errors.New("TOKENIZED_DATA_HASH_MASTER_SECRET environment variable is required")
	}
	return nil
}

// computeTokenizedDataHash returns tokenized_data_hash for the tenant-scoped
// tokenized beneficiary fields in tokenMap. Missing token keys hash as null,
// matching the hash spec.
func (s *IntentService) computeTokenizedDataHash(tenantID string, tokenMap map[string]string) string {
	key := canonicalizer.DeriveTenantScopedKey(tokenizedDataHashMasterSecret, tenantID)
	hash, err := canonicalizer.ComputeTokenizedDataHash(key, canonicalizer.TokenizedDataHashInput{
		TenantID:             tenantID,
		BeneficiaryNameToken: tokenMap["name"],
		AccountNumberToken:   tokenMap["account_number"],
		IFSCToken:            tokenMap["ifsc"],
		VPAToken:             tokenMap["vpa"],
		EmailToken:           tokenMap["email"],
		PhoneToken:           tokenMap["phone"],
	})
	if err != nil {
		log.Printf("⚠️ Failed to compute tokenized_data_hash for tenant %s: %v", tenantID, err)
		return ""
	}
	return hash
}

// computeEvidenceLeafHashes returns raw_row_leaf_hash and
// canonical_row_leaf_hash for intent. ArtifactID/ArtifactVersionID are sealed
// by an upstream artifact service, not derived here — left blank until that
// pipeline exists. row_index uses intent.SourceRowNum (defaulting to 0 if
// unset). raw_row_hash uses intent.RawRowHash, relayed from zord-edge
// (JCS-canonicalized hash of the exact original row bytes); falls back to
// intent.PayloadHash (plain SHA-256 of the raw payload) for older paths that
// never set RawRowHash.
func (s *IntentService) computeEvidenceLeafHashes(intent *models.CanonicalIntent) (rawLeafHash string, canonicalLeafHash string) {
	rowIndex := 0
	if intent.SourceRowNum != nil {
		rowIndex = *intent.SourceRowNum
	}

	rawRowHash := intent.RawRowHash
	if rawRowHash == "" {
		rawRowHash = intent.PayloadHash
	}

	rawLeafHash, err := canonicalizer.ComputeRawRowEvidenceLeafHash(canonicalizer.RawRowEvidenceLeafHashInput{
		TenantID:     intent.TenantID,
		SourceRowRef: intent.SourceRowRef,
		RowIndex:     rowIndex,
		RawRowHash:   rawRowHash,
	})
	if err != nil {
		log.Printf("⚠️ Failed to compute raw_row_evidence_leaf_hash for intent %s: %v", intent.IntentID, err)
	}
	canonicalLeafHash, err = canonicalizer.ComputeCanonicalRowEvidenceLeafHash(canonicalizer.CanonicalRowEvidenceLeafHashInput{
		TenantID:         intent.TenantID,
		SourceRowRef:     intent.SourceRowRef,
		CanonicalRowHash: intent.CanonicalRowHash,
	})
	if err != nil {
		log.Printf("⚠️ Failed to compute canonical_row_evidence_leaf_hash for intent %s: %v", intent.IntentID, err)
	}
	return rawLeafHash, canonicalLeafHash
}

// computeGenericMappingProfileHash builds mapping_profile_hash for requests
// where no registered MappingProfile matched (ResolveProfileForIntent
// returned nil — e.g. no source_system header, or no profile registered for
// it). field_mappings is derived from the NIR field-level source paths this
// request actually used, so the hash reflects real interpretation rules
// instead of being left blank whenever there's no formal profile row.
func (s *IntentService) computeGenericMappingProfileHash(profileID, profileVersion, sourceSystem, detectedFormat string, fieldsMap map[string]models.NIRField) string {
	fieldMappings := make(map[string]string, len(fieldsMap))
	for name, f := range fieldsMap {
		fieldMappings[name] = f.SourcePath
	}
	p := &models.MappingProfile{
		ProfileID:                profileID,
		ProfileVersion:           profileVersion,
		SourceSystem:             sourceSystem,
		FileFormat:               detectedFormat,
		ColumnMap:                fieldMappings,
		StrictRequiredFieldsJSON: json.RawMessage("[]"),
		SoftInferableFieldsJSON:  json.RawMessage("[]"),
		FieldKindPolicyJSON:      json.RawMessage("{}"),
		SensitiveFieldPolicyJSON: json.RawMessage("{}"),
	}
	return p.ComputeProfileHash()
}

// computeScores calculates all 7 intent-level scores.
// All scores are in 0–100 space.
// tempIntent must have BeneficiaryFingerprint, Amount, Currency, ClientPayoutRef,
// ClientBatchRef, ProviderHint, SourceSystem, GovernanceHash, BusinessIdempotencyKey,
// and DuplicateRiskFlag set before calling this.
// nir may be nil (pre-NIR path) — scores degrade gracefully.
// gov is the Governance struct from ApplyPolicy().
func (s *IntentService) computeScores(
	intent *models.CanonicalIntent,
	nir *models.NormalizedIngestRecord,
	gov models.Governance,
	tokenizationComplete bool,
) (schema, mapping, refQuality, matchability, proof, dupRisk, quality float64, reasonCodes []string) {

	// ── 1. Schema Completeness Score ─────────────────────────────────────────
	// Measures whether the intent has enough canonical fields to be a payout contract.
	schema = s.computeSchemaScore(intent, nir, tokenizationComplete, &reasonCodes)

	// ── 2. Mapping Confidence Score ───────────────────────────────────────────
	// Measures how reliably source fields were mapped. Reads directly from NIR.
	mapping = s.computeMappingScore(intent, nir, gov, &reasonCodes)

	// ── 3. Reference Quality Score ────────────────────────────────────────────
	// Measures carrier strength for PSP/bank traceability.
	// Does NOT include trace_id — settlement files never return it.
	refQuality = s.computeReferenceQualityScore(intent, &reasonCodes)

	// ── 4. Matchability Score ─────────────────────────────────────────────────
	// Measures likelihood of clean settlement attachment later.
	matchability = s.computeMatchabilityScore(intent, mapping, &reasonCodes)

	// ── 5. Proof Readiness Score ──────────────────────────────────────────────
	// Measures evidence-pack defensibility for audit/dispute.
	proof = s.computeProofScore(intent, nir, refQuality, &reasonCodes)

	// ── 6. Duplicate Risk Score ───────────────────────────────────────────────
	// Risk only — not confirmed duplicate. Confirmation belongs to Service 7.
	dupRisk = s.computeDuplicateRiskScore(intent, &reasonCodes)

	// ── 7. Intent Quality Score ───────────────────────────────────────────────
	// Aggregate with governance caps.
	dupSafety := 100.0 - dupRisk
	quality = (schema*wQualitySchema +
		mapping*wQualityMapping +
		refQuality*wQualityReference +
		matchability*wQualityMatchability +
		proof*wQualityProof +
		dupSafety*wQualityDupSafety)

	// Governance caps — applied after formula
	if !gov.SemanticValid {
		quality -= 50.0
		reasonCodes = appendUniq(reasonCodes, "SEMANTIC_INVALID")
	}
	if gov.DuplicateDetected {
		quality -= 40.0
		reasonCodes = appendUniq(reasonCodes, "DUPLICATE_DETECTED")
	}
	if len(gov.MissingFields) > 0 {
		quality -= 30.0
		reasonCodes = appendUniq(reasonCodes, "MISSING_REQUIRED_FIELDS")
	}
	if intent.DuplicateRiskFlag {
		quality -= 30.0
	}
	if len(intent.ValidationAnomalies) > 0 {
		quality -= float64(len(intent.ValidationAnomalies)) * 10.0
	}
	if nir != nil && nir.LowConfidenceFieldCount > 0 {
		quality -= float64(nir.LowConfidenceFieldCount) * 5.0
	}

	// Cap thresholds (doc section 6.7)
	if dupRisk >= 80.0 {
		if quality > 60.0 {
			quality = 60.0
		}
		reasonCodes = appendUniq(reasonCodes, "HIGH_DUPLICATE_RISK_CAP")
	}
	if matchability < 40.0 {
		if quality > 75.0 {
			quality = 75.0
		}
		reasonCodes = appendUniq(reasonCodes, "LOW_MATCHABILITY_CAP")
	}
	if proof < 40.0 {
		if quality > 70.0 {
			quality = 70.0
		}
		reasonCodes = appendUniq(reasonCodes, "LOW_PROOF_READINESS_CAP")
	}

	schema = capScore100(schema) / 100.0
	mapping = capScore100(mapping) / 100.0
	refQuality = capScore100(refQuality) / 100.0
	matchability = capScore100(matchability) / 100.0
	proof = capScore100(proof) / 100.0
	dupRisk = capScore100(dupRisk) / 100.0
	quality = capScore100(quality) / 100.0

	return
}

func (s *IntentService) computeSchemaScore(
	intent *models.CanonicalIntent,
	nir *models.NormalizedIngestRecord,
	tokenizationComplete bool,
	reasonCodes *[]string,
) float64 {
	score := 0.0

	if !intent.Amount.IsZero() {
		score += wSchemaAmount
	} else {
		*reasonCodes = appendUniq(*reasonCodes, "MISSING_AMOUNT")
	}
	if intent.Currency != "" {
		score += wSchemaCurrency
	}
	// Beneficiary identity basis: fingerprint OR pii_tokens present
	if intent.BeneficiaryFingerprint != "" {
		score += wSchemaBeneficiary
	} else {
		*reasonCodes = appendUniq(*reasonCodes, "MISSING_BENEFICIARY_IDENTITY_BASIS")
	}
	// client_payout_ref OR business_idempotency_key
	if (intent.ClientPayoutRef != "" && intent.ClientPayoutRef != "NA") ||
		intent.BusinessIdempotencyKey != "" {
		score += wSchemaClientPayoutRef
	} else {
		*reasonCodes = appendUniq(*reasonCodes, "MISSING_CLIENT_REFERENCE")
	}
	if intent.SourceSystem != "" {
		score += wSchemaSourceSystem
	}
	// source_row_ref lives on NIR as EnvelopeID/SourcePath — use EnvelopeID as proxy
	if intent.EnvelopeID != "" {
		score += wSchemaSourceRowRef
	}
	if intent.ClientBatchRef != "" && intent.ClientBatchRef != "NA" {
		score += wSchemaClientBatchRef
	} else {
		*reasonCodes = appendUniq(*reasonCodes, "MISSING_BATCH_REFERENCE")
	}
	if intent.IntendedExecutionAt != nil {
		score += wSchemaExecutionAt
	}
	if intent.IntentType != "" {
		score += wSchemaPayoutType
	}
	// vendor/seller/customer token: check pii_tokens is not empty {}
	if len(intent.PIITokens) > 2 { // "{}" = 2 bytes
		score += wSchemaVendorRef
	}
	// purpose/narration — use GovernanceReasonCodesJSON as a proxy for purpose being set
	if intent.ProviderHint != "" {
		score += wSchemaPurpose
	}
	if intent.MappingProfileID != "" && intent.MappingProfileVersion != "" {
		score += wSchemaMappingProfile
	} else {
		*reasonCodes = appendUniq(*reasonCodes, "MAPPING_PROFILE_NOT_PINNED")
	}
	if tokenizationComplete {
		score += wSchemaTokenization
	}

	// Hard required fields: if gap count > 0, penalise
	if nir != nil && nir.RequiredFieldGapCount > 0 {
		score -= float64(nir.RequiredFieldGapCount) * 10.0
		*reasonCodes = appendUniq(*reasonCodes, "REQUIRED_FIELD_GAPS")
	}

	return score
}

func (s *IntentService) computeMappingScore(
	intent *models.CanonicalIntent,
	nir *models.NormalizedIngestRecord,
	gov models.Governance,
	reasonCodes *[]string,
) float64 {
	if nir == nil {
		return 50.0 // no NIR = moderate confidence only
	}

	// Field confidence levels (doc section 6.2):
	// 1.00 = exact profile match, 0.90 = approved synonym, 0.75 = source fallback,
	// 0.60 = fuzzy/inferred, 0.40 = derived from weak fields, 0.00 = missing
	var confSummary struct {
		AvgConfidence float64 `json:"avg_confidence"`
		Overall       float64 `json:"overall"`
		LowConfCount  int     `json:"low_confidence_field_count"`
	}
	avgConf := 1.0
	lowConfCount := nir.LowConfidenceFieldCount

	if len(nir.FieldConfidenceSummary) > 0 {
		_ = json.Unmarshal(nir.FieldConfidenceSummary, &confSummary)
		if confSummary.AvgConfidence > 0 {
			avgConf = confSummary.AvgConfidence
		} else if confSummary.Overall > 0 {
			avgConf = confSummary.Overall
		}
		if confSummary.LowConfCount > 0 {
			lowConfCount = confSummary.LowConfCount
		}
	}

	totalFields := 10.0 // critical field count per doc section 6.2
	highConfRatio := (totalFields - float64(nir.RequiredFieldGapCount) - float64(lowConfCount)) / totalFields
	if highConfRatio < 0 {
		highConfRatio = 0
	}

	// Convert 0–1 avgConf to 0–100
	score := (avgConf * 100 * 0.6) + (highConfRatio * 100 * 0.4)

	// Penalties (doc section 6.2)
	if nir.MappingUncertainFlag {
		score -= 15.0 // hard required field was inferred
		*reasonCodes = appendUniq(*reasonCodes, "FUZZY_MAPPING_USED")
	}
	if len(gov.LowConfidenceFields) > 0 {
		score -= 10.0
	}
	score -= float64(lowConfCount) * 5.0
	score -= float64(nir.RequiredFieldGapCount) * 10.0

	return score
}

func (s *IntentService) computeReferenceQualityScore(
	intent *models.CanonicalIntent,
	reasonCodes *[]string,
) float64 {
	score := 0.0

	// Do NOT include trace_id — settlement files never return it (doc section 6.3)
	if intent.ClientPayoutRef != "" && intent.ClientPayoutRef != "NA" {
		score += wRefClientPayoutRef
	} else {
		*reasonCodes = appendUniq(*reasonCodes, "MISSING_CLIENT_PAYOUT_REF")
	}
	if intent.ClientBatchRef != "" && intent.ClientBatchRef != "NA" {
		score += wRefClientBatchRef
	} else {
		*reasonCodes = appendUniq(*reasonCodes, "LOW_BATCH_REFERENCE")
	}
	if intent.EnvelopeID != "" {
		score += wRefSourceRowRef
	}
	if intent.BeneficiaryFingerprint != "" {
		score += wRefBeneficiaryFP
	}
	if intent.BusinessIdempotencyKey != "" {
		score += wRefBusinessIdemKey
	}
	if intent.IntendedExecutionAt != nil {
		score += wRefExecutionAt
	}
	if intent.ProviderHint != "" {
		score += wRefProviderHint
	}

	return score // Zord signature bonus not implemented yet — reserved for Prepare-and-Sign
}

func (s *IntentService) computeMatchabilityScore(
	intent *models.CanonicalIntent,
	mappingConf float64,
	reasonCodes *[]string,
) float64 {
	// Sub-scores all in 0–100 before weighting

	// external_reference_strength
	extRef := 0.0
	if intent.ClientPayoutRef != "" && intent.ClientPayoutRef != "NA" {
		extRef += 60.0
	}
	if intent.ProviderHint != "" {
		extRef += 30.0 // provider hint as proxy for prepared carrier
	}
	// invoice/order ref not available in current model — skip

	// party_amount_strength
	partyAmt := 0.0
	if intent.BeneficiaryFingerprint != "" {
		partyAmt += 40.0
	}
	if !intent.Amount.IsZero() {
		partyAmt += 30.0
	}
	if intent.Currency != "" {
		partyAmt += 20.0
	}
	if intent.IntentType != "" {
		partyAmt += 10.0
	}

	// batch_context_strength
	batchCtx := 0.0
	if intent.ClientBatchRef != "" && intent.ClientBatchRef != "NA" {
		batchCtx += 50.0
	}
	if intent.EnvelopeID != "" {
		batchCtx += 30.0
	}
	if intent.BusinessIdempotencyKey != "" {
		batchCtx += 20.0
	}

	// timing_strength
	timing := 0.0
	if intent.IntendedExecutionAt != nil {
		timing = 70.0 // precise execution time
	} else if !intent.CreatedAt.IsZero() {
		timing = 40.0 // date bucket only
	}

	// source_system_strength
	srcSystem := 0.0
	if intent.SourceSystem != "" {
		srcSystem += 60.0
		if s.isTrustedSystem(intent.SourceSystem) {
			srcSystem += 40.0
		}
	}

	score := extRef*wMatchExternalRef +
		partyAmt*wMatchPartyAmount +
		batchCtx*wMatchBatchContext +
		timing*wMatchTiming +
		srcSystem*wMatchSourceSystem +
		mappingConf*wMatchMappingConf

	if score < 40.0 {
		*reasonCodes = appendUniq(*reasonCodes, "LOW_MATCHABILITY")
	}

	return score
}

func (s *IntentService) computeProofScore(
	intent *models.CanonicalIntent,
	nir *models.NormalizedIngestRecord,
	refQuality float64,
	reasonCodes *[]string,
) float64 {
	score := 0.0

	// raw_envelope_integrity: envelope present + payload hash present
	if intent.EnvelopeID != "" && intent.PayloadHash != "" {
		score += wProofRawEnvelope * 100
	}
	// NIR_provenance
	if nir != nil && len(nir.FieldsJSON) > 2 {
		score += wProofNIRProvenance * 100
	}
	// canonical_hash_ready
	if intent.CanonicalHash != "" {
		score += wProofCanonicalHash * 100
	}
	// governance_decision_ready
	if intent.GovernanceHash != "" && intent.GovernanceState != "" {
		score += wProofGovernance * 100
	}
	// tokenization_complete: pii_tokens non-empty
	if len(intent.PIITokens) > 2 {
		score += wProofTokenization * 100
	}
	// business_idempotency_ready
	if intent.BusinessIdempotencyKey != "" {
		score += wProofBusinessIdem * 100
	}
	// reference_quality (normalized 0–100 → 0–1 for weighting)
	score += wProofReferenceQuality * refQuality
	// mapping_profile_version_pinned
	if intent.MappingProfileID != "" && intent.MappingProfileVersion != "" {
		score += wProofMappingProfile * 100
	}
	// batch_context_ready
	if intent.ClientBatchRef != "" && intent.ClientBatchRef != "NA" {
		score += wProofBatchContext * 100
	}

	if score < 40.0 {
		*reasonCodes = appendUniq(*reasonCodes, "LOW_PROOF_READINESS")
	}

	return score
}

func (s *IntentService) computeDuplicateRiskScore(
	intent *models.CanonicalIntent,
	reasonCodes *[]string,
) float64 {
	// Strict duplicate signals (terminal)
	if intent.DuplicateRiskFlag && intent.DuplicateReasonCode != "" && intent.DuplicateReasonCode != "NONE" {
		switch {
		case intent.DuplicateReasonCode == "SAME_IDEMPOTENCY_KEY":
			*reasonCodes = appendUniq(*reasonCodes, "STRICT_DUPLICATE_IDEMPOTENCY")
			return 100.0
		case intent.DuplicateReasonCode == "CLIENT_PAYOUT_REF_REUSED":
			*reasonCodes = appendUniq(*reasonCodes, "STRICT_DUPLICATE_CLIENT_REF")
			return 95.0
		}
	}

	// Semantic duplicate score — additive signals
	semantic := 0.0
	if intent.BeneficiaryFingerprint != "" && intent.DuplicateRiskFlag {
		semantic += 25.0
	}
	if !intent.Amount.IsZero() && intent.DuplicateRiskFlag {
		semantic += 25.0
	}
	if intent.DuplicateRiskFlag {
		semantic += 30.0 // registry hit = same beneficiary+amount+time bucket
		*reasonCodes = appendUniq(*reasonCodes, "SAME_BENEFICIARY_AMOUNT_TIME")
	}

	return semantic
}

// buildScoreBreakdown returns score_breakdown_json for every intent.
// This is required by the manager's doc — every score must have component breakdown.
func buildScoreBreakdown(
	schema, mapping, refQuality, matchability, proof, dupRisk, quality float64,
) json.RawMessage {
	breakdown := map[string]any{
		"schema_completeness_score": schema,
		"mapping_confidence_score":  mapping,
		"reference_quality_score":   refQuality,
		"matchability_score":        matchability,
		"proof_readiness_score":     proof,
		"duplicate_risk_score":      dupRisk,
		"intent_quality_score":      quality,
		"score_version":             models.ScoreVersion,
	}
	b, _ := json.Marshal(breakdown)
	return b
}

// capScore100 caps a float64 to [0, 100].
func capScore100(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// appendUniq appends s to slice only if not already present.
func appendUniq(slice []string, s string) []string {
	for _, v := range slice {
		if v == s {
			return slice
		}
	}
	return append(slice, s)
}

func (s *IntentService) isTrustedSystem(source string) bool {
	trusted := map[string]bool{
		"SAP_ERP":      true,
		"CORE_BANKING": true,
		"SWIFT_GPI":    true,
	}
	return trusted[source]
}

func (s *IntentService) ApplyPolicy(nir *models.NormalizedIngestRecord, req models.ParsedIncomingIntent) models.Governance {
	gov := models.Governance{
		SemanticValid:        true,
		RoutingConsistent:    true,
		ExecutionWindowValid: true,
		PolicyFlags:          []string{},
		SemanticErrors:       []string{},
		MissingFields:        []string{},
		LowConfidenceFields:  []string{},
	}

	// ----------------------------------------
	// SEMANTIC POLICY (HARD)
	// ----------------------------------------

	// BANK requires IFSC
	if req.Beneficiary.Instrument.Kind == "BANK" && req.Beneficiary.Instrument.IFSC == "" {
		gov.SemanticValid = false
		gov.SemanticErrors = append(gov.SemanticErrors, "BANK_REQUIRES_IFSC")
	}
	// UPI requires VPA
	if req.Beneficiary.Instrument.Kind == "UPI" && req.Beneficiary.Instrument.VPA == "" {
		gov.SemanticValid = false
		gov.SemanticErrors = append(gov.SemanticErrors, "UPI_REQUIRES_VPA")
	}
	// BANK + VPA -> error
	// if req.Beneficiary.Instrument.Kind == "BANK" && req.Beneficiary.Instrument.VPA != "" {
	// 	gov.SemanticValid = false
	// 	gov.SemanticErrors = append(gov.SemanticErrors, "BANK_WITH_VPA_INVALID")
	// }
	// // UPI + IFSC -> error
	// if req.Beneficiary.Instrument.Kind == "UPI" && req.Beneficiary.Instrument.IFSC != "" {
	// 	gov.SemanticValid = false
	// 	gov.SemanticErrors = append(gov.SemanticErrors, "UPI_WITH_IFSC_INVALID")
	// }

	// source vs provider_hint: UPI -> UPI_RAIL, BANK -> BANK_RAIL
	// REMOVED: Making provider_hint flexible
	/*
		if req.Beneficiary.Instrument.Kind == "UPI" && req.ProviderHint != "" && !strings.Contains(strings.ToUpper(req.ProviderHint), "UPI") {
			gov.RoutingConsistent = false
			gov.SemanticErrors = append(gov.SemanticErrors, "ROUTING_INCONSISTENT_UPI")
		}
		if req.Beneficiary.Instrument.Kind == "BANK" && req.ProviderHint != "" && !strings.Contains(strings.ToUpper(req.ProviderHint), "BANK") {
			gov.RoutingConsistent = false
			gov.SemanticErrors = append(gov.SemanticErrors, "ROUTING_INCONSISTENT_BANK")
		}
	*/

	// execution_window vs intended_execution_at
	if req.IntendedExecutionAt != "" {
		t, err := time.Parse(time.RFC3339, req.IntendedExecutionAt)
		if err != nil {
			gov.SemanticValid = false
			gov.SemanticErrors = append(gov.SemanticErrors, "INVALID_EXECUTION_AT_FORMAT")
		} else {
			if t.Before(time.Now().Add(-1 * time.Hour)) {
				gov.ExecutionWindowValid = false
				gov.SemanticErrors = append(gov.SemanticErrors, "EXECUTION_WINDOW_EXPIRED")
			}
		}
	}

	// required fields validation
	if req.IntentType == "" {
		gov.MissingFields = append(gov.MissingFields, "intent_type")
		gov.SemanticValid = false
	}
	if req.Amount.Value == "" {
		gov.MissingFields = append(gov.MissingFields, "amount.value")
		gov.SemanticValid = false
	} else if strings.HasPrefix(strings.TrimSpace(req.Amount.Value), "-") {
		// Explicit check for negative amounts to ensure routing to DLQ
		gov.SemanticValid = false
		gov.PolicyFlags = append(gov.PolicyFlags, "NEGATIVE_AMOUNT_NOT_ALLOWED")
	}

	// ----------------------------------------
	// DATA QUALITY POLICY
	// ----------------------------------------
	if nir != nil {
		if nir.RequiredFieldGapCount > 0 {
			gov.PolicyFlags = append(gov.PolicyFlags, "REQUIRED_FIELD_GAPS")
		}
		if nir.LowConfidenceFieldCount > 0 {
			gov.LowConfidenceFields = append(gov.LowConfidenceFields, "SEE_NIR_LOGS")
			gov.PolicyFlags = append(gov.PolicyFlags, "LOW_CONFIDENCE_DETECTION")
		}
	}

	return gov
}

// applyHardStrictReject upgrades gov to a hard reject when profile is in
// HARD_STRICT mode and at least one required field is missing. Returns true
// when it did so, so the caller can pick a DLQ reason code distinct from the
// generic SEMANTIC_INVALID. Factored out of processIncomingIntentInternal so
// R-09's HARD_STRICT gating is unit-testable without a full IntentService
// (S3/Kafka/DB) — same rationale as R-03's callWithRetry extraction in
// kafka/consumer.go. REVIEW_STRICT and OBSERVE profiles are untouched: this
// only ever fires for the new, explicitly opted-in mode.
func applyHardStrictReject(profile *models.MappingProfile, requiredFieldGapCount int, missingFieldNames []string, gov *models.Governance) bool {
	if profile == nil || profile.ValidationMode != models.ValidationModeHardStrict || requiredFieldGapCount == 0 {
		return false
	}
	gov.SemanticValid = false
	for _, f := range missingFieldNames {
		gov.MissingFields = appendUniq(gov.MissingFields, f)
	}
	gov.PolicyFlags = appendUniq(gov.PolicyFlags, "HARD_STRICT_REQUIRED_FIELD_MISSING")
	return true
}

/* ---------------- Pipeline ---------------- */

// ProcessIncomingIntent is the ONLY entrypoint.
func (s *IntentService) processIncomingIntentInternal(
	ctx context.Context,
	event *models.Event,
) (
	retIn *models.IncomingIntent,
	retProfile *models.MappingProfile,
	retDecrypted []byte,
	retRawAudit []byte,
	retAuditProfileID string,
	retAuditProfileVersion string,
	retSourceRowNum *int,
	retNir *models.NormalizedIngestRecord,
	retCanonical *models.CanonicalIntent,
	retOutbox *models.OutboxEvent,
	retRegistry *models.BusinessIdempotencyEntry,
	retDlq *models.DLQEntry,
	retErr error,
	retPolicyDecision *models.IntentPolicyDecision,
	retDuplicateDecision *models.DuplicateDecision,
) {

	//Unmarshal Payload into IncomingIntent struct
	var in *models.IncomingIntent

	in = &models.IncomingIntent{
		TenantID:          event.TenantID,
		EnvelopeID:        event.EnvelopeID,
		TraceID:           event.TraceID,
		Source:            event.Source,
		SourceSystem:      event.SourceSystem,
		ObjectRef:         event.ObjectRef,
		IdempotencyKey:    event.IdempotencyKey,
		EncryptedPayload:  event.Payload,
		PayloadHash:       event.PayloadHash,
		RawRowHash:        event.RawRowHash,
		ArtifactID:        event.ArtifactID,
		ArtifactVersionID: event.ArtifactVersionID,
		ContentType:       event.ContentType,
		KMSKeyVersion:     event.KMSKeyVersion,
		EncryptionKeyID:   event.EncryptionKeyID,
		ReceivedAt:        event.ReceivedAt,
		BatchID:           event.BatchID,
		SourceRowRef:      event.SourceRowRef,
		FileName:          event.FileName,
		FileContentHash:   event.FileContentHash,
		RowCountEstimate:  event.RowCountEstimate,
	}

	var resolvedProfile *models.MappingProfile
	var decryptedPayload []byte
	var rawAuditPayload []byte
	var auditProfileID string
	var auditProfileVersion string
	var sourceRowNum *int
	var err error

	// -------- STEP 0: Transport guards --------

	log.Printf("ProcessIncomingIntent: Source=%s EnvelopeID=%s", in.Source, in.EnvelopeID)

	if in.Source == "WEBHOOK" {
		log.Printf("ProcessIncomingIntent: Routing to processWebhook for EnvelopeID=%s", in.EnvelopeID)
		webhookCanonical, webhookDlq, webhookErr := s.processWebhook(ctx, in)
		retIn = in
		retCanonical = webhookCanonical
		retDlq = webhookDlq
		retErr = webhookErr
		return
	}

	batchIDStr := ""
	if in.BatchID != nil {
		batchIDStr = *in.BatchID
	}

	if len(in.EncryptedPayload) == 0 {
		retIn = in
		retProfile = resolvedProfile
		retDecrypted = decryptedPayload
		retRawAudit = rawAuditPayload
		retAuditProfileID = auditProfileID
		retAuditProfileVersion = auditProfileVersion
		retSourceRowNum = sourceRowNum
		retDlq = &models.DLQEntry{
			ReasonCode:  "EMPTY_PAYLOAD",
			ErrorDetail: "payload content is empty",
			DLQStatus:   models.ClassifyDLQ("EMPTY_PAYLOAD"),
			BatchID:     batchIDStr,
			TraceID:     in.TraceID.String(),
		}
		return
	}

	if in.TraceID == uuid.Nil {
		retIn = in
		retProfile = resolvedProfile
		retDecrypted = decryptedPayload
		retRawAudit = rawAuditPayload
		retAuditProfileID = auditProfileID
		retAuditProfileVersion = auditProfileVersion
		retSourceRowNum = sourceRowNum
		retDlq = &models.DLQEntry{
			ReasonCode:  "MISSING_TRACE_ID",
			ErrorDetail: "trace_id is required but missing",
			DLQStatus:   models.ClassifyDLQ("MISSING_TRACE_ID"),
			BatchID:     batchIDStr,
			TraceID:     in.TraceID.String(),
		}
		return
	}

	if in.EnvelopeID == uuid.Nil {
		retIn = in
		retProfile = resolvedProfile
		retDecrypted = decryptedPayload
		retRawAudit = rawAuditPayload
		retAuditProfileID = auditProfileID
		retAuditProfileVersion = auditProfileVersion
		retSourceRowNum = sourceRowNum
		retDlq = &models.DLQEntry{
			ReasonCode:  "MISSING_ENVELOPE_ID",
			ErrorDetail: "envelope_id is required but missing",
			DLQStatus:   models.ClassifyDLQ("MISSING_ENVELOPE_ID"),
			BatchID:     batchIDStr,
			TraceID:     in.TraceID.String(),
		}
		return
	}

	if in.TenantID == uuid.Nil {
		retIn = in
		retProfile = resolvedProfile
		retDecrypted = decryptedPayload
		retRawAudit = rawAuditPayload
		retAuditProfileID = auditProfileID
		retAuditProfileVersion = auditProfileVersion
		retSourceRowNum = sourceRowNum
		retDlq = &models.DLQEntry{
			ReasonCode:  "MISSING_TENANT_ID",
			ErrorDetail: "tenant_id is required but missing",
			DLQStatus:   models.ClassifyDLQ("MISSING_TENANT_ID"),
			BatchID:     batchIDStr,
			TraceID:     in.TraceID.String(),
		}
		return
	}

	if in.ObjectRef == "" {
		retIn = in
		retProfile = resolvedProfile
		retDecrypted = decryptedPayload
		retRawAudit = rawAuditPayload
		retAuditProfileID = auditProfileID
		retAuditProfileVersion = auditProfileVersion
		retSourceRowNum = sourceRowNum
		retDlq = &models.DLQEntry{
			ReasonCode:  "MISSING_OBJECT_REF",
			ErrorDetail: "object_ref is required but missing",
			DLQStatus:   models.ClassifyDLQ("MISSING_OBJECT_REF"),
			BatchID:     batchIDStr,
			TraceID:     in.TraceID.String(),
		}
		return
	}

	// -------- STEP 5: Parse raw payload into domain model --------
	contentType := in.ContentType
	if contentType == "" {
		contentType = "application/json"
	}
	encCtx := vault.EncryptionContext{
		TenantID:          in.TenantID.String(),
		ArtifactID:        in.ArtifactID.String(),
		ArtifactVersionID: in.ArtifactVersionID,
		ContentType:       contentType,
	}
	decryptedPayload, err = vault.DecryptPayload(encCtx, in.EncryptedPayload, in.KMSKeyVersion)
	if err != nil {
		log.Printf("vault decrypt failed envelope_id=%s tenant_id=%s artifact_id=%s artifact_version_id=%s content_type=%s err=%v",
			in.EnvelopeID, encCtx.TenantID, encCtx.ArtifactID, encCtx.ArtifactVersionID, encCtx.ContentType, err)
		retIn = in
		retProfile = resolvedProfile
		retDecrypted = decryptedPayload
		retRawAudit = rawAuditPayload
		retAuditProfileID = auditProfileID
		retAuditProfileVersion = auditProfileVersion
		retSourceRowNum = sourceRowNum
		retDlq = &models.DLQEntry{
			Stage:       "SECURITY_DLQ",
			ReasonCode:  "PAYLOAD_DECRYPTION_FAILED",
			ErrorDetail: "payload decryption failed: " + err.Error(),
			DLQStatus:   models.ClassifyDLQ("PAYLOAD_DECRYPTION_FAILED"),
			BatchID:     batchIDStr,
			TraceID:     in.TraceID.String(),
		}
		return
	}
	log.Printf("vault decrypt ok envelope_id=%s tenant_id=%s artifact_id=%s artifact_version_id=%s content_type=%s",
		in.EnvelopeID, encCtx.TenantID, encCtx.ArtifactID, encCtx.ArtifactVersionID, encCtx.ContentType)

	rawAuditPayload = append([]byte(nil), decryptedPayload...)
	sourceRowRef := ""
	if in.SourceRowRef != nil {
		sourceRowRef = strings.TrimSpace(*in.SourceRowRef)
	} else {
		log.Printf("⚠️ processIncomingIntentInternal: source_row_ref is nil for envelopeID=%s — source_row_num will be nil", in.EnvelopeID)
	}
	rawRowHash := ""
	if in.RawRowHash != nil {
		rawRowHash = strings.TrimSpace(*in.RawRowHash)
	}
	artifactID := ""
	if in.ArtifactID != uuid.Nil {
		artifactID = in.ArtifactID.String()
	}
	artifactVersionID := strings.TrimSpace(in.ArtifactVersionID)
	sourceRowNum = sourceRowNumFromRef(sourceRowRef)
	auditProfileID = autoGenericProfileID(rawAuditPayload)
	auditProfileVersion = "v1"

	// -------- STEP 4: Recompute SHA256(raw_bytes) and compare --------
	rawHash := sha256.Sum256(decryptedPayload)
	hexRawHash := hex.EncodeToString(rawHash[:])
	if in.PayloadHash == "" {
		log.Printf("⚠️ Missing raw payload hash for EnvelopeID=%s", in.EnvelopeID)
		retIn = in
		retProfile = resolvedProfile
		retDecrypted = decryptedPayload
		retRawAudit = rawAuditPayload
		retAuditProfileID = auditProfileID
		retAuditProfileVersion = auditProfileVersion
		retSourceRowNum = sourceRowNum
		retDlq = &models.DLQEntry{
			Stage:       "SECURITY_DLQ",
			ReasonCode:  "MISSING_RAW_PAYLOAD_HASH",
			ErrorDetail: "payload_hash is required but missing",
			DLQStatus:   models.ClassifyDLQ("MISSING_RAW_PAYLOAD_HASH"),
			BatchID:     batchIDStr,
			TraceID:     in.TraceID.String(),
		}
		return
	}

	if len(in.PayloadHash) != 64 {
		log.Printf("⚠️ Invalid raw payload hash length for EnvelopeID=%s (expected 64, got %d)", in.EnvelopeID, len(in.PayloadHash))
		retIn = in
		retProfile = resolvedProfile
		retDecrypted = decryptedPayload
		retRawAudit = rawAuditPayload
		retAuditProfileID = auditProfileID
		retAuditProfileVersion = auditProfileVersion
		retSourceRowNum = sourceRowNum
		retDlq = &models.DLQEntry{
			Stage:       "SECURITY_DLQ",
			ReasonCode:  "INVALID_RAW_PAYLOAD_HASH_LENGTH",
			ErrorDetail: "invalid payload_hash length (expected 64 chars)",
			DLQStatus:   models.ClassifyDLQ("INVALID_RAW_PAYLOAD_HASH_LENGTH"),
			BatchID:     batchIDStr,
			TraceID:     in.TraceID.String(),
		}
		return
	}
	if in.PayloadHash != "" && hexRawHash != in.PayloadHash {
		log.Printf("⚠️ Raw payload hash mismatch for EnvelopeID=%s", in.EnvelopeID)
		retIn = in
		retProfile = resolvedProfile
		retDecrypted = decryptedPayload
		retRawAudit = rawAuditPayload
		retAuditProfileID = auditProfileID
		retAuditProfileVersion = auditProfileVersion
		retSourceRowNum = sourceRowNum
		retDlq = &models.DLQEntry{
			Stage:       "SECURITY_DLQ",
			ReasonCode:  "RAW_PAYLOAD_INTEGRITY_FAILED",
			ErrorDetail: "payload integrity validation failed: hash mismatch",
			DLQStatus:   models.ClassifyDLQ("RAW_PAYLOAD_INTEGRITY_FAILED"),
			BatchID:     batchIDStr,
			TraceID:     in.TraceID.String(),
		}
		return
	}

	in.SourceSystem = strings.ToUpper(strings.TrimSpace(in.SourceSystem))
	if in.SourceSystem == "" || in.SourceSystem == "UNKNOWN" {
		var rawFields map[string]any
		if err := json.Unmarshal(decryptedPayload, &rawFields); err == nil {
			headers := make([]string, 0, len(rawFields))
			for header := range rawFields {
				headers = append(headers, header)
			}
			if detected := DetectSourceType(headers); detected != "" {
				in.SourceSystem = detected
				log.Printf("ℹ️ [profile] detected source_system=%s envelope=%s", detected, in.EnvelopeID)
			}
		}
	}

	// -------- STEP 4: Mapping Profile Application ─────────────────────────────
	// If a mapping profile is configured for this tenant + source_system,
	// apply column_map to translate tenant headers → canonical JSON keys.
	// This is the correct location for profile-driven normalization.
	// The normalizer at Step 5.1 then runs as a fast-path (no-op for canonical JSON).
	var profileUnmappedFields map[string]any
	if in.SourceSystem != "" && in.SourceSystem != "UNKNOWN" {
		artifactFamily := models.ArtifactFamilyLiveIntentJSON
		if in.Source == "CSV" || in.Source == "XLSX" || in.Source == "BULK_FILE" {
			artifactFamily = models.ArtifactFamilyPayoutFile
		}

		profile, profileErr := ResolveProfileForIntent(
			ctx,
			s.db,
			in.TenantID,
			in.SourceSystem,
			artifactFamily,
		)
		if profileErr != nil {
			// INT-06: a real profile-lookup failure (DB outage, etc.) must
			// never be treated as "no profile configured" — this replica
			// cannot tell whether the tenant actually has a profile driving
			// stricter validation, so proceeding here risks silently
			// accepting (or differently canonicalizing) a row that a
			// healthy replica — or a retry of this same row once the DB
			// recovers — would process under the tenant's real rules. Fail
			// the row back to the Kafka handler so it retries (kafka.
			// callWithRetry) instead of falling through to default rules.
			log.Printf("⚠️ [profile] lookup failed envelope=%s: %v — failing row instead of continuing without profile",
				in.EnvelopeID, profileErr)
			retIn = in
			retProfile = resolvedProfile
			retDecrypted = decryptedPayload
			retRawAudit = rawAuditPayload
			retAuditProfileID = auditProfileID
			retAuditProfileVersion = auditProfileVersion
			retSourceRowNum = sourceRowNum
			retErr = fmt.Errorf("mapping profile resolution unavailable for envelope=%s: %w", in.EnvelopeID, profileErr)
			return
		} else if profile != nil {
			resolvedProfile = profile
			parser := NewGenericSourceParser()
			mapped, unmapped, mapErr := parser.ParseToCanonicalJSON(decryptedPayload, profile)
			if mapErr != nil {
				log.Printf("⚠️ [profile] ParseToCanonicalJSON failed envelope=%s: %v — continuing with raw payload",
					in.EnvelopeID, mapErr)
			} else {
				decryptedPayload = mapped
				profileUnmappedFields = unmapped
				log.Printf("ℹ️ [profile] applied profile=%s source=%s envelope=%s",
					profile.ProfileID, in.SourceSystem, in.EnvelopeID)
			}
		}
	}
	// ── END STEP 4 ────────────────────────────────────────────────────────────

	// -------- STEP 5.1: Header normalization (ETL 10.1 / 10.2 / 10.3) --------
	// Normalize tenant-specific field names → Zord canonical JSON keys.
	// If payload is already canonical, this is a no-op (fast path).
	tenantSynonyms, synonymErr := s.getTenantSynonyms(ctx, in.TenantID)
	if synonymErr != nil {
		// INT-06: same reasoning as the mapping-profile failure above — a
		// real DB failure while loading the tenant's synonym overrides must
		// not be treated as "tenant has no overrides". Normalizing without
		// them here could produce a different NIR (and a different
		// canonical hash) than a healthy replica, or a retry of this same
		// row after the DB recovers, would produce. Fail the row instead of
		// silently normalizing without the tenant's real overrides.
		log.Printf("⚠️ [synonyms] lookup failed envelope=%s: %v — failing row instead of normalizing without tenant overrides",
			in.EnvelopeID, synonymErr)
		retIn = in
		retProfile = resolvedProfile
		retDecrypted = decryptedPayload
		retRawAudit = rawAuditPayload
		retAuditProfileID = auditProfileID
		retAuditProfileVersion = auditProfileVersion
		retSourceRowNum = sourceRowNum
		retErr = fmt.Errorf("tenant synonym resolution unavailable for envelope=%s: %w", in.EnvelopeID, synonymErr)
		return
	}
	normResult, normErr := normalizer.Normalize(decryptedPayload, tenantSynonyms)
	if normErr != nil {
		log.Printf("⚠️ Normalization failed for EnvelopeID=%s: %v — falling back to raw payload", in.EnvelopeID, normErr)
		// Do NOT DLQ — fall through with original payload (graceful degradation)
	} else {
		decryptedPayload = normResult.NormalizedJSON
		if normResult.WasNormalized {
			log.Printf("ℹ️ Payload normalized for EnvelopeID=%s warnings=%v", in.EnvelopeID, normResult.Warnings)
		}
	}
	// ── END STEP 5.1 ──────────────────────────────────────────────────────────

	var parsed models.ParsedIncomingIntent
	if err := json.Unmarshal(decryptedPayload, &parsed); err != nil {
		retIn = in
		retProfile = resolvedProfile
		retDecrypted = decryptedPayload
		retRawAudit = rawAuditPayload
		retAuditProfileID = auditProfileID
		retAuditProfileVersion = auditProfileVersion
		retSourceRowNum = sourceRowNum
		retDlq = &models.DLQEntry{
			ReasonCode:  "INVALID_JSON_PAYLOAD",
			ErrorDetail: "malformed JSON payload: " + err.Error(),
			DLQStatus:   models.ClassifyDLQ("INVALID_JSON_PAYLOAD"),
			BatchID:     batchIDStr,
			TraceID:     in.TraceID.String(),
		}
		return
	}
	parsed.SchemaVersion = SchemaVersionV1
	if sourceRowRef != "" {
		parsed.SourceRowRef = sourceRowRef
	}

	// FIX: Idempotency Key Fallback
	if in.IdempotencyKey == "" {
		in.IdempotencyKey = parsed.IdempotencyKey
		log.Printf("ProcessIncomingIntent: EnvelopeID=%s, falling back to payload idempotency_key=%s", in.EnvelopeID, in.IdempotencyKey)
	} else if parsed.IdempotencyKey == "" {
		parsed.IdempotencyKey = in.IdempotencyKey
	}

	// -------- STEP 6: Build NIR --------
	fieldsMap := make(map[string]models.NIRField)
	gapCount := 0
	lowConfCount := 0
	// R-09: names of required fields that were missing, so a HARD_STRICT
	// reject (below) can report exactly which fields the tenant needs to
	// fix — REVIEW_STRICT/OBSERVE never read this, only the count.
	var missingRequiredFieldNames []string

	// Helper to add structured field
	addFields := func(name string, value any, path string, required bool) {
		conf := 1.0 // default for direct parse
		if value == "" || value == nil {
			if required {
				gapCount++
				missingRequiredFieldNames = append(missingRequiredFieldNames, name)
			}
			conf = 0.0
		}
		if conf > 0 && conf < 0.8 {
			lowConfCount++
		}
		fieldsMap[name] = models.NIRField{
			Value:            value,
			SourcePath:       path,
			ConfidenceScore:  conf,
			SensitiveFlag:    false,  // Default
			TransformApplied: "NONE", // Default
			ExtractionNotes:  "",     // Default
		}
	}

	// requiredFor lets a tenant's mapping profile promote an otherwise-optional
	// field to required (StrictRequiredFieldsJSON), driving the required-field-
	// gap mechanism below: REVIEW_STRICT (and the default, unconfigured
	// profile) flags for review; HARD_STRICT (R-09) hard-rejects instead. The
	// 5 baseline fields above are always required regardless of profile —
	// tenant policy can only ADD requirements on top of core structural
	// safety, never remove them. OBSERVE mode records these fields for
	// visibility but never flags or rejects.
	requiredFor := func(name string, hardcodedDefault bool) bool {
		if resolvedProfile == nil {
			return hardcodedDefault
		}
		if resolvedProfile.ValidationMode == models.ValidationModeObserve {
			return false
		}
		if resolvedProfile.IsFieldRequired(name) {
			return true
		}
		return hardcodedDefault
	}

	addFields("intent_type", parsed.IntentType, "$.intent_type", true)
	addFields("amount", parsed.Amount.Value, "$.amount.value", true)
	addFields("currency", parsed.Amount.Currency, "$.amount.currency", true)
	addFields("beneficiary_name", parsed.Beneficiary.Name, "$.beneficiary.name", true)
	addFields("idempotency_key", parsed.IdempotencyKey, "$.idempotency_key", true)
	addFields("client_batch_ref", parsed.ClientBatchRef, "$.client_batch_ref", requiredFor("client_batch_ref", false))
	addFields("client_payout_ref", parsed.ClientPayoutRef, "$.client_payout_ref", requiredFor("client_payout_ref", false))
	addFields("provider_hint", parsed.ProviderHint, "$.provider_hint", requiredFor("provider_hint", false))
	addFields("intended_execution_at", parsed.IntendedExecutionAt, "$.intended_execution_at", requiredFor("intended_execution_at", false))

	fieldsJSON, _ := json.Marshal(fieldsMap)

	profileID := "generic_json_profile"
	if resolvedProfile != nil {
		profileID = resolvedProfile.ProfileID
	} else if auditProfileID != "" {
		profileID = auditProfileID
	}

	profileVersion := "v1"
	if resolvedProfile != nil {
		profileVersion = resolvedProfile.ProfileVersion
	} else if parsed.SchemaVersion != "" {
		profileVersion = parsed.SchemaVersion
	}

	profileHash := ""
	if resolvedProfile != nil {
		profileHash = resolvedProfile.ProfileHash
	} else {
		// No registered profile matched — still hash the field mappings this
		// request actually used, so mapping_profile_hash is never blank just
		// because there's no formal MappingProfile row.
		profileHash = s.computeGenericMappingProfileHash(profileID, profileVersion, in.SourceSystem, "json", fieldsMap)
	}

	nir := &models.NormalizedIngestRecord{
		NIRID:                   uuid.New(),
		EnvelopeID:              in.EnvelopeID,
		TenantID:                in.TenantID,
		DetectedFormat:          "json",
		ProfileID:               profileID,
		ProfileVersion:          profileVersion,
		FieldsJSON:              fieldsJSON,
		FieldConfidenceSummary:  json.RawMessage(`{"overall": 1.0}`),
		UnmappedJSON:            json.RawMessage(`{}`),
		MappingUncertainFlag:    false,
		RequiredFieldGapCount:   gapCount,
		LowConfidenceFieldCount: lowConfCount,
		CreatedAt:               time.Now().UTC(),
		MappingProfileHash:      profileHash,
	}

	// Fields the resolved mapping profile's column_map didn't account for are
	// never dropped — they're preserved here for audit/lineage even though the
	// normalizer sees profile-parsed JSON as already-canonical (WasNormalized=false)
	// and so wouldn't otherwise report them.
	if len(profileUnmappedFields) > 0 {
		if unmappedBytes, err := json.Marshal(profileUnmappedFields); err == nil {
			nir.UnmappedJSON = unmappedBytes
		}
	}

	if normResult != nil && normResult.WasNormalized {
		unmappedBytes, _ := json.Marshal(normResult.UnmappedFields)
		nir.UnmappedJSON = unmappedBytes
		hasFuzzyMatch := false
		for _, prov := range normResult.FieldProvenance {
			if prov.MatchMethod == "fuzzy" {
				hasFuzzyMatch = true
				break
			}
		}
		nir.MappingUncertainFlag = hasFuzzyMatch

		// Stamp provenance into each NIRField's TransformApplied
		for _, prov := range normResult.FieldProvenance {
			if field, ok := fieldsMap[canonicalPathToFieldName(prov.CanonicalPath)]; ok {
				field.TransformApplied = prov.Transform
				field.ExtractionNotes = prov.MatchMethod
				field.ConfidenceScore = prov.Confidence
				fieldsMap[canonicalPathToFieldName(prov.CanonicalPath)] = field
			}
		}

		// Re-compute lowConfCount and average confidence dynamically based on fieldsMap
		totalConf := 0.0
		cnt := 0
		lowConfCount = 0
		for _, field := range fieldsMap {
			totalConf += field.ConfidenceScore
			cnt++
			if field.ConfidenceScore > 0 && field.ConfidenceScore < 0.8 {
				lowConfCount++
			}
		}
		avgConf := 1.0
		if cnt > 0 {
			avgConf = totalConf / float64(cnt)
		}

		nir.LowConfidenceFieldCount = lowConfCount
		confSummaryBytes, _ := json.Marshal(map[string]any{
			"avg_confidence":             avgConf,
			"overall":                    avgConf,
			"low_confidence_field_count": lowConfCount,
		})
		nir.FieldConfidenceSummary = confSummaryBytes

		// Update FieldsJSON after adding provenance
		updatedFieldsJSON, _ := json.Marshal(fieldsMap)
		nir.FieldsJSON = updatedFieldsJSON
	} else {
		confSummaryBytes, _ := json.Marshal(map[string]any{
			"avg_confidence":             1.0,
			"overall":                    1.0,
			"low_confidence_field_count": 0,
		})
		nir.FieldConfidenceSummary = confSummaryBytes
	}

	// FIX: Generate IntentID early to include in GovernanceHash and DLQ Context
	intentID := uuid.NewString()
	parsed.IntentID = intentID

	// -------- STEP 6.5: APPLY GOVERNANCE POLICY (NEW) --------
	governance := s.ApplyPolicy(nir, parsed)

	// R-09: HARD_STRICT is an explicit per-tenant opt-in (mapping_profiles.
	// validation_mode, set via the admin mapping-profile API) that turns a
	// profile-required-field gap into a hard reject instead of the
	// REVIEW_STRICT/default hold-for-review path. It reuses the exact
	// SemanticValid/POLICY_DLQ mechanism below rather than a separate reject
	// path, so REVIEW_STRICT and OBSERVE tenants see zero behavior change.
	hardStrictRejected := applyHardStrictReject(resolvedProfile, nir.RequiredFieldGapCount, missingRequiredFieldNames, &governance)

	// 4.2.8: attach an explainability record whenever a mapping-profile
	// required-field gap drove this decision — HARD_STRICT reject (surfaced
	// on the DLQ entry's intent_context below) or REVIEW_STRICT hold
	// (surfaced on the row itself via GovernanceReasonCodesJSON).
	if nir.RequiredFieldGapCount > 0 {
		gapDecision := models.StrictModeDecisionReviewStrictHeld
		gapReasonCode := "REQUIRED_FIELD_GAPS"
		if hardStrictRejected {
			gapDecision = models.StrictModeDecisionHardStrictRejected
			gapReasonCode = "HARD_STRICT_REQUIRED_FIELD_MISSING"
		}
		explanation := models.BuildStrictModeExplanation(gapDecision, gapReasonCode, resolvedProfile, parsed.SourceRowRef, missingRequiredFieldNames)
		governance.RequiredFieldGapDecision = &explanation
	}

	if !governance.SemanticValid {
		log.Printf("⚠️ Semantic Policy Violation for EnvelopeID=%s: %v", in.EnvelopeID, governance.SemanticErrors)
		reasonCode := "SEMANTIC_INVALID"
		if hardStrictRejected {
			reasonCode = "HARD_STRICT_REQUIRED_FIELD_MISSING"
		}
		policyDLQStatus := models.ClassifyDLQ(reasonCode)

		// Build a comprehensive error detail from all governance failure collections
		var errorParts []string
		if len(governance.SemanticErrors) > 0 {
			errorParts = append(errorParts, "semantic errors: "+strings.Join(governance.SemanticErrors, ", "))
		}
		if len(governance.MissingFields) > 0 {
			errorParts = append(errorParts, "missing required fields: "+strings.Join(governance.MissingFields, ", "))
		}
		if len(governance.PolicyFlags) > 0 {
			errorParts = append(errorParts, "policy flags: "+strings.Join(governance.PolicyFlags, ", "))
		}
		errorDetail := strings.Join(errorParts, "; ")
		if errorDetail == "" {
			errorDetail = "semantic policy validation failed"
		}

		intentContext := models.BuildIntentContext(policyDLQStatus, parsed)
		if hardStrictRejected && governance.RequiredFieldGapDecision != nil {
			intentContext = models.BuildIntentContextWithStrictMode(policyDLQStatus, parsed, *governance.RequiredFieldGapDecision)
		}

		dlqEntry := models.DLQEntry{
			TenantID:       in.TenantID.String(),
			EnvelopeID:     in.EnvelopeID.String(),
			Stage:          "POLICY_DLQ",
			ReasonCode:     reasonCode,
			ErrorDetail:    errorDetail,
			DLQStatus:      policyDLQStatus,
			BatchID:        batchIDStr,
			ClientBatchRef: batchIDStr,
			SourceRowNum:   sourceRowNum,
			IntentContext:  intentContext,
			TraceID:        in.TraceID.String(),
			CreatedAt:      time.Now().UTC(),
		}
		// Save to the repository so the database row is created
		savedDLQ, err := s.validator.DLQRepo().Save(ctx, dlqEntry)
		if err != nil {
			log.Printf("Failed to save POLICY_DLQ entry: %v", err)
			retIn = in
			retProfile = resolvedProfile
			retDecrypted = decryptedPayload
			retRawAudit = rawAuditPayload
			retAuditProfileID = auditProfileID
			retAuditProfileVersion = auditProfileVersion
			retSourceRowNum = sourceRowNum
			retDlq = &dlqEntry
			return
		}
		retIn = in
		retProfile = resolvedProfile
		retDecrypted = decryptedPayload
		retRawAudit = rawAuditPayload
		retAuditProfileID = auditProfileID
		retAuditProfileVersion = auditProfileVersion
		retSourceRowNum = sourceRowNum
		retDlq = &savedDLQ
		return
	}

	// FIX: Compute GovernanceHash early (UPDATED)
	// We need a temporary canonical for reason codes aggregation
	tempGovCanonical := &models.CanonicalIntent{
		IntentID:   intentID,
		Governance: governance,
	}
	governanceJSON := s.aggregateGovernanceReasons(tempGovCanonical, nir)
	governanceHash := s.computeGovernanceHashInternal("VALID", string(governanceJSON), "v1", intentID)

	parsed.PayloadHash = in.PayloadHash
	if rawRowHash != "" {
		parsed.RawRowHash = rawRowHash
	}
	parsed.ArtifactID = artifactID
	parsed.ArtifactVersionID = artifactVersionID
	parsed.FieldConfidenceSummary = nir.FieldConfidenceSummary
	parsed.LowConfidenceFieldCount = nir.LowConfidenceFieldCount
	parsed.RequiredFieldGapCount = nir.RequiredFieldGapCount
	parsed.IntentID = intentID

	// -------- STEP 5.5: Idempotency guard --------

	existing, err := s.repo.FindByEnvelope(
		ctx,
		in.TenantID.String(),
		in.EnvelopeID.String(),
	)
	if err != nil {
		retIn = in
		retProfile = resolvedProfile
		retDecrypted = decryptedPayload
		retRawAudit = rawAuditPayload
		retAuditProfileID = auditProfileID
		retAuditProfileVersion = auditProfileVersion
		retSourceRowNum = sourceRowNum
		retErr = err
		return
	}

	if existing != nil {
		// Idempotency cache hit: return immediately, no DB write needed.
		// Set retIn so the wrapper's defer can run audit correctly.
		retIn = in
		retCanonical = existing
		return
	}

	// -------- STEP 6: VALIDATION --------
	batchRef := ""
	if in.BatchID != nil {
		batchRef = *in.BatchID
	}
	intent, dlq, err := s.validator.ValidateParsed(
		ctx,
		in.TenantID.String(),
		in.EnvelopeID.String(),
		parsed,
		batchRef,
		in.TraceID.String(),
		batchIDStr, // ← NEW
	)
	if err != nil {
		retIn = in
		retProfile = resolvedProfile
		retDecrypted = decryptedPayload
		retRawAudit = rawAuditPayload
		retAuditProfileID = auditProfileID
		retAuditProfileVersion = auditProfileVersion
		retSourceRowNum = sourceRowNum
		retErr = err
		return
	}

	if dlq != nil {
		retIn = in
		retProfile = resolvedProfile
		retDecrypted = decryptedPayload
		retRawAudit = rawAuditPayload
		retAuditProfileID = auditProfileID
		retAuditProfileVersion = auditProfileVersion
		retSourceRowNum = sourceRowNum
		retDlq = dlq
		return
	}

	if intent == nil {
		retIn = in
		retProfile = resolvedProfile
		retDecrypted = decryptedPayload
		retRawAudit = rawAuditPayload
		retAuditProfileID = auditProfileID
		retAuditProfileVersion = auditProfileVersion
		retSourceRowNum = sourceRowNum
		retErr = err
		retErr = errors.New("validator returned nil intent")
		return
	}

	// -------- STEP 7: CANONICALIZATION --------

	canonicalInput := canonicalizer.CanonicalizeIntent(*intent)

	// -------- STEP 7.5: PRE-GUARDS --------

	if dlq := guards.RunPreGuards(in, canonicalInput); dlq != nil {
		dlq.TraceID = in.TraceID.String()
		dlq.IntentContext = models.BuildIntentContext(dlq.DLQStatus, *intent)
		retIn = in
		retProfile = resolvedProfile
		retDecrypted = decryptedPayload
		retRawAudit = rawAuditPayload
		retAuditProfileID = auditProfileID
		retAuditProfileVersion = auditProfileVersion
		retSourceRowNum = sourceRowNum
		retDlq = dlq
		return
	}

	// Governance hash computed early at step 6.5
	canonicalInput.GovernanceHash = governanceHash
	canonicalInput.IntentID = intentID // Ensure intent_id is passed to Kafka if needed
	canonicalInput.PayloadHash = in.PayloadHash
	canonicalInput.RawRowHash = rawRowHash
	canonicalInput.ArtifactID = artifactID
	canonicalInput.ArtifactVersionID = artifactVersionID
	canonicalInput.FieldConfidenceSummary = nir.FieldConfidenceSummary
	canonicalInput.LowConfidenceFieldCount = nir.LowConfidenceFieldCount
	canonicalInput.RequiredFieldGapCount = nir.RequiredFieldGapCount

	// -------- STEP 8: TOKENIZATION --------

	tokenReq := enclaveTokenizeRequest{
		TenantID:      in.TenantID.String(),
		TraceID:       in.TraceID.String(),
		ObjectRef:     intentID,
		CorrelationID: in.TraceID.String(),
		PII: map[string]string{
			"account_number": canonicalInput.AccountNumber,
			"ifsc":           canonicalInput.Beneficiary.Instrument.IFSC,
			"vpa":            canonicalInput.Beneficiary.Instrument.VPA,
			"name":           canonicalInput.Beneficiary.Name,
			"phone":          canonicalInput.Remitter.Phone,
			"email":          canonicalInput.Remitter.Email,
		},
	}

	tokenMap, err := callEnclaveTokenize(ctx, tokenReq)

	if err != nil {

		log.Printf("Token enclave unavailable, publishing tokenize request to Kafka: %v", err)

		// -------- KAFKA FALLBACK --------

		if s.tokenizeQueue == nil {
			retIn = in
			retProfile = resolvedProfile
			retDecrypted = decryptedPayload
			retRawAudit = rawAuditPayload
			retAuditProfileID = auditProfileID
			retAuditProfileVersion = auditProfileVersion
			retSourceRowNum = sourceRowNum
			retErr = err
			return
		}

		req := models.TokenizeRequestEvent{
			EventType:      "PII_TOKENIZE_REQUEST",
			TraceID:        in.TraceID.String(),
			EnvelopeID:     in.EnvelopeID.String(),
			TenantID:       in.TenantID.String(),
			ObjectRef:      in.ObjectRef,
			IdempotencyKey: in.IdempotencyKey,
			Source:         in.Source,
			ReceivedAt:     time.Now().UTC(),
			Canonical:      canonicalInput,
			BatchID:        in.BatchID,
		}

		err = s.tokenizeQueue.PublishTokenizeRequest(ctx, req)
		if err != nil {
			log.Printf("Kafka publish failed: %v", err)
			retIn = in
			retProfile = resolvedProfile
			retDecrypted = decryptedPayload
			retRawAudit = rawAuditPayload
			retAuditProfileID = auditProfileID
			retAuditProfileVersion = auditProfileVersion
			retSourceRowNum = sourceRowNum
			retErr = err
			return
		}

		log.Printf("Tokenization request queued in Kafka for EnvelopeID=%s", in.EnvelopeID)

		// Stop pipeline for now
		retIn = in
		retProfile = resolvedProfile
		retDecrypted = decryptedPayload
		retRawAudit = rawAuditPayload
		retAuditProfileID = auditProfileID
		retAuditProfileVersion = auditProfileVersion
		retSourceRowNum = sourceRowNum
		return
	}

	// Persist full token map in pii_tokens JSONB
	piiJSON, _ := json.Marshal(tokenMap)

	// Ledger item #18: record which requested PII fields actually came back
	// tokenized (enclave provenance), not just the pass/fail bool that
	// tokenization_status already carries. No secret values here — only field
	// names — since this rides along on every read of the intent.
	tokenizedFields := make([]string, 0, len(tokenMap))
	requestedFields := make([]string, 0, len(tokenReq.PII))
	for field, val := range tokenMap {
		if val != "" {
			tokenizedFields = append(tokenizedFields, field)
		}
	}
	for field := range tokenReq.PII {
		requestedFields = append(requestedFields, field)
	}
	sort.Strings(tokenizedFields)
	sort.Strings(requestedFields)
	tokenizationMetadataJSON, _ := json.Marshal(map[string]any{
		"method":           "enclave_sync",
		"requested_fields": requestedFields,
		"tokenized_fields": tokenizedFields,
		"tokenized_at":     time.Now().UTC(),
	})

	beneficiaryTokenized := map[string]any{
		"instrument": map[string]any{
			"kind":       canonicalInput.Beneficiary.Instrument.Kind,
			"ifsc_token": tokenMap["ifsc"],
			"vpa_token":  tokenMap["vpa"],
		},
		"name_token": tokenMap["name"],
		"country":    canonicalInput.Beneficiary.Country,
	}

	beneficiaryJSON, _ := json.Marshal(beneficiaryTokenized)
	constraintsJSON, _ := json.Marshal(canonicalInput.Constraints)

	amount, _ := parseAmount(canonicalInput.Amount.Value)

	// -------- STEP 8.5: COMPUTE SCORES & FINGERPRINT --------

	bFingerprint := s.computeBeneficiaryFingerprint(tokenMap)
	timeBucket := time.Now().UTC().Format("2006-01-02")
	// Slice 8 (D18): v2 key (no amount when a reference exists) is stored;
	// the legacy v1 key is still checked during the transition.
	bKeys := s.computeBusinessIdempotencyKeys(
		in.TenantID.String(), in.SourceSystem, canonicalInput.ClientPayoutRef,
		bFingerprint, amount, canonicalInput.Amount.Currency,
		canonicalInput.IntendedExecutionAt, canonicalInput.PurposeCode,
	)
	bIdemKey := bKeys.V2

	// UPDATED: Abnormal amount detection
	var anomalies []string
	if s.isAbnormalAmount(amount, canonicalInput.Amount.Currency) {
		anomalies = append(anomalies, "ABNORMAL_AMOUNT")
	}

	// -------- STEP 8.7: Business Idempotency Registry Check (NEW) --------
	bizMatch, err := resolveBusinessIdempotency(ctx, s.repo, in.TenantID.String(), bKeys,
		amount.Mul(decimal.NewFromInt(100)).IntPart(), canonicalInput.Amount.Currency)
	registryDuplicate := bizMatch.Entry
	if err != nil {
		retIn = in
		retProfile = resolvedProfile
		retDecrypted = decryptedPayload
		retRawAudit = rawAuditPayload
		retAuditProfileID = auditProfileID
		retAuditProfileVersion = auditProfileVersion
		retSourceRowNum = sourceRowNum
		retErr = err
		return
	}

	dupRisk := false
	dupReason := "NONE"
	comparedIntentID := ""
	var registryEntry *models.BusinessIdempotencyEntry

	if registryDuplicate != nil {
		dupRisk = true
		dupReason = registryDuplicate.DuplicateReasonCode
		if dupReason == "" || dupReason == "NONE" {
			dupReason = "SAME_BENEFICIARY_AMOUNT_TIME"
		}
		comparedIntentID = registryDuplicate.IntentID.String()
		if bizMatch.AmountMismatch {
			// Same reference, different amount: never merge silently.
			dupReason = "CLIENT_PAYOUT_REF_REUSED"
			anomalies = append(anomalies, AnomalyAmountChangedSameRef)
		}
	} else {
		// Prepare registry entry for new intent
		registryEntry = &models.BusinessIdempotencyEntry{
			TenantID:               in.TenantID,
			BusinessIdempotencyKey: bIdemKey,
			IntentID:               uuid.Nil, // Will be set after IntentID generated if needed, but here we can use a temp ID or let repo handle it
			BeneficiaryFingerprint: bFingerprint,
			AmountMinor:            amount.Mul(decimal.NewFromInt(100)).IntPart(),
			CurrencyCode:           canonicalInput.Amount.Currency,
			TimeBucket:             timeBucket,
			DuplicateReasonCode:    "NONE",
			CreatedAt:              time.Now().UTC(),
		}
	}

	// Strict duplicate signals (ledger item #11) take precedence over the
	// semantic registry match above — a reused idempotency_key or
	// client_payout_ref is a stronger signal than "similar-looking payment".
	if strictID, serr := s.repo.FindIntentIDByIdempotencyKey(ctx, in.TenantID.String(), in.IdempotencyKey); serr == nil && strictID != "" {
		dupRisk = true
		dupReason = "SAME_IDEMPOTENCY_KEY"
		comparedIntentID = strictID
	} else if refID, serr := s.repo.FindIntentIDByClientPayoutRef(ctx, in.TenantID.String(), canonicalInput.ClientPayoutRef); serr == nil && refID != "" {
		dupRisk = true
		dupReason = "CLIENT_PAYOUT_REF_REUSED"
		comparedIntentID = refID
	}

	var executionAt *time.Time

	if canonicalInput.IntendedExecutionAt != "" {
		t, err := time.Parse(time.RFC3339, canonicalInput.IntendedExecutionAt)
		if err == nil {
			executionAt = &t
		}
	}

	// FIX: Deterministic Request Fingerprint
	reqFingerprint := s.computeRequestFingerprint(
		canonicalInput.Beneficiary.Name,
		amount,
		canonicalInput.AccountNumber,
		canonicalInput.Beneficiary.Instrument.VPA,
		canonicalInput.Amount.Currency,
	)

	// Score requires partial intent for signals
	tempIntent := &models.CanonicalIntent{
		TraceID:                    in.TraceID.String(),
		IntentID:                   intentID,
		EnvelopeID:                 in.EnvelopeID.String(),
		TenantID:                   in.TenantID.String(),
		IdempotencyKey:             in.IdempotencyKey,
		SalientHash:                reqFingerprint,
		PayloadHash:                in.PayloadHash,
		RawRowHash:                 rawRowHash,
		ArtifactID:                 artifactID,
		ArtifactVersionID:          artifactVersionID,
		IntentType:                 canonicalInput.IntentType,
		CanonicalVersion:           "v1",
		SchemaVersion:              canonicalInput.SchemaVersion,
		Amount:                     amount,
		Currency:                   canonicalInput.Amount.Currency,
		IntendedExecutionAt:        executionAt,
		Constraints:                constraintsJSON,
		BeneficiaryType:            canonicalInput.Beneficiary.Instrument.Kind,
		PIITokens:                  piiJSON,
		Beneficiary:                beneficiaryJSON,
		Status:                     "CREATED",
		CreatedAt:                  time.Now().UTC(),
		PaymentInstructionReceived: &in.ReceivedAt,
		CanonicalIntentCreated:     func(t time.Time) *time.Time { return &t }(time.Now().UTC()),
		ClientPayoutRef:            canonicalInput.ClientPayoutRef,
		ProviderHint:               canonicalInput.ProviderHint,
		ClientBatchRef:             batchIDStr,
		RequestFingerprint:         reqFingerprint,
		RoutingHintsJSON:           json.RawMessage(`{}`),
		GovernanceState:            "PENDING",
		BusinessState:              "NEW",
		DuplicateRiskFlag:          dupRisk,
		MappingProfileID:           nir.ProfileID,
		MappingProfileVersion:      nir.ProfileVersion,
		SourceSystem:               in.SourceSystem,
		GovernanceHash:             governanceHash,
		BusinessIdempotencyKey:     bIdemKey,
		BeneficiaryFingerprint:     bFingerprint,
		DuplicateReasonCode:        dupReason,
		BatchID:                    in.BatchID,
		SourceRowNum:               sourceRowNum,
		SourceRowRef:               sourceRowRef,
		ValidationAnomalies:        anomalies,
	}

	// Update governance with duplicate detection results
	if dupRisk {
		governance.DuplicateDetected = true
		governance.DuplicateReason = dupReason
	}

	tokenizationComplete := len(tempIntent.PIITokens) > 2
	schemaScore, mapScore, refQualityScore, mScore, pScore, dupRiskScore, iScore, scoreReasonCodes :=
		s.computeScores(tempIntent, nir, governance, tokenizationComplete)

	// score_validity_status — set based on governance gate
	scoreValidityStatus := models.ScoreValidityScoredValid
	if iScore < 0.70 || len(scoreReasonCodes) > 0 {
		scoreValidityStatus = models.ScoreValidityScoredReview
	}

	scoredAt := time.Now().UTC()
	scoreBreakdown := buildScoreBreakdown(schemaScore, mapScore, refQualityScore, mScore, pScore, dupRiskScore, iScore)
	scoreReasonCodesJSON, _ := json.Marshal(scoreReasonCodes)

	// Link registry entry to intent if it's a new entry
	if registryEntry != nil {
		registryEntry.IntentID = uuid.MustParse(intentID)
	}

	canonical := models.CanonicalIntent{
		TraceID:           in.TraceID.String(),
		IntentID:          intentID,
		EnvelopeID:        in.EnvelopeID.String(),
		TenantID:          in.TenantID.String(),
		IdempotencyKey:    in.IdempotencyKey,
		SalientHash:       reqFingerprint,
		PayloadHash:       in.PayloadHash,
		RawRowHash:        rawRowHash,
		ArtifactID:        artifactID,
		ArtifactVersionID: artifactVersionID,

		IntentType:       canonicalInput.IntentType,
		CanonicalVersion: "v1",
		SchemaVersion:    canonicalInput.SchemaVersion,

		Amount:   amount,
		Currency: canonicalInput.Amount.Currency,

		IntendedExecutionAt: executionAt,
		Constraints:         constraintsJSON,

		BeneficiaryType:      canonicalInput.Beneficiary.Instrument.Kind,
		PIITokens:            piiJSON,
		Beneficiary:          beneficiaryJSON,
		TokenizationMetadata: tokenizationMetadataJSON,

		Status:                     "CREATED",
		CreatedAt:                  time.Now().UTC(),
		PaymentInstructionReceived: &in.ReceivedAt,
		CanonicalIntentCreated:     func(t time.Time) *time.Time { return &t }(time.Now().UTC()),

		ClientPayoutRef:       canonicalInput.ClientPayoutRef,
		ProviderHint:          canonicalInput.ProviderHint,
		ClientBatchRef:        batchIDStr,
		RequestFingerprint:    reqFingerprint,
		RoutingHintsJSON:      json.RawMessage(`{}`),
		GovernanceState:       "PENDING",
		BusinessState:         "NEW",
		DuplicateRiskFlag:     dupRisk,
		MappingProfileID:      nir.ProfileID,
		MappingProfileVersion: nir.ProfileVersion,
		MappingProfileHash:    nir.MappingProfileHash,
		SourceSystem:          in.SourceSystem,
		GovernanceHash:        governanceHash,

		// Service 2 fields
		BusinessIdempotencyKey:   bIdemKey,
		BusinessIdempotencyKeyV1: legacyBusinessKey(bKeys),
		BeneficiaryFingerprint:   bFingerprint,
		ConfidenceScore:          nil, // REMOVED
		ProofReadinessScore:      pScore,
		MatchabilityScore:        mScore,
		IntentQualityScore:       iScore,
		MappingConfidenceScore:   mapScore,
		SchemaCompletenessScore:  schemaScore,
		DuplicateReasonCode:      dupReason,

		// NEW fields:
		ReferenceQualityScore: refQualityScore,
		DuplicateRiskScore:    dupRiskScore,
		ScoreVersion:          models.ScoreVersion,
		ScoreValidityStatus:   scoreValidityStatus,
		ScoreBreakdownJSON:    scoreBreakdown,
		ScoreReasonCodesJSON:  scoreReasonCodesJSON,
		ScoredAt:              &scoredAt,

		UpdatedAt:           func(t time.Time) *time.Time { return &t }(time.Now().UTC()),
		BatchID:             in.BatchID,
		SourceRowNum:        sourceRowNum,
		SourceRowRef:        sourceRowRef,
		ValidationAnomalies: anomalies,
	}
	canonical.CanonicalRowHash = s.computeCanonicalRowHash(&canonical)
	canonical.TokenizedDataHash = s.computeTokenizedDataHash(canonical.TenantID, tokenMap)
	canonical.RawRowEvidenceLeafHash, canonical.CanonicalRowEvidenceLeafHash = s.computeEvidenceLeafHashes(&canonical)

	// -------- STEP 9.1: AGGREGATE GOVERNANCE REASONS --------
	canonical.Governance = governance

	// UPDATED: Determine GovernanceState (VALID / INVALID / FLAGGED)
	canonical.GovernanceState = "VALID"
	if canonical.DuplicateRiskFlag || len(canonical.ValidationAnomalies) > 0 {
		canonical.GovernanceState = "FLAGGED"
	}
	if nir.MappingUncertainFlag || nir.RequiredFieldGapCount > 0 {
		canonical.GovernanceState = "FLAGGED"
	}
	if iScore < 0.5 {
		canonical.GovernanceState = "FLAGGED"
	}

	// Batch-size policy limit (ledger item #10): a batch this large gets
	// held for review rather than blocked outright, since we've never
	// enforced this before and don't want day-one enforcement to reject a
	// legitimate large batch from an existing tenant.
	if in.RowCountEstimate != nil && *in.RowCountEstimate > guards.MaxBatchSize {
		canonical.GovernanceState = "REQUIRES_REVIEW"
		canonical.Governance.PolicyFlags = append(canonical.Governance.PolicyFlags, "BATCH_SIZE_EXCEEDS_LIMIT")
	}

	// Tenant daily-amount policy limit (R-05): atomic per-(tenant,
	// business_date, currency) reservation, not a stale pre-read total.
	// Locking the usage row inside one transaction — rather than reading a
	// snapshot before this row is decided — is what makes this safe against
	// two concurrent requests (or two rows in the same batch) both reading
	// the same total and both passing: the second one to reach the lock
	// always sees the first one's committed increment. A held (REQUIRES_
	// REVIEW) amount is never added to accepted_amount — only ACCEPT does.
	//
	// Known limitation: an ACCEPT reservation commits before this row is
	// actually persisted to payment_intents (repo.Save happens later, in
	// the caller). If that later save fails, the reservation is not rolled
	// back — the tenant's usage row reflects a slightly higher total than
	// what's actually in payment_intents until the next request corrects
	// course. A full compensating-transaction/saga wasn't built for this
	// rare failure mode; none of R-05's acceptance tests exercise it.
	businessDate := s.resolveBusinessDate(ctx, canonical.TenantID)
	dailyLimit := guards.DailyLimitForCurrency(canonical.Currency)
	dailyLimitDecision, dailyTotalBefore, errDailyUsage := s.tenantDailyUsage.ReserveIfWithinLimit(
		ctx, canonical.TenantID, businessDate, canonical.Currency,
		canonical.Amount, dailyLimit,
	)
	reservationErrored := errDailyUsage != nil
	if reservationErrored {
		// Fail safe, not fail open: R-05 exists because the old check could
		// be silently bypassed. An usage-tracking failure holds for review
		// rather than risking an unverified amount passing as ACCEPTED.
		recordDailyLimitReservationFailure(ctx, canonical.TenantID, canonical.Currency, businessDate, canonical.Amount, errDailyUsage)
		dailyLimitDecision = persistence.DailyLimitDecisionRequiresReview
		dailyTotalBefore = decimal.Zero
	} else {
		recordDailyLimitReservation(ctx, canonical.TenantID, canonical.Currency, businessDate, dailyLimitDecision, canonical.Amount)
	}
	if dailyLimitDecision == persistence.DailyLimitDecisionRequiresReview {
		canonical.GovernanceState = "REQUIRES_REVIEW"
		canonical.Governance.PolicyFlags = append(canonical.Governance.PolicyFlags, DailyLimitPolicyFlagFor(reservationErrored))
	}

	canonical.GovernanceReasonCodesJSON = s.aggregateGovernanceReasons(&canonical, nir)

	// 🆕 Status Fields
	govDec := "Pass"
	if canonical.GovernanceState == "FLAGGED" || canonical.GovernanceState == "REQUIRES_REVIEW" {
		govDec = "Fail"
	}
	canonical.GovernanceDecision = &govDec

	// A row only ever reaches payment_intents once it has cleared governance,
	// so the only lifecycle states reachable here today are ACCEPTED and
	// FLAGGED_FOR_REVIEW. The remaining states in the CHECK constraint are
	// reserved for later phases (policy engine, duplicate decisions, evidence,
	// dispatch readiness) that don't persist a state transition yet.
	canonical.IntentLifecycleState = "ACCEPTED"
	if canonical.GovernanceState == "FLAGGED" || canonical.GovernanceState == "REQUIRES_REVIEW" {
		canonical.IntentLifecycleState = "FLAGGED_FOR_REVIEW"
	}

	reqStatus := nir.RequiredFieldGapCount == 0
	canonical.RequiredFieldsStatus = &reqStatus

	tokStatus := true
	canonical.TokenizationStatus = &tokStatus

	// Finalize governance_decision_hash + input_facts_hash now that
	// GovernanceState, DuplicateRiskFlag and Amount are all settled.
	// dailyTotalBefore is the accepted total prior to this reservation —
	// an audit fact about what the decision was based on, not the
	// post-reservation total.
	canonical.GovernanceHash = s.computeGovernanceHash(
		&canonical,
		canonicalInput.PurposeCode,
		dailyTotalBefore.Mul(decimal.NewFromInt(100)).IntPart(),
	)

	retPolicyDecision = buildIntentPolicyDecision(
		canonical.TenantID, canonical.IntentID, canonical.GovernanceState,
		append(append([]string{}, canonical.Governance.SemanticErrors...), canonical.Governance.PolicyFlags...),
		policyInputFacts(&canonical, in.RowCountEstimate),
	)
	canonical.PolicySource, canonical.PolicyVersion, canonical.PolicyHash =
		retPolicyDecision.PolicySource, retPolicyDecision.PolicyVersion, retPolicyDecision.PolicyHash

	retDuplicateDecision = buildDuplicateDecision(
		canonical.TenantID, canonical.IntentID, dupReason, dupRiskScore, comparedIntentID,
		bIdemKey, in.IdempotencyKey, canonicalInput.ClientPayoutRef,
		policyInputFacts(&canonical, in.RowCountEstimate),
	)

	// -------- RETURN PREPARED VALUES --------
	canonicalPayload, err := json.Marshal(canonical)
	if err != nil {
		log.Printf("⚠️ Failed to marshal canonical intent: %v", err)
		retErr = err
		return
	}

	outbox, err := CanonicalIntentToOutboxEvent(canonical, canonicalPayload, EventTypeCanonicalIntentCreatedV1)
	if err != nil {
		log.Printf("⚠️ Failed to create outbox event: %v", err)
		retErr = err
		return
	}

	retIn = in
	retProfile = resolvedProfile
	retDecrypted = decryptedPayload
	retRawAudit = rawAuditPayload
	retAuditProfileID = auditProfileID
	retAuditProfileVersion = auditProfileVersion
	retSourceRowNum = sourceRowNum
	retNir = nir
	retCanonical = &canonical
	retOutbox = &outbox
	retRegistry = registryEntry
	return
}

func (s *IntentService) ProcessIncomingIntent(
	ctx context.Context,
	event *models.Event,
) (retCanonical *models.CanonicalIntent, retDlq *models.DLQEntry, retErr error) {

	var in *models.IncomingIntent
	var resolvedProfile *models.MappingProfile
	var decryptedPayload []byte
	var rawAuditPayload []byte
	var auditProfileID string
	var auditProfileVersion string
	var sourceRowNum *int

	defer func() {
		if retDlq != nil && retDlq.SourceRowNum == nil && sourceRowNum != nil {
			retDlq.SourceRowNum = sourceRowNum
		}

		if in == nil || in.BatchID == nil || *in.BatchID == "" {
			return
		}

		if retErr != nil {
			return
		}

		var status = "ACCEPTED"
		var errDetail = ""
		var mappingID = ""
		var profileIDHint = ""

		if retDlq != nil {
			if retDlq.ReasonCode == "DUPLICATE_BUSINESS_KEY" || retDlq.ReasonCode == "DUPLICATE_IDEMPOTENCY_KEY" {
				status = "DUPLICATE"
			} else {
				status = "FAILED"
			}
			errDetail = retDlq.ReasonCode
		}

		if resolvedProfile != nil {
			mappingID = resolvedProfile.ProfileID
			profileIDHint = resolvedProfile.ProfileVersion
		} else if auditProfileID != "" {
			mappingID = auditProfileID
			profileIDHint = auditProfileVersion
		}

		auditPayload := rawAuditPayload
		if len(auditPayload) == 0 {
			auditPayload = decryptedPayload
		}
		rowIndex := 0
		if sourceRowNum != nil {
			rowIndex = *sourceRowNum
		}

		fileName := ""
		fileHash := ""
		var totalRows int = 0

		if in.FileName != nil {
			fileName = *in.FileName
		}
		if in.FileContentHash != nil {
			fileHash = *in.FileContentHash
		}
		if in.RowCountEstimate != nil {
			totalRows = *in.RowCountEstimate
		}

		runID, errRun := db.EnsureIngestRun(ctx, s.db, in.TenantID.String(), *in.BatchID)
		if errRun != nil {
			log.Printf("⚠️ Audit: failed to ensure ingest run for batch=%s: %v", *in.BatchID, errRun)
			// Fall back to a fresh id so the row/run upserts below still have a
			// valid UUID to write — best-effort audit, not worth failing the intent over.
			runID = uuid.New().String()
		}

		errInsert := db.InsertIngestRow(ctx, s.db,
			runID, *in.BatchID, in.TenantID.String(), mappingID, profileIDHint,
			rowIndex, in.IdempotencyKey, status, errDetail, in.SourceSystem,
			fileName, fileHash, auditPayload,
		)
		if errInsert != nil {
			log.Printf("⚠️ Audit: failed to insert row audit for batch=%s row=%d: %v", *in.BatchID, rowIndex, errInsert)
		}

		acceptedCount := 0
		failedCount := 0
		duplicateCount := 0
		errStats := s.db.QueryRowContext(ctx, `
			SELECT
				COUNT(*) FILTER (WHERE status = 'ACCEPTED'),
				COUNT(*) FILTER (WHERE status = 'FAILED'),
				COUNT(*) FILTER (WHERE status = 'DUPLICATE')
			FROM intent_ingest_rows
			WHERE tenant_id = $1 AND batch_id = $2`,
			in.TenantID.String(), *in.BatchID,
		).Scan(&acceptedCount, &failedCount, &duplicateCount)

		if errStats != nil {
			log.Printf("⚠️ Audit: failed to query batch stats for batch=%s: %v", *in.BatchID, errStats)
			return
		}

		processedRows := acceptedCount + failedCount + duplicateCount
		hasTotalRows := totalRows > 0
		if !hasTotalRows {
			totalRows = processedRows
		}

		runStatus := "PROCESSING"
		if processedRows > 0 && processedRows >= totalRows {
			runStatus = "COMPLETED"
		}

		runLastErrorCode := ""
		if status == "FAILED" {
			runLastErrorCode = errDetail
		}

		errUpsert := db.UpsertIngestRun(ctx, s.db,
			runID, *in.BatchID, in.TenantID.String(),
			mappingID, profileIDHint, fileName, fileHash,
			totalRows, acceptedCount, failedCount, duplicateCount,
			runStatus,
			runLastErrorCode, runLastErrorCode,
		)
		if errUpsert != nil {
			log.Printf("⚠️ Audit: failed to upsert run audit for batch=%s: %v", *in.BatchID, errUpsert)
		}
		if errUpsert == nil && runStatus == "COMPLETED" {
			batchKey := fmt.Sprintf("%s|%s", in.TenantID.String(), *in.BatchID)
			_, aggErr, _ := batchAggregateGroup.Do(batchKey, func() (interface{}, error) {
				return s.repo.UpdateBatchAggregateConfidence(context.Background(), in.TenantID.String(), *in.BatchID)
			})
			if aggErr != nil {
				log.Printf("âš ï¸ Failed to update batch aggregate confidence before vector emit for batch=%s: %v", *in.BatchID, aggErr)
			}

			s.maybeEmitCompletedBatchVectorIndex(ctx, in.TenantID.String(), *in.BatchID)
		}
	}()

	var nir *models.NormalizedIngestRecord
	var canonical *models.CanonicalIntent
	var outbox *models.OutboxEvent
	var registryEntry *models.BusinessIdempotencyEntry
	var policyDecision *models.IntentPolicyDecision
	var duplicateDecision *models.DuplicateDecision

	in, resolvedProfile, decryptedPayload, rawAuditPayload, auditProfileID, auditProfileVersion, sourceRowNum,
		nir, canonical, outbox, registryEntry, retDlq, retErr, policyDecision, duplicateDecision = s.processIncomingIntentInternal(ctx, event)
	if retErr != nil {
		return nil, nil, retErr
	}
	if retDlq != nil {
		if in.Source == "WEBHOOK" {
			// Webhook saves itself inside processWebhook
			return nil, retDlq, nil
		}
		// DLQ entries are saved by the caller in main.go
		return nil, retDlq, nil
	}

	if in.Source == "WEBHOOK" {
		// Webhook has already been saved inside processWebhook
		return canonical, nil, nil
	}

	// Idempotency cache hit: outbox is nil because no new intent was built.
	// Return the existing canonical directly — no DB write.
	if outbox == nil {
		return canonical, nil, nil
	}

	saved, err := s.repo.Save(ctx, nir, *canonical, *outbox, registryEntry, policyDecision, duplicateDecision)
	if err != nil {
		log.Printf("⚠️ Repo.Save failed for EnvelopeID=%s: %v", in.EnvelopeID, err)
		retErr = err
		return nil, nil, err
	}

	canonicalVal := saved
	version := 1
	prevHash, err := s.repo.GetPreviousTenantCanonicalHash(ctx, saved.TenantID, saved.IntentID)
	if err != nil {
		retErr = err
		return nil, nil, err
	}

	canonicalBytes, err := json.Marshal(saved)
	if err != nil {
		retErr = err
		return nil, nil, err
	}

	canonicalRef, hash, err := s.s3.StoreSnapshot(ctx, "canonical", saved.TenantID, saved.IntentID, version, canonicalBytes, prevHash)
	if err != nil {
		log.Printf("⚠️ S3 Canonical Snapshot failed: %v. Warning: WORM metadata will remain null.", err)
		return &saved, nil, nil
	}

	var nirRef string
	nirBytes, _ := json.Marshal(nir)
	nirRef, _, err = s.s3.StoreSnapshot(ctx, "nir", saved.TenantID, saved.IntentID, version, nirBytes, "")
	if err != nil {
		log.Printf("⚠️ S3 NIR Snapshot failed: %v", err)
	}

	govBytes := []byte(`{"state":"` + canonicalVal.GovernanceState + `"}`)
	govRef, _, err := s.s3.StoreSnapshot(ctx, "governance", saved.TenantID, saved.IntentID, version, govBytes, "")
	if err != nil {
		log.Printf("⚠️ S3 Governance Snapshot failed: %v", err)
	}

	saved.CanonicalHash = hash
	newGovernanceHash := s.recomputeGovernanceDecisionHash(&saved)

	err = s.repo.UpdateSnapshotRefs(ctx, saved.TenantID, saved.IntentID, canonicalRef, nirRef, govRef, hash, prevHash, newGovernanceHash)
	if err != nil {
		retErr = err
		return nil, nil, err
	}

	saved.CanonicalSnapshotRef = canonicalRef
	saved.NIRSnapshotRef = nirRef
	saved.GovernanceSnapshotRef = govRef
	saved.GovernanceHash = newGovernanceHash

	if in.BatchID != nil && *in.BatchID != "" {
		batchKey := fmt.Sprintf("%s|%s", in.TenantID.String(), *in.BatchID)
		_, err, _ := batchAggregateGroup.Do(batchKey, func() (interface{}, error) {
			return s.repo.UpdateBatchAggregateConfidence(context.Background(), in.TenantID.String(), *in.BatchID)
		})
		if err != nil {
			log.Printf("⚠️ Failed to update batch aggregate confidence for batch=%s: %v", *in.BatchID, err)
		}
	}
	batchID := ""
	if in.BatchID != nil {
		batchID = *in.BatchID
	}

	if batchID != "" {
		// Batch-level vector indexing is emitted once from the batch completion path.
		// Do not emit one vector event per row here, otherwise large uploads create noisy duplicate events.
		log.Printf("[intent-engine][vector-index] defer batch vector emit tenant=%s batch_id=%s intent_id=%s", saved.TenantID, batchID, saved.IntentID)
	} else {
		s.emitVectorIndexRequest(
			"payment_intent.saved.v1",
			saved.TenantID,
			"payment_intent",
			saved.IntentID,
			"",
			map[string]string{
				"governance_state":     saved.GovernanceState,
				"vector_summary_scope": "single_intent",
			},
		)
	}
	return &saved, nil, nil
}

func (s *IntentService) ProcessIncomingIntentsBatch(
	ctx context.Context,
	events []*models.Event,
) ([]models.CanonicalIntent, []models.DLQEntry, error) {
	if len(events) == 0 {
		return nil, nil, nil
	}

	var batchItems []models.SaveBatchItem
	var ingestRows []db.IngestRowItem

	var firstIn *models.IncomingIntent
	var resolvedMappingID string
	var resolvedProfileVersion string

	// All events in one call share the same batch upload, so a single stable
	// run_id covers the whole batch — resolved once upfront rather than per row.
	var batchRunID string
	if events[0].BatchID != nil && *events[0].BatchID != "" {
		if id, errRun := db.EnsureIngestRun(ctx, s.db, events[0].TenantID.String(), *events[0].BatchID); errRun != nil {
			log.Printf("⚠️ ProcessIncomingIntentsBatch: failed to ensure ingest run for batch=%s: %v", *events[0].BatchID, errRun)
			batchRunID = uuid.New().String()
		} else {
			batchRunID = id
		}
	}

	// R-05: unlike batchRunID above, the daily-amount limit is deliberately
	// NOT resolved once upfront here. Each row now reserves its own slice of
	// the tenant's daily limit atomically inside processIncomingIntentInternal,
	// so row N in this loop correctly sees rows 1..N-1's already-committed
	// usage — a single stale total shared across the whole batch previously
	// let every row individually look fine against the same starting number
	// while collectively blowing through the limit.
	for _, event := range events {
		in, resolvedProfile, decryptedPayload, rawAuditPayload, auditProfileID, auditProfileVersion, sourceRowNum,
			nir, canonical, outbox, registryEntry, dlq, err, policyDecision, duplicateDecision := s.processIncomingIntentInternal(ctx, event)
		if err != nil {
			log.Printf("⚠️ ProcessIncomingIntentsBatch: system error preparing intent: %v", err)
			continue
		}

		if firstIn == nil && in != nil {
			firstIn = in
			if resolvedProfile != nil {
				resolvedMappingID = resolvedProfile.ProfileID
				resolvedProfileVersion = resolvedProfile.ProfileVersion
			} else if auditProfileID != "" {
				resolvedMappingID = auditProfileID
				resolvedProfileVersion = auditProfileVersion
			}
		}

		status := "ACCEPTED"
		errDetail := ""
		mappingID := ""
		profileIDHint := ""

		if dlq != nil {
			if dlq.ReasonCode == "DUPLICATE_BUSINESS_KEY" || dlq.ReasonCode == "DUPLICATE_IDEMPOTENCY_KEY" {
				status = "DUPLICATE"
			} else {
				status = "FAILED"
			}
			errDetail = dlq.ReasonCode
			if dlq.SourceRowNum == nil && sourceRowNum != nil {
				dlq.SourceRowNum = sourceRowNum
			}
		}

		if resolvedProfile != nil {
			mappingID = resolvedProfile.ProfileID
			profileIDHint = resolvedProfile.ProfileVersion
		} else if auditProfileID != "" {
			mappingID = auditProfileID
			profileIDHint = auditProfileVersion
		}

		auditPayload := rawAuditPayload
		if len(auditPayload) == 0 {
			auditPayload = decryptedPayload
		}
		rowIndex := 0
		if sourceRowNum != nil {
			rowIndex = *sourceRowNum
		}

		fileName := ""
		fileHash := ""
		if in != nil {
			if in.FileName != nil {
				fileName = *in.FileName
			}
			if in.FileContentHash != nil {
				fileHash = *in.FileContentHash
			}

			ingestRows = append(ingestRows, db.IngestRowItem{
				RunID:          batchRunID,
				BatchID:        *in.BatchID,
				TenantID:       in.TenantID.String(),
				MappingID:      mappingID,
				ProfileID:      profileIDHint,
				RowIndex:       rowIndex,
				IdempotencyKey: in.IdempotencyKey,
				Status:         status,
				ErrorDetail:    errDetail,
				SourceSystem:   in.SourceSystem,
				FileName:       fileName,
				FileHash:       fileHash,
				RawRowJSON:     auditPayload,
			})
		}

		// Skip idempotency cache hits and webhook paths — outbox is nil, nothing to write.
		if outbox != nil || dlq != nil {
			batchItems = append(batchItems, models.SaveBatchItem{
				Nir:               nir,
				Intent:            canonical,
				Outbox:            outbox,
				RegistryEntry:     registryEntry,
				DlqEntry:          dlq,
				PolicyDecision:    policyDecision,
				DuplicateDecision: duplicateDecision,
			})
		}
	}

	savedIntents, savedDLQs, err := s.repo.SaveBatch(ctx, batchItems)
	if err != nil {
		return nil, nil, fmt.Errorf("ProcessIncomingIntentsBatch: SaveBatch failed: %w", err)
	}

	for _, saved := range savedIntents {
		version := 1
		prevHash, err := s.repo.GetPreviousTenantCanonicalHash(ctx, saved.TenantID, saved.IntentID)
		if err != nil {
			log.Printf("⚠️ Batch S3 Snapshot: failed to get previous hash for intent %s: %v", saved.IntentID, err)
			continue
		}

		canonicalBytes, err := json.Marshal(saved)
		if err != nil {
			continue
		}

		canonicalRef, hash, err := s.s3.StoreSnapshot(ctx, "canonical", saved.TenantID, saved.IntentID, version, canonicalBytes, prevHash)
		if err != nil {
			log.Printf("⚠️ Batch S3 Canonical Snapshot failed: %v", err)
			continue
		}

		var nirRef string
		for _, item := range batchItems {
			if item.Intent != nil && item.Intent.IntentID == saved.IntentID && item.Nir != nil {
				nirBytes, _ := json.Marshal(item.Nir)
				nirRef, _, _ = s.s3.StoreSnapshot(ctx, "nir", saved.TenantID, saved.IntentID, version, nirBytes, "")
				break
			}
		}

		govBytes := []byte(`{"state":"` + saved.GovernanceState + `"}`)
		govRef, _, _ := s.s3.StoreSnapshot(ctx, "governance", saved.TenantID, saved.IntentID, version, govBytes, "")

		saved.CanonicalHash = hash
		newGovernanceHash := s.recomputeGovernanceDecisionHash(&saved)

		err = s.repo.UpdateSnapshotRefs(ctx, saved.TenantID, saved.IntentID, canonicalRef, nirRef, govRef, hash, prevHash, newGovernanceHash)
		if err != nil {
			log.Printf("⚠️ Batch S3: UpdateSnapshotRefs failed for intent %s: %v", saved.IntentID, err)
		}
	}

	if len(ingestRows) > 0 {
		errAudit := db.InsertIngestRowsBatch(ctx, s.db, ingestRows)
		if errAudit != nil {
			log.Printf("⚠️ Batch Audit: failed to insert row audits: %v", errAudit)
		}
	}

	if firstIn != nil && firstIn.BatchID != nil && *firstIn.BatchID != "" {
		// Use the counts we actually just wrote as the ground truth.
		// Do NOT re-query intent_ingest_rows here — that table may not yet reflect
		// all rows (chunked inserts just finished) and would produce a stale count
		// that UpdateBatchAggregateConfidence then uses to override canonicalized_count.
		actualAccepted := len(savedIntents)
		actualFailed := len(savedDLQs)
		actualDuplicate := 0 // duplicates were handled inside SaveBatch (ON CONFLICT)

		totalRows := actualAccepted + actualFailed + actualDuplicate
		if firstIn.RowCountEstimate != nil && *firstIn.RowCountEstimate > totalRows {
			// Trust the file's declared row count if higher (some rows may still be
			// pending from a concurrent per-event path on the same batch).
			totalRows = *firstIn.RowCountEstimate
		}

		fileName := ""
		fileHash := ""
		if firstIn.FileName != nil {
			fileName = *firstIn.FileName
		}
		if firstIn.FileContentHash != nil {
			fileHash = *firstIn.FileContentHash
		}

		runStatus := "COMPLETED"
		processedRows := actualAccepted + actualFailed + actualDuplicate
		if firstIn.RowCountEstimate != nil && *firstIn.RowCountEstimate > processedRows {
			runStatus = "PROCESSING"
		}

		if batchRunID == "" {
			batchRunID = uuid.New().String()
		}
		runLastErrorCode := ""
		if len(savedDLQs) > 0 {
			runLastErrorCode = savedDLQs[len(savedDLQs)-1].ReasonCode
		}

		errUpsert := db.UpsertIngestRun(ctx, s.db,
			batchRunID, *firstIn.BatchID, firstIn.TenantID.String(),
			resolvedMappingID, resolvedProfileVersion, fileName, fileHash,
			totalRows, actualAccepted, actualFailed, actualDuplicate,
			runStatus,
			runLastErrorCode, runLastErrorCode,
		)
		if errUpsert != nil {
			log.Printf("⚠️ Batch Audit: failed to upsert run audit for batch=%s: %v", *firstIn.BatchID, errUpsert)
		}

		batchKey := fmt.Sprintf("%s|%s", firstIn.TenantID.String(), *firstIn.BatchID)
		_, err, _ := batchAggregateGroup.Do(batchKey, func() (interface{}, error) {
			return s.repo.UpdateBatchAggregateConfidence(context.Background(), firstIn.TenantID.String(), *firstIn.BatchID)
		})
		if err != nil {
			log.Printf("⚠️ Failed to update batch aggregate confidence for batch=%s: %v", *firstIn.BatchID, err)
		}
	}
	emittedBatches := map[string]string{}

	for _, saved := range savedIntents {
		batchID := ""
		if saved.BatchID != nil {
			batchID = strings.TrimSpace(*saved.BatchID)
		}
		if batchID != "" {
			emittedBatches[batchID] = saved.TenantID
		}
	}

	for _, dlq := range savedDLQs {
		batchID := strings.TrimSpace(dlq.BatchID)
		if batchID != "" {
			emittedBatches[batchID] = dlq.TenantID
			continue
		}

	}

	for batchID, tenantID := range emittedBatches {
		log.Printf("[intent-engine][vector-index] checking final batch emit tenant=%s batch_id=%s", tenantID, batchID)
		s.maybeEmitCompletedBatchVectorIndex(ctx, tenantID, batchID)
	}
	return savedIntents, savedDLQs, nil
}

/* ---------------- ASYNC TOKENIZATION RESULT (KAFKA) ---------------- */

// ProcessTokenizeResult resumes the pipeline when tokenization
// result arrives asynchronously from Kafka (pii.tokenize.result)
func (s *IntentService) ProcessTokenizeResult(
	ctx context.Context,
	event *models.TokenizeResultEvent,
) (*models.CanonicalIntent, error) {

	log.Printf("ProcessTokenizeResult: EnvelopeID=%s", event.EnvelopeID)

	tokenMap := event.Tokens
	canonicalInput := event.Canonical
	sourceRowNum := sourceRowNumFromRef(canonicalInput.SourceRowRef)

	// -------- JSON fields --------

	piiJSON, err := json.Marshal(tokenMap)
	if err != nil {
		return nil, err
	}

	// Ledger item #18: same provenance record as the sync enclave path, tagged
	// with the async method — this result arrived via the Kafka tokenize-queue
	// fallback, not a live enclave call.
	tokenizedFields := make([]string, 0, len(tokenMap))
	for field, val := range tokenMap {
		if val != "" {
			tokenizedFields = append(tokenizedFields, field)
		}
	}
	sort.Strings(tokenizedFields)
	tokenizationMetadataJSON, _ := json.Marshal(map[string]any{
		"method":           "enclave_async_kafka",
		"tokenized_fields": tokenizedFields,
		"tokenized_at":     time.Now().UTC(),
	})

	beneficiaryTokenized := map[string]any{
		"instrument": map[string]any{
			"kind":       canonicalInput.Beneficiary.Instrument.Kind,
			"ifsc_token": tokenMap["ifsc"],
			"vpa_token":  tokenMap["vpa"],
		},
		"name_token": tokenMap["name"],
		"country":    canonicalInput.Beneficiary.Country,
	}

	beneficiaryJSON, err := json.Marshal(beneficiaryTokenized)
	if err != nil {
		return nil, err
	}

	constraintsJSON, err := json.Marshal(canonicalInput.Constraints)
	if err != nil {
		return nil, err
	}

	amount, err := parseAmount(canonicalInput.Amount.Value)
	if err != nil {
		return nil, err
	}

	batchIDStr := ""
	if event.BatchID != nil {
		batchIDStr = *event.BatchID
	}

	// -------- Build NIR (Reconstructed for async flow) --------
	fieldsMap := make(map[string]models.NIRField)
	fieldsMap["intent_type"] = models.NIRField{Value: canonicalInput.IntentType, SourcePath: "KAFKA_RECONSTRUCTED", ConfidenceScore: 1.0, SensitiveFlag: false, TransformApplied: "NONE", ExtractionNotes: ""}
	fieldsMap["amount"] = models.NIRField{Value: canonicalInput.Amount.Value, SourcePath: "KAFKA_RECONSTRUCTED", ConfidenceScore: 1.0, SensitiveFlag: false, TransformApplied: "NONE", ExtractionNotes: ""}
	fieldsMap["currency"] = models.NIRField{Value: canonicalInput.Amount.Currency, SourcePath: "KAFKA_RECONSTRUCTED", ConfidenceScore: 1.0, SensitiveFlag: false, TransformApplied: "NONE", ExtractionNotes: ""}
	fieldsMap["beneficiary_name"] = models.NIRField{Value: canonicalInput.Beneficiary.Name, SourcePath: "KAFKA_RECONSTRUCTED", ConfidenceScore: 1.0, SensitiveFlag: false, TransformApplied: "NONE", ExtractionNotes: ""}
	fieldsMap["client_batch_ref"] = models.NIRField{Value: canonicalInput.ClientBatchRef, SourcePath: "KAFKA_RECONSTRUCTED", ConfidenceScore: 1.0, SensitiveFlag: false, TransformApplied: "NONE", ExtractionNotes: ""}
	fieldsMap["client_payout_ref"] = models.NIRField{Value: canonicalInput.ClientPayoutRef, SourcePath: "KAFKA_RECONSTRUCTED", ConfidenceScore: 1.0, SensitiveFlag: false, TransformApplied: "NONE", ExtractionNotes: ""}
	fieldsMap["provider_hint"] = models.NIRField{Value: canonicalInput.ProviderHint, SourcePath: "KAFKA_RECONSTRUCTED", ConfidenceScore: 1.0, SensitiveFlag: false, TransformApplied: "NONE", ExtractionNotes: ""}
	fieldsMap["intended_execution_at"] = models.NIRField{Value: canonicalInput.IntendedExecutionAt, SourcePath: "KAFKA_RECONSTRUCTED", ConfidenceScore: 1.0, SensitiveFlag: false, TransformApplied: "NONE", ExtractionNotes: ""}
	fieldsJSON, _ := json.Marshal(fieldsMap)

	profileID := "kafka_async_profile"
	if event.SourceSystem != "" {
		profileID = fmt.Sprintf("%s_%s_async_profile", event.TenantID, strings.ToLower(event.SourceSystem))
	}

	profileVersion := "v1"
	if canonicalInput.SchemaVersion != "" {
		profileVersion = canonicalInput.SchemaVersion
	}

	fieldConfSummary := json.RawMessage(`{"overall": 0.9}`)
	if len(canonicalInput.FieldConfidenceSummary) > 0 {
		fieldConfSummary = canonicalInput.FieldConfidenceSummary
	}
	lowConfCount := canonicalInput.LowConfidenceFieldCount
	gapCount := canonicalInput.RequiredFieldGapCount

	// No registered profile is resolved on this async/reconstructed path, so
	// mapping_profile_hash is derived from the KAFKA_RECONSTRUCTED field
	// mappings above — never left blank.
	profileHash := s.computeGenericMappingProfileHash(profileID, profileVersion, event.SourceSystem, "json", fieldsMap)

	nir := &models.NormalizedIngestRecord{
		NIRID:                   uuid.New(),
		EnvelopeID:              uuid.MustParse(event.EnvelopeID),
		TenantID:                uuid.MustParse(event.TenantID),
		DetectedFormat:          "json",
		ProfileID:               profileID,
		ProfileVersion:          profileVersion,
		FieldsJSON:              fieldsJSON,
		FieldConfidenceSummary:  fieldConfSummary,
		UnmappedJSON:            json.RawMessage(`{}`),
		MappingProfileHash:      profileHash,
		MappingUncertainFlag:    false,
		RequiredFieldGapCount:   gapCount,
		LowConfidenceFieldCount: lowConfCount,
		CreatedAt:               time.Now().UTC(),
		// No resolvedProfile in this async (post-tokenization) flow, so no hash to snapshot.
	}

	// -------- COMPUTE SCORES & FINGERPRINT --------
	// Reconstruct governance for async flow
	governance := s.ApplyPolicy(nir, canonicalInput)

	bFingerprint := s.computeBeneficiaryFingerprint(tokenMap)
	timeBucket := time.Now().UTC().Format("2006-01-02")
	bKeys := s.computeBusinessIdempotencyKeys(
		event.TenantID, event.SourceSystem, canonicalInput.ClientPayoutRef,
		bFingerprint, amount, canonicalInput.Amount.Currency,
		canonicalInput.IntendedExecutionAt, canonicalInput.PurposeCode,
	)
	bIdemKey := bKeys.V2

	// UPDATED: Abnormal amount detection
	var anomalies []string
	if s.isAbnormalAmount(amount, canonicalInput.Amount.Currency) {
		anomalies = append(anomalies, "ABNORMAL_AMOUNT")
	}

	// -------- Business Idempotency Registry Check (NEW) --------
	bizMatch, err := resolveBusinessIdempotency(ctx, s.repo, event.TenantID, bKeys,
		amount.Mul(decimal.NewFromInt(100)).IntPart(), canonicalInput.Amount.Currency)
	if err != nil {
		return nil, err
	}
	registryDuplicate := bizMatch.Entry

	dupRisk := false
	dupReason := "NONE"
	comparedIntentID := ""
	var registryEntry *models.BusinessIdempotencyEntry

	if registryDuplicate != nil {
		dupRisk = true
		dupReason = registryDuplicate.DuplicateReasonCode
		if dupReason == "" {
			dupReason = "SAME_BENEFICIARY_AMOUNT_TIME"
		}
		comparedIntentID = registryDuplicate.IntentID.String()
		if bizMatch.AmountMismatch {
			// Same reference, different amount: never merge silently.
			dupReason = "CLIENT_PAYOUT_REF_REUSED"
			anomalies = append(anomalies, AnomalyAmountChangedSameRef)
		}
	} else {
		// Prepare registry entry
		registryEntry = &models.BusinessIdempotencyEntry{
			TenantID:               uuid.MustParse(event.TenantID),
			BusinessIdempotencyKey: bIdemKey,
			IntentID:               uuid.Nil, // Set below
			BeneficiaryFingerprint: bFingerprint,
			AmountMinor:            amount.Mul(decimal.NewFromInt(100)).IntPart(),
			CurrencyCode:           canonicalInput.Amount.Currency,
			TimeBucket:             timeBucket,
			DuplicateReasonCode:    "NONE",
			CreatedAt:              time.Now().UTC(),
		}
	}

	intentID := uuid.NewString()

	if registryEntry != nil {
		registryEntry.IntentID = uuid.MustParse(intentID)
	}

	var executionAt *time.Time
	if canonicalInput.IntendedExecutionAt != "" {
		t, err := time.Parse(time.RFC3339, canonicalInput.IntendedExecutionAt)
		if err == nil {
			executionAt = &t
		}
	}

	idempotencyKey := event.IdempotencyKey
	if idempotencyKey == "" {
		idempotencyKey = canonicalInput.IdempotencyKey
	}

	// Strict duplicate signals take precedence over the semantic registry
	// match above — see single-item path for rationale.
	if strictID, serr := s.repo.FindIntentIDByIdempotencyKey(ctx, event.TenantID, idempotencyKey); serr == nil && strictID != "" {
		dupRisk = true
		dupReason = "SAME_IDEMPOTENCY_KEY"
		comparedIntentID = strictID
	} else if refID, serr := s.repo.FindIntentIDByClientPayoutRef(ctx, event.TenantID, canonicalInput.ClientPayoutRef); serr == nil && refID != "" {
		dupRisk = true
		dupReason = "CLIENT_PAYOUT_REF_REUSED"
		comparedIntentID = refID
	}

	// FIX: Deterministic Request Fingerprint (Replacing KAFKA_TOKENIZED)
	reqFingerprint := s.computeRequestFingerprint(
		canonicalInput.Beneficiary.Name,
		amount,
		canonicalInput.AccountNumber,
		canonicalInput.Beneficiary.Instrument.VPA,
		canonicalInput.Amount.Currency,
	)

	// Score requires partial intent for signals
	tempIntent := &models.CanonicalIntent{
		TraceID:                    event.TraceID,
		IntentID:                   intentID,
		EnvelopeID:                 event.EnvelopeID,
		TenantID:                   event.TenantID,
		IdempotencyKey:             idempotencyKey,
		SalientHash:                reqFingerprint,
		PayloadHash:                canonicalInput.PayloadHash,
		RawRowHash:                 canonicalInput.RawRowHash,
		ArtifactID:                 canonicalInput.ArtifactID,
		ArtifactVersionID:          canonicalInput.ArtifactVersionID,
		IntentType:                 canonicalInput.IntentType,
		CanonicalVersion:           "v1",
		SchemaVersion:              canonicalInput.SchemaVersion,
		Amount:                     amount,
		Currency:                   canonicalInput.Amount.Currency,
		IntendedExecutionAt:        executionAt,
		Constraints:                constraintsJSON,
		BeneficiaryType:            canonicalInput.Beneficiary.Instrument.Kind,
		PIITokens:                  piiJSON,
		Beneficiary:                beneficiaryJSON,
		Status:                     "CREATED",
		CreatedAt:                  time.Now().UTC(),
		PaymentInstructionReceived: func(t time.Time) *time.Time { return &t }(time.Now().UTC()),
		CanonicalIntentCreated:     func(t time.Time) *time.Time { return &t }(time.Now().UTC()),
		ClientPayoutRef:            canonicalInput.ClientPayoutRef,
		ProviderHint:               canonicalInput.ProviderHint,
		ClientBatchRef:             batchIDStr,
		RequestFingerprint:         reqFingerprint,
		RoutingHintsJSON:           json.RawMessage(`{}`),
		GovernanceState:            "PENDING",
		BusinessState:              "NEW",
		DuplicateRiskFlag:          dupRisk,
		MappingProfileID:           nir.ProfileID,
		MappingProfileVersion:      nir.ProfileVersion,
		SourceSystem:               event.SourceSystem,
		GovernanceHash:             canonicalInput.GovernanceHash,
		BusinessIdempotencyKey:     bIdemKey,
		BeneficiaryFingerprint:     bFingerprint,
		DuplicateReasonCode:        dupReason,
		BatchID:                    event.BatchID,
		SourceRowNum:               sourceRowNum,
		SourceRowRef:               canonicalInput.SourceRowRef,
		ValidationAnomalies:        anomalies,
	}

	// Update governance with duplicate detection results
	if dupRisk {
		governance.DuplicateDetected = true
		governance.DuplicateReason = dupReason
	}

	tokenizationComplete := len(tempIntent.PIITokens) > 2
	schemaScore, mapScore, refQualityScore, mScore, pScore, dupRiskScore, iScore, scoreReasonCodes :=
		s.computeScores(tempIntent, nir, governance, tokenizationComplete)

	// score_validity_status — set based on governance gate
	scoreValidityStatus := models.ScoreValidityScoredValid
	if iScore < 0.70 || len(scoreReasonCodes) > 0 {
		scoreValidityStatus = models.ScoreValidityScoredReview
	}

	scoredAt := time.Now().UTC()
	scoreBreakdown := buildScoreBreakdown(schemaScore, mapScore, refQualityScore, mScore, pScore, dupRiskScore, iScore)
	scoreReasonCodesJSON, _ := json.Marshal(scoreReasonCodes)

	intent := models.CanonicalIntent{
		TraceID:        event.TraceID,
		IntentID:       intentID,
		EnvelopeID:     event.EnvelopeID,
		TenantID:       event.TenantID,
		IdempotencyKey: idempotencyKey,
		SalientHash:    reqFingerprint,

		IntentType:       canonicalInput.IntentType,
		CanonicalVersion: "v1",
		SchemaVersion:    canonicalInput.SchemaVersion,

		Amount:   amount,
		Currency: canonicalInput.Amount.Currency,

		IntendedExecutionAt: executionAt,
		Constraints:         constraintsJSON,

		BeneficiaryType:      canonicalInput.Beneficiary.Instrument.Kind,
		PIITokens:            piiJSON,
		Beneficiary:          beneficiaryJSON,
		TokenizationMetadata: tokenizationMetadataJSON,

		Status:                     "CREATED",
		CreatedAt:                  time.Now().UTC(),
		PaymentInstructionReceived: func(t time.Time) *time.Time { return &t }(time.Now().UTC()),
		CanonicalIntentCreated:     func(t time.Time) *time.Time { return &t }(time.Now().UTC()),

		ClientPayoutRef:       canonicalInput.ClientPayoutRef,
		ProviderHint:          canonicalInput.ProviderHint,
		ClientBatchRef:        batchIDStr,
		RequestFingerprint:    reqFingerprint,
		RoutingHintsJSON:      json.RawMessage(`{}`),
		GovernanceState:       "PENDING",
		BusinessState:         "NEW",
		DuplicateRiskFlag:     dupRisk,
		MappingProfileID:      nir.ProfileID,
		MappingProfileVersion: nir.ProfileVersion, // Flowed from async NIR
		MappingProfileHash:    nir.MappingProfileHash,
		SourceSystem:          event.SourceSystem,
		GovernanceHash:        event.Canonical.GovernanceHash,
		// Service 2 fields
		BusinessIdempotencyKey:   bIdemKey,
		BusinessIdempotencyKeyV1: legacyBusinessKey(bKeys),
		BeneficiaryFingerprint:   bFingerprint,
		ConfidenceScore:          nil, // REMOVED
		ProofReadinessScore:      pScore,
		MatchabilityScore:        mScore,
		IntentQualityScore:       iScore,
		MappingConfidenceScore:   mapScore,
		SchemaCompletenessScore:  schemaScore,
		DuplicateReasonCode:      dupReason,

		// NEW fields:
		ReferenceQualityScore: refQualityScore,
		DuplicateRiskScore:    dupRiskScore,
		ScoreVersion:          models.ScoreVersion,
		ScoreValidityStatus:   scoreValidityStatus,
		ScoreBreakdownJSON:    scoreBreakdown,
		ScoreReasonCodesJSON:  scoreReasonCodesJSON,
		ScoredAt:              &scoredAt,

		UpdatedAt:           func(t time.Time) *time.Time { return &t }(time.Now().UTC()),
		BatchID:             event.BatchID,
		SourceRowNum:        sourceRowNum,
		SourceRowRef:        canonicalInput.SourceRowRef,
		ValidationAnomalies: anomalies,
	}
	intent.CanonicalRowHash = s.computeCanonicalRowHash(&intent)
	intent.TokenizedDataHash = s.computeTokenizedDataHash(intent.TenantID, tokenMap)
	intent.RawRowEvidenceLeafHash, intent.CanonicalRowEvidenceLeafHash = s.computeEvidenceLeafHashes(&intent)

	// -------- AGGREGATE GOVERNANCE REASONS --------
	intent.Governance = governance
	intent.GovernanceReasonCodesJSON = s.aggregateGovernanceReasons(&intent, nir)

	// UPDATED: Determine GovernanceState (VALID / INVALID / FLAGGED)
	intent.GovernanceState = "VALID"
	if intent.DuplicateRiskFlag || len(intent.ValidationAnomalies) > 0 {
		intent.GovernanceState = "FLAGGED"
	}
	if nir.MappingUncertainFlag || nir.RequiredFieldGapCount > 0 {
		intent.GovernanceState = "FLAGGED"
	}
	if iScore < 0.5 {
		intent.GovernanceState = "FLAGGED"
	}

	// 🆕 Status Fields
	govDec := "Pass"
	if intent.GovernanceState == "FLAGGED" || intent.GovernanceState == "REQUIRES_REVIEW" {
		govDec = "Fail"
	}
	intent.GovernanceDecision = &govDec

	// See single-item path for why only ACCEPTED/FLAGGED_FOR_REVIEW are reachable here.
	intent.IntentLifecycleState = "ACCEPTED"
	if intent.GovernanceState == "FLAGGED" || intent.GovernanceState == "REQUIRES_REVIEW" {
		intent.IntentLifecycleState = "FLAGGED_FOR_REVIEW"
	}

	reqStatus := nir.RequiredFieldGapCount == 0
	intent.RequiredFieldsStatus = &reqStatus

	tokStatus := true
	intent.TokenizationStatus = &tokStatus

	// Finalize governance_decision_hash + input_facts_hash. This async path
	// doesn't track a running tenant daily total, so daily_total_minor is 0.
	intent.GovernanceHash = s.computeGovernanceHash(&intent, canonicalInput.PurposeCode, 0)

	policyDecision := buildIntentPolicyDecision(
		intent.TenantID, intent.IntentID, intent.GovernanceState,
		append(append([]string{}, intent.Governance.SemanticErrors...), intent.Governance.PolicyFlags...),
		policyInputFacts(&intent, nil),
	)
	intent.PolicySource, intent.PolicyVersion, intent.PolicyHash = policyDecision.PolicySource, policyDecision.PolicyVersion, policyDecision.PolicyHash

	duplicateDecision := buildDuplicateDecision(
		intent.TenantID, intent.IntentID, dupReason, dupRiskScore, comparedIntentID,
		bIdemKey, idempotencyKey, canonicalInput.ClientPayoutRef,
		policyInputFacts(&intent, nil),
	)

	payload, err := json.Marshal(intent)
	if err != nil {
		return nil, err
	}

	outbox, err := CanonicalIntentToOutboxEvent(intent, payload, EventTypeCanonicalIntentCreatedV1)
	if err != nil {
		return nil, err
	}

	saved, err := s.repo.Save(ctx, nir, intent, outbox, registryEntry, policyDecision, duplicateDecision)
	if err != nil {
		return nil, err
	}
	version := 1

	prevHash, err := s.repo.GetPreviousTenantCanonicalHash(
		ctx,
		saved.TenantID,
		saved.IntentID,
	)
	if err != nil {
		return nil, err
	}

	canonicalBytes, err := json.Marshal(saved)
	if err != nil {
		return nil, err
	}

	canonicalRef, hash, err := s.s3.StoreSnapshot(
		ctx,
		"canonical",
		saved.TenantID,
		saved.IntentID,
		version,
		canonicalBytes,
		prevHash,
	)
	if err != nil {
		return nil, err
	}

	nirBytes, _ := json.Marshal(nir)
	nirRef, _, err := s.s3.StoreSnapshot(
		ctx,
		"nir",
		saved.TenantID,
		saved.IntentID,
		version,
		nirBytes,
		"",
	)
	if err != nil {
		return nil, err
	}

	govBytes := []byte(`{"state":"` + intent.GovernanceState + `"}`)
	govRef, _, err := s.s3.StoreSnapshot(
		ctx,
		"governance",
		saved.TenantID,
		saved.IntentID,
		version,
		govBytes,
		"",
	)
	if err != nil {
		return nil, err
	}

	saved.CanonicalHash = hash
	newGovernanceHash := s.recomputeGovernanceDecisionHash(&saved)

	err = s.repo.UpdateSnapshotRefs(
		ctx,
		saved.TenantID,
		saved.IntentID,
		canonicalRef,
		nirRef,
		govRef,
		hash,
		prevHash,
		newGovernanceHash,
	)

	saved.CanonicalSnapshotRef = canonicalRef
	saved.NIRSnapshotRef = nirRef
	saved.GovernanceSnapshotRef = govRef
	saved.GovernanceHash = newGovernanceHash

	if event.BatchID != nil && *event.BatchID != "" {
		batchKey := fmt.Sprintf("%s|%s", event.TenantID, *event.BatchID)
		_, err, _ := batchAggregateGroup.Do(batchKey, func() (interface{}, error) {
			return s.repo.UpdateBatchAggregateConfidence(context.Background(), event.TenantID, *event.BatchID)
		})
		if err != nil {
			log.Printf("⚠️ Failed to update batch aggregate confidence for batch=%s: %v", *event.BatchID, err)
		}
	}

	return &saved, nil
}

/* ---------------- WEBHOOK ---------------- */

func (s *IntentService) processWebhook(
	ctx context.Context,
	in *models.IncomingIntent,
) (*models.CanonicalIntent, *models.DLQEntry, error) {

	canonical := models.CanonicalIntent{
		TraceID:        in.TraceID.String(),
		IntentID:       uuid.NewString(),
		EnvelopeID:     in.EnvelopeID.String(),
		TenantID:       in.TenantID.String(),
		IdempotencyKey: in.IdempotencyKey,
		SalientHash:    in.IdempotencyKey,
		IntentType:     "WEBHOOK",
		SchemaVersion:  SchemaVersionV1,
		Amount:         decimal.Zero,
		Currency:       "XXX",
		Status:         "CREATED",
		CreatedAt:      time.Now().UTC(),

		IntendedExecutionAt:  nil,
		Constraints:          json.RawMessage("{}"),
		PIITokens:            json.RawMessage("{}"),
		Beneficiary:          json.RawMessage("{}"),
		TokenizationMetadata: json.RawMessage(`{"method":"none","reason":"webhook path does not tokenize"}`),

		ClientPayoutRef:           in.IdempotencyKey, // Fallback to idempotency key for webhooks if ref is missing
		RequestFingerprint:        in.IdempotencyKey,
		RoutingHintsJSON:          json.RawMessage(`{}`),
		GovernanceReasonCodesJSON: json.RawMessage(`{}`),
		ScoreBreakdownJSON:        json.RawMessage(`{}`),
		ScoreReasonCodesJSON:      json.RawMessage(`{}`),
		GovernanceState:           "WEBHOOK",
		BusinessState:             "NEW",
		IntentLifecycleState:      "RECEIVED", // webhook path skips mapping/validation/scoring today
		DuplicateRiskFlag:         false,
		MappingProfileID:          "WEBHOOK_PROFILE",
		MappingProfileVersion:     "WEBHOOK",
		MappingProfileHash:        s.computeGenericMappingProfileHash("WEBHOOK_PROFILE", "WEBHOOK", in.SourceSystem, "WEBHOOK", nil),
		UpdatedAt:                 func(t time.Time) *time.Time { return &t }(time.Now().UTC()),
	}
	canonical.CanonicalRowHash = s.computeCanonicalRowHash(&canonical)
	canonical.RawRowEvidenceLeafHash, canonical.CanonicalRowEvidenceLeafHash = s.computeEvidenceLeafHashes(&canonical)

	payload := []byte("{}")

	// Built via the same helper the real intent path uses (instead of a
	// hand-rolled struct literal) so this can't silently drift out of sync
	// with payment_intents' NOT-NULL/JSON-typed columns the way it previously
	// did — this literal used to omit Constraints/PIITokens/Beneficiary/
	// RoutingHintsJSON/GovernanceReasonCodesJSON entirely, which would have
	// failed the outbox INSERT on any real webhook.
	//
	// eventType is the same canonical-intent-v1 type every other producer
	// call site uses (previously a bespoke "WEBHOOK_RECEIVED" literal that
	// Relay/Outcome never recognized, so webhook-originated intents never
	// reached Service 5 as a normal canonical intent).
	outbox, err := CanonicalIntentToOutboxEvent(canonical, payload, EventTypeCanonicalIntentCreatedV1)
	if err != nil {
		return nil, nil, err
	}

	// Ledger item #19: the webhook path has no source fields to map, but it
	// must still leave a lineage record rather than silently having none —
	// this NIR explicitly documents why it's minimal instead of just omitting
	// it, so a lineage query never has to guess whether a row is missing by
	// accident or by design.
	nir := &models.NormalizedIngestRecord{
		NIRID:                  uuid.New(),
		EnvelopeID:             in.EnvelopeID,
		TenantID:               in.TenantID,
		DetectedFormat:         "WEBHOOK",
		ProfileID:              "WEBHOOK_PROFILE",
		ProfileVersion:         "WEBHOOK",
		FieldsJSON:             json.RawMessage(`{}`),
		FieldConfidenceSummary: json.RawMessage(`{}`),
		UnmappedJSON:           json.RawMessage(`{"skip_reason":"webhook path does not run mapping/validation/scoring"}`),
		CreatedAt:              time.Now().UTC(),
	}

	saved, err := s.repo.Save(ctx, nir, canonical, outbox, nil, nil, nil)
	if err != nil {
		return nil, nil, err
	}

	return &saved, nil, nil
}

func (s *IntentService) aggregateGovernanceReasons(intent *models.CanonicalIntent, nir *models.NormalizedIngestRecord) json.RawMessage {
	// UPDATED: Marshal the full Governance struct instead of manual aggregation
	res, _ := json.Marshal(intent.Governance)
	return res
}

// computeGovernanceHash sets intent.GovernanceInputFactsHash and returns
// governance_decision_hash per the hash spec: SHA-256(JCS_Canonicalize({
// tenant_id, canonical_intent_hash, input_facts_hash, decision, reason_codes,
// required_approval_level, risk_level})). policy_id/policy_version/
// policy_hash are intentionally omitted — there is no real policy engine
// backing them yet. canonical_intent_hash reads intent.CanonicalHash as
// known at call time — for the sync path that's still "" (the WORM chain
// hash isn't computed until after persistence), so this hash reflects the
// governance decision made at persist time, not a final chain-anchored value.
// beneficiaryChanged and previousPaymentCount have no upstream signal yet, so
// they're always false/0 until that tracking exists.
func (s *IntentService) computeGovernanceHash(intent *models.CanonicalIntent, purposeCode string, dailyTotalMinor int64) string {
	inputFactsHash, err := canonicalizer.ComputeGovernanceInputFactsHash(canonicalizer.GovernanceInputFactsHashInput{
		AmountMinor:            intent.Amount.Mul(decimal.NewFromInt(100)).IntPart(),
		Currency:               intent.Currency,
		BeneficiaryFingerprint: intent.BeneficiaryFingerprint,
		PaymentRail:            intent.BeneficiaryType,
		PurposeCode:            purposeCode,
		BeneficiaryChanged:     false,
		IsPossibleDuplicate:    intent.DuplicateRiskFlag,
		DailyTotalMinor:        dailyTotalMinor,
		PreviousPaymentCount:   0,
	})
	if err != nil {
		log.Printf("⚠️ Failed to compute governance input_facts_hash for intent %s: %v", intent.IntentID, err)
	}
	intent.GovernanceInputFactsHash = inputFactsHash

	reasonCodes := append(append([]string{}, intent.Governance.SemanticErrors...), intent.Governance.PolicyFlags...)

	hash, err := canonicalizer.ComputeGovernanceDecisionHash(canonicalizer.GovernanceDecisionHashInput{
		TenantID:            intent.TenantID,
		CanonicalIntentHash: intent.CanonicalHash,
		InputFactsHash:      inputFactsHash,
		Decision:            intent.GovernanceState,
		ReasonCodes:         reasonCodes,
	})
	if err != nil {
		log.Printf("⚠️ Failed to compute governance_decision_hash for intent %s: %v", intent.IntentID, err)
		return ""
	}
	return hash
}

// recomputeGovernanceDecisionHash recomputes governance_decision_hash using
// intent.CanonicalHash as canonical_intent_hash. Call this once the WORM
// chain hash is known (after S3 snapshot storage), so governance_hash ends up
// anchored to the real canonical_intent_hash instead of the "" placeholder
// computeGovernanceHash used at construction time. InputFactsHash is reused
// as-is since it doesn't depend on canonical_hash. On error, returns the
// intent's current GovernanceHash unchanged rather than blanking it out.
func (s *IntentService) recomputeGovernanceDecisionHash(intent *models.CanonicalIntent) string {
	reasonCodes := append(append([]string{}, intent.Governance.SemanticErrors...), intent.Governance.PolicyFlags...)

	hash, err := canonicalizer.ComputeGovernanceDecisionHash(canonicalizer.GovernanceDecisionHashInput{
		TenantID:            intent.TenantID,
		CanonicalIntentHash: intent.CanonicalHash,
		InputFactsHash:      intent.GovernanceInputFactsHash,
		Decision:            intent.GovernanceState,
		ReasonCodes:         reasonCodes,
	})
	if err != nil {
		log.Printf("⚠️ Failed to recompute governance_decision_hash for intent %s: %v", intent.IntentID, err)
		return intent.GovernanceHash
	}
	return hash
}

func (s *IntentService) computeGovernanceHashInternal(state, reasonsJSON, version, intentID string) string {
	// Construct input: governanceState + "|" + normalizedGovernanceJSON + "|" + policyVersion + "|" + intentID
	hashInput := state + "|" +
		reasonsJSON + "|" +
		version + "|" +
		intentID

	hashBytes := sha256.Sum256([]byte(hashInput))
	return hex.EncodeToString(hashBytes[:])
}

func canonicalPathToFieldName(path string) string {
	switch path {
	case "amount.value":
		return "amount"
	case "amount.currency":
		return "currency"
	case "beneficiary.name":
		return "beneficiary_name"
	default:
		return path
	}
}

// sourceRowNumFromRef converts the source_row_ref string (a plain integer relayed
// by zord-edge from the actual Excel/CSV file row number) into *int for storage.
// Returns nil only when the string is empty or not a valid integer.
func sourceRowNumFromRef(ref string) *int {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	idx, err := strconv.Atoi(ref)
	if err != nil {
		return nil
	}
	return &idx
}

func autoGenericProfileID(rawJSON []byte) string {
	var raw map[string]any
	if err := json.Unmarshal(rawJSON, &raw); err != nil || len(raw) == 0 {
		sum := sha256.Sum256(rawJSON)
		return "auto-generic-" + hex.EncodeToString(sum[:])[:12] + "-v1"
	}

	headers := make([]string, 0, len(raw))
	for key := range raw {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if normalized == "" || normalized == "source_row_ref" {
			continue
		}
		headers = append(headers, normalized)
	}
	if len(headers) == 0 {
		headers = append(headers, "json")
	}
	sort.Strings(headers)

	sum := sha256.Sum256([]byte(strings.Join(headers, "|")))
	return "auto-generic-" + hex.EncodeToString(sum[:])[:12] + "-v1"
}

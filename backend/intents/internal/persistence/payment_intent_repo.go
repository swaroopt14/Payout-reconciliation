package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/shopspring/decimal"

	"zord-intent-engine/internal/canonicalizer"
	"zord-intent-engine/internal/models"

	"github.com/google/uuid"
)

type PaymentIntentRepo struct {
	db *sql.DB
}

func NewPaymentIntentRepo(db *sql.DB) *PaymentIntentRepo {
	return &PaymentIntentRepo{db: db}
}

func (r *PaymentIntentRepo) Save(
	ctx context.Context,
	nir *models.NormalizedIngestRecord,
	intent models.CanonicalIntent, outbox models.OutboxEvent,
	registry *models.BusinessIdempotencyEntry,
	policyDecision *models.IntentPolicyDecision,
	duplicateDecision *models.DuplicateDecision,
) (models.CanonicalIntent, error) {

	if intent.ContractID == "" {
		intent.ContractID = uuid.NewString()
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return intent, err
	}

	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if nir != nil {
		nirQuery := `
		INSERT INTO normalized_ingest_records (
			nir_id, envelope_id, tenant_id,
			detected_format, profile_id, profile_version,
			fields_json, field_confidence_summary, unmapped_json, mapping_uncertain_flag,
			required_field_gap_count, low_confidence_field_count,
			created_at, mapping_profile_hash
		) VALUES (
			$1, $2, $3,
			$4, $5, $6,
			$7, $8, $9, $10,
			$11, $12,
			$13, $14
		)`
		_, err = tx.ExecContext(ctx, nirQuery,
			nir.NIRID, nir.EnvelopeID, nir.TenantID,
			nir.DetectedFormat, nir.ProfileID, nir.ProfileVersion,
			nir.FieldsJSON, nir.FieldConfidenceSummary, nir.UnmappedJSON, nir.MappingUncertainFlag,
			nir.RequiredFieldGapCount, nir.LowConfidenceFieldCount,
			nir.CreatedAt, nir.MappingProfileHash,
		)
		if err != nil {
			log.Printf("Repo.Save: INSERT normalized_ingest_records failed: %v", err)
			return intent, err
		}
	}

	query := `
	INSERT INTO payment_intents (
    intent_id, envelope_id, tenant_id, contract_id,
    trace_id, idempotency_key, salient_hash, payload_hash,
    intent_type, canonical_version, schema_version,
    amount, currency, intended_execution_at,
    constraints, beneficiary_type, pii_tokens, beneficiary,
    status, confidence_score,
    canonical_snapshot_ref, nir_snapshot_ref, governance_snapshot_ref, governance_hash,
    canonical_hash,
    created_at,
    client_payout_ref, provider_hint, request_fingerprint, routing_hints_json,
    governance_state, business_state, duplicate_risk_flag,
    mapping_profile_id, mapping_profile_version, source_system, updated_at,
    business_idempotency_key, beneficiary_fingerprint,
    proof_readiness_score, matchability_score, intent_quality_score,
    mapping_confidence_score,
    schema_completeness_score,
	governance_reason_codes_json,
    duplicate_reason_code, client_batch_ref,
	batchid,
	source_row_num,
    aggregate_confidence_score, -- NEW
    reference_quality_score,
    duplicate_risk_score,
    score_version,
    score_validity_status,
    score_breakdown_json,
    score_reason_codes_json,
    scored_at,
    required_fields_status,
    tokenization_status,
    tokenization_metadata,
    governance_decision,
    payment_instruction_received,
    canonical_intent_created,
    intent_lifecycle_state,
    mapping_profile_hash,
    policy_source, policy_version, policy_hash,
    source_row_ref, canonical_row_hash,
    input_facts_hash, tokenized_data_hash,
    raw_row_evidence_leaf_hash, canonical_row_evidence_leaf_hash,
    raw_row_hash,
    artifact_id, artifact_version_id
)
VALUES (
    $1,$2,$3,$4,
    $5,$6,$7,$8,
    $9,$10,$11,
    $12,$13,$14,
    $15,$16,$17,$18,
    $19,$20,
    $21,$22,$23,
    $24,$25,
    $26, $27,
    $28,$29,$30,
    $31,$32,$33,
    $34,$35,$36,$37,
    $38,$39,
    $40,$41,$42,
    $43,
    $44, $45,
    $46, $47, $48, $49, $50, -- UPDATED
    $51, $52, $53, $54, $55, $56, $57,
    $58, $59, $60, $61, $62, $63,
    $64, $65,
    $66, $67, $68,
    $69, $70,
    $71, $72,
    $73, $74,
    $75,
    $76, $77
) `

	_, err = tx.ExecContext(
		ctx,
		query,
		intent.IntentID,                     // $1
		intent.EnvelopeID,                   // $2
		intent.TenantID,                     // $3
		intent.ContractID,                   // $4
		intent.TraceID,                      // $5
		intent.IdempotencyKey,               // $6
		intent.SalientHash,                  // $7
		intent.PayloadHash,                  // $8
		intent.IntentType,                   // $9
		intent.CanonicalVersion,             // $10
		intent.SchemaVersion,                // $11
		intent.Amount,                       // $12
		intent.Currency,                     // $13
		intent.IntendedExecutionAt,          // $14
		intent.Constraints,                  // $15
		intent.BeneficiaryType,              // $16
		intent.PIITokens,                    // $17
		intent.Beneficiary,                  // $18
		intent.Status,                       // $19
		intent.ConfidenceScore,              // $20
		intent.CanonicalSnapshotRef,         // $21
		intent.NIRSnapshotRef,               // $22
		intent.GovernanceSnapshotRef,        // $23
		intent.GovernanceHash,               // $24  ← matches column: governance_hash
		intent.CanonicalHash,                // $25  ← matches column: canonical_hash
		intent.CreatedAt,                    // $26  ← matches column: created_at
		intent.ClientPayoutRef,              // $27  ← matches column: client_payout_ref
		intent.ProviderHint,                 // $28
		intent.RequestFingerprint,           // $29
		intent.RoutingHintsJSON,             // $30
		intent.GovernanceState,              // $31
		intent.BusinessState,                // $32
		intent.DuplicateRiskFlag,            // $33
		intent.MappingProfileID,             // $34
		intent.MappingProfileVersion,        // $35
		intent.SourceSystem,                 // $36
		intent.UpdatedAt,                    // $37
		intent.BusinessIdempotencyKey,       // $38
		intent.BeneficiaryFingerprint,       // $39
		intent.ProofReadinessScore,          // $40
		intent.MatchabilityScore,            // $41
		intent.IntentQualityScore,           // $42
		intent.MappingConfidenceScore,       // $43
		intent.SchemaCompletenessScore,      // $44
		intent.GovernanceReasonCodesJSON,    // $45
		intent.DuplicateReasonCode,          // $46
		intent.ClientBatchRef,               // $47
		intent.BatchID,                      // $48
		intent.SourceRowNum,                 // $49
		intent.AggregateConfidenceScore,     // $50 -- NEW
		intent.ReferenceQualityScore,        // $51
		intent.DuplicateRiskScore,           // $52
		intent.ScoreVersion,                 // $53
		intent.ScoreValidityStatus,          // $54
		intent.ScoreBreakdownJSON,           // $55
		intent.ScoreReasonCodesJSON,         // $56
		intent.ScoredAt,                     // $57
		intent.RequiredFieldsStatus,         // $58
		intent.TokenizationStatus,           // $59
		intent.TokenizationMetadata,         // $60
		intent.GovernanceDecision,           // $61
		intent.PaymentInstructionReceived,   // $62
		intent.CanonicalIntentCreated,       // $63
		intent.IntentLifecycleState,         // $64
		intent.MappingProfileHash,           // $65
		intent.PolicySource,                 // $66
		intent.PolicyVersion,                // $67
		intent.PolicyHash,                   // $68
		intent.SourceRowRef,                 // $69
		intent.CanonicalRowHash,             // $70
		intent.GovernanceInputFactsHash,     // $71
		intent.TokenizedDataHash,            // $72
		intent.RawRowEvidenceLeafHash,       // $73
		intent.CanonicalRowEvidenceLeafHash, // $74
		intent.RawRowHash,                   // $75
		intent.ArtifactID,                   // $76
		intent.ArtifactVersionID,            // $77
	)

	if err != nil {
		log.Printf("Repo.Save: INSERT payment_intents failed: %v", err)
		return intent, err
	}

	// INT-09: query text and args are both derived from OutboxInsertColumns
	// (outbox_insert_contract.go) — a single source of truth shared with
	// SaveBatch below, instead of two independently hand-typed copies.
	outboxQuery := buildOutboxInsertQuerySingle()

	outbox.ContractID = intent.ContractID

	_, err = tx.ExecContext(ctx, outboxQuery, OutboxInsertArgs(outbox)...)
	if err != nil {
		log.Printf("Repo.Save: INSERT outbox failed: %v", err)
		return intent, err
	}

	if registry != nil {
		registryQuery := `
    INSERT INTO business_idempotency_registry (
        tenant_id, business_idempotency_key, intent_id,
        beneficiary_fingerprint, amount_minor, currency_code,
        time_bucket, duplicate_reason_code, created_at
    ) VALUES (
        $1, $2, $3, $4, $5, $6, $7, $8, $9
    ) ON CONFLICT (tenant_id, business_idempotency_key) DO NOTHING`
		result, err := tx.ExecContext(ctx, registryQuery,
			registry.TenantID, registry.BusinessIdempotencyKey, registry.IntentID,
			registry.BeneficiaryFingerprint, registry.AmountMinor, registry.CurrencyCode,
			registry.TimeBucket, registry.DuplicateReasonCode, registry.CreatedAt,
		)
		if err != nil {
			log.Printf("Repo.Save: INSERT business_idempotency_registry failed: %v", err)
			return intent, err
		}
		// Check if the INSERT was suppressed by ON CONFLICT (rows affected = 0 means a concurrent
		// intent already owns this key — signal the service layer to mark this intent as duplicate)
		rowsAffected, _ := result.RowsAffected()
		if rowsAffected == 0 {
			intent.DuplicateRiskFlag = true
			if intent.DuplicateReasonCode == "" || intent.DuplicateReasonCode == "NONE" {
				intent.DuplicateReasonCode = "SAME_BENEFICIARY_AMOUNT_TIME"
			}
			intent.GovernanceState = "FLAGGED"
			// Update the already-inserted payment_intents row to reflect duplicate flag
			_, err = tx.ExecContext(ctx, `
            UPDATE payment_intents
            SET duplicate_risk_flag = true,
                duplicate_reason_code = $1,
                governance_state = 'FLAGGED',
                updated_at = now()
            WHERE tenant_id = $2 AND intent_id = $3`,
				intent.DuplicateReasonCode,
				intent.TenantID,
				intent.IntentID,
			)
			if err != nil {
				log.Printf("Repo.Save: UPDATE duplicate flag failed: %v", err)
				return intent, err
			}
		}
	}

	if policyDecision != nil {
		policyQuery := `
		INSERT INTO intent_policy_decisions (
			tenant_id, intent_id, policy_source, policy_version, policy_hash,
			policy_result, reason_codes_json, input_facts_hash, input_facts_json, evaluated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (tenant_id, intent_id, policy_source, policy_version) DO NOTHING`
		_, err = tx.ExecContext(ctx, policyQuery,
			policyDecision.TenantID, policyDecision.IntentID, policyDecision.PolicySource,
			policyDecision.PolicyVersion, policyDecision.PolicyHash, policyDecision.PolicyResult,
			policyDecision.ReasonCodesJSON, policyDecision.InputFactsHash, policyDecision.InputFactsJSON,
			policyDecision.EvaluatedAt,
		)
		if err != nil {
			log.Printf("Repo.Save: INSERT intent_policy_decisions failed: %v", err)
			return intent, err
		}
	}

	if duplicateDecision != nil {
		dupQuery := `
		INSERT INTO duplicate_decisions (
			tenant_id, intent_id, decision, reason_code, duplicate_score,
			compared_intent_id, duplicate_group_id, comparison_facts_hash, policy_version, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`
		_, err = tx.ExecContext(ctx, dupQuery,
			duplicateDecision.TenantID, duplicateDecision.IntentID, duplicateDecision.Decision,
			duplicateDecision.ReasonCode, duplicateDecision.DuplicateScore, duplicateDecision.ComparedIntentID,
			duplicateDecision.DuplicateGroupID, duplicateDecision.ComparisonFactsHash, duplicateDecision.PolicyVersion,
			duplicateDecision.CreatedAt,
		)
		if err != nil {
			log.Printf("Repo.Save: INSERT duplicate_decisions failed: %v", err)
			return intent, err
		}
	}

	err = tx.Commit()
	if err != nil {
		return intent, err
	}

	return intent, nil
}

func (r *PaymentIntentRepo) FindByEnvelope(
	ctx context.Context,
	tenantID string,
	envelopeID string,
) (*models.CanonicalIntent, error) {

	query := `
	SELECT
		intent_id,
		envelope_id,
		tenant_id,
		contract_id,
		intent_type,
		canonical_version,
		schema_version,
		amount,
		currency,
		intended_execution_at,
		constraints,
		beneficiary_type,
		pii_tokens,
		beneficiary,
		status,
		confidence_score,
		created_at,
		client_payout_ref,
		provider_hint,
		request_fingerprint,
		routing_hints_json,
		governance_state,
		business_state,
		duplicate_risk_flag,
		mapping_profile_id,
		mapping_profile_version,
		source_system,
		updated_at,
		business_idempotency_key,
		beneficiary_fingerprint,
		proof_readiness_score,
		matchability_score,
		intent_quality_score,
		mapping_confidence_score,
		schema_completeness_score,
		governance_reason_codes_json,
		duplicate_reason_code,
		client_batch_ref,
		canonical_snapshot_ref,
		COALESCE(nir_snapshot_ref, '') as nir_snapshot_ref,
		COALESCE(governance_snapshot_ref, '') as governance_snapshot_ref,
		COALESCE(governance_hash, '') as governance_hash,
		batchid,
		source_row_num,
		aggregate_confidence_score, -- NEW
		required_fields_status,
		tokenization_status,
		governance_decision,
		payment_instruction_received,
		canonical_intent_created,
		intent_lifecycle_state,
		COALESCE(mapping_profile_hash, '') as mapping_profile_hash,
		COALESCE(policy_source, '') as policy_source,
		COALESCE(policy_version, '') as policy_version,
		COALESCE(policy_hash, '') as policy_hash
	FROM payment_intents
	WHERE tenant_id = $1
	  AND envelope_id = $2
	LIMIT 1
	`

	var intent models.CanonicalIntent

	err := r.db.QueryRowContext(
		ctx,
		query,
		tenantID,
		envelopeID,
	).Scan(
		&intent.IntentID,
		&intent.EnvelopeID,
		&intent.TenantID,
		&intent.ContractID,
		&intent.IntentType,
		&intent.CanonicalVersion,
		&intent.SchemaVersion,
		&intent.Amount,
		&intent.Currency,
		&intent.IntendedExecutionAt,
		&intent.Constraints,
		&intent.BeneficiaryType,
		&intent.PIITokens,
		&intent.Beneficiary,
		&intent.Status,
		&intent.ConfidenceScore,
		&intent.CreatedAt,
		&intent.ClientPayoutRef,
		&intent.ProviderHint,
		&intent.RequestFingerprint,
		&intent.RoutingHintsJSON,
		&intent.GovernanceState,
		&intent.BusinessState,
		&intent.DuplicateRiskFlag,
		&intent.MappingProfileID,
		&intent.MappingProfileVersion,
		&intent.SourceSystem,
		&intent.UpdatedAt,
		&intent.BusinessIdempotencyKey,
		&intent.BeneficiaryFingerprint,
		&intent.ProofReadinessScore,
		&intent.MatchabilityScore,
		&intent.IntentQualityScore,
		&intent.MappingConfidenceScore,
		&intent.SchemaCompletenessScore,
		&intent.GovernanceReasonCodesJSON,
		&intent.DuplicateReasonCode,
		&intent.ClientBatchRef,
		&intent.CanonicalSnapshotRef,
		&intent.NIRSnapshotRef,
		&intent.GovernanceSnapshotRef,
		&intent.GovernanceHash,
		&intent.BatchID,
		&intent.SourceRowNum,
		&intent.AggregateConfidenceScore, // NEW
		&intent.RequiredFieldsStatus,
		&intent.TokenizationStatus,
		&intent.GovernanceDecision,
		&intent.PaymentInstructionReceived,
		&intent.CanonicalIntentCreated,
		&intent.IntentLifecycleState,
		&intent.MappingProfileHash,
		&intent.PolicySource,
		&intent.PolicyVersion,
		&intent.PolicyHash,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return &intent, nil
}

func (r *PaymentIntentRepo) UpdateSnapshotRefs(
	ctx context.Context,
	tenantID string,
	intentID string,
	canonicalRef string,
	nirRef string,
	govRef string,
	hash string,
	prevHash string,
	governanceHash string,
) error {
	query := `
	UPDATE payment_intents
	SET canonical_snapshot_ref = $1,
	    nir_snapshot_ref = $2,
	    governance_snapshot_ref = $3,
	    canonical_hash = $4,
	    governance_hash = $7,
	    updated_at = now()
	WHERE tenant_id = $5 AND intent_id = $6
	`

	if _, err := r.db.ExecContext(ctx, query, canonicalRef, nirRef, govRef, hash, tenantID, intentID, governanceHash); err != nil {
		return err
	}

	outboxQuery := `
	UPDATE outbox
	SET canonical_snapshot_ref = $1,
	    nir_snapshot_ref = $2,
	    governance_snapshot_ref = $3,
	    canonical_hash = $4,
	    governance_hash = $7
	WHERE tenant_id = $5 AND aggregate_id = $6
	`

	if _, err := r.db.ExecContext(ctx, outboxQuery, canonicalRef, nirRef, govRef, hash, tenantID, intentID, governanceHash); err != nil {
		return err
	}

	insertVersionQuery := `
	INSERT INTO intent_versions (intent_id, version_no, prev_hash, created_at)
	VALUES ($1, $2, $3, now())
	ON CONFLICT (intent_id, version_no) DO NOTHING
	`

	_, err := r.db.ExecContext(ctx, insertVersionQuery, intentID, 1, prevHash)

	return err
}

// GetHeldIntentForApproval returns the amount/currency/governance_state of
// one intent, scoped to tenant — R-05's approval re-check needs the amount
// and currency to re-run ReserveIfWithinLimit, and the governance_state to
// refuse approving something that was never actually held.
func (r *PaymentIntentRepo) GetHeldIntentForApproval(ctx context.Context, tenantID, intentID string) (amount decimal.Decimal, currency string, governanceState string, err error) {
	var amountStr string
	err = r.db.QueryRowContext(ctx, `
		SELECT amount::text, currency, governance_state
		FROM payment_intents
		WHERE tenant_id = $1 AND intent_id = $2
	`, tenantID, intentID).Scan(&amountStr, &currency, &governanceState)
	if err != nil {
		return decimal.Zero, "", "", err
	}
	amount, err = decimal.NewFromString(amountStr)
	return amount, currency, governanceState, err
}

// ApproveHeldIntent flips a REQUIRES_REVIEW intent to VALID/ACCEPTED after
// R-05's atomic re-check confirms it now fits within the tenant's daily
// limit. Updates payment_intents and outbox together so a not-yet-leased
// outbox row reflects the approval too. An outbox row that zord-relay has
// already leased/sent as FLAGGED_FOR_REVIEW is not retroactively redelivered
// — that would need a real evidence/re-emission story, out of scope here.
// approvedBy is the verified PAYOUT_APPROVER user id (Slice 8, D39); it and
// approved_at are stamped on the same UPDATE that flips the intent.
func (r *PaymentIntentRepo) ApproveHeldIntent(ctx context.Context, tenantID, intentID, approvedBy string) error {
	if strings.TrimSpace(approvedBy) == "" {
		return fmt.Errorf("approve held intent: approved_by is required")
	}
	if _, err := r.db.ExecContext(ctx, `
		UPDATE payment_intents
		SET governance_state = 'VALID', governance_decision = 'Pass',
		    intent_lifecycle_state = 'ACCEPTED',
		    approved_by = $3, approved_at = now(), updated_at = now()
		WHERE tenant_id = $1 AND intent_id = $2
	`, tenantID, intentID, approvedBy); err != nil {
		return fmt.Errorf("approve held intent: update payment_intents: %w", err)
	}

	if _, err := r.db.ExecContext(ctx, `
		UPDATE outbox
		SET governance_state = 'VALID', governance_decision = 'Pass',
		    intent_lifecycle_state = 'ACCEPTED'
		WHERE tenant_id = $1 AND aggregate_id = $2
	`, tenantID, intentID); err != nil {
		return fmt.Errorf("approve held intent: update outbox: %w", err)
	}
	return nil
}

func (r *PaymentIntentRepo) GetPreviousTenantCanonicalHash(
	ctx context.Context,
	tenantID string,
	intentID string,
) (string, error) {
	var prevHash string

	err := r.db.QueryRowContext(ctx, `
		SELECT canonical_hash
		FROM payment_intents
		WHERE tenant_id = $1
		  AND intent_id <> $2
		  AND canonical_hash IS NOT NULL
		  AND canonical_hash <> ''
		ORDER BY created_at DESC
		LIMIT 1
	`, tenantID, intentID).Scan(&prevHash)

	if err == sql.ErrNoRows {
		return "GENESIS", nil
	}
	if err != nil {
		return "", err
	}

	return prevHash, nil
}

func (r *PaymentIntentRepo) FindByBusinessIdempotencyKey(
	ctx context.Context,
	tenantID string,
	key string,
) (*models.CanonicalIntent, error) {

	query := `
	SELECT
		intent_id,
		envelope_id,
		tenant_id,
		contract_id,
		intent_type,
		canonical_version,
		schema_version,
		amount,
		currency,
		intended_execution_at,
		constraints,
		beneficiary_type,
		pii_tokens,
		beneficiary,
		status,
		confidence_score,
		created_at,
		client_payout_ref,
		provider_hint,
		request_fingerprint,
		routing_hints_json,
		governance_state,
		business_state,
		duplicate_risk_flag,
		mapping_profile_id,
		mapping_profile_version,
		source_system,
		updated_at,
		business_idempotency_key,
		beneficiary_fingerprint,
		proof_readiness_score,
		matchability_score,
		intent_quality_score,
		mapping_confidence_score,
		schema_completeness_score,
		governance_reason_codes_json,
		duplicate_reason_code,
		client_batch_ref,
		canonical_snapshot_ref,
		COALESCE(nir_snapshot_ref, '') as nir_snapshot_ref,
		COALESCE(governance_snapshot_ref, '') as governance_snapshot_ref,
		COALESCE(governance_hash, '') as governance_hash,
		batchid,
		source_row_num,
		aggregate_confidence_score, -- NEW
		required_fields_status,
		tokenization_status,
		governance_decision,
		payment_instruction_received,
		canonical_intent_created,
		intent_lifecycle_state,
		COALESCE(mapping_profile_hash, '') as mapping_profile_hash,
		COALESCE(policy_source, '') as policy_source,
		COALESCE(policy_version, '') as policy_version,
		COALESCE(policy_hash, '') as policy_hash
	FROM payment_intents
	WHERE tenant_id = $1
	  AND business_idempotency_key = $2
	LIMIT 1
	`

	var intent models.CanonicalIntent

	err := r.db.QueryRowContext(
		ctx,
		query,
		tenantID,
		key,
	).Scan(
		&intent.IntentID,
		&intent.EnvelopeID,
		&intent.TenantID,
		&intent.ContractID,
		&intent.IntentType,
		&intent.CanonicalVersion,
		&intent.SchemaVersion,
		&intent.Amount,
		&intent.Currency,
		&intent.IntendedExecutionAt,
		&intent.Constraints,
		&intent.BeneficiaryType,
		&intent.PIITokens,
		&intent.Beneficiary,
		&intent.Status,
		&intent.ConfidenceScore,
		&intent.CreatedAt,
		&intent.ClientPayoutRef,
		&intent.ProviderHint,
		&intent.RequestFingerprint,
		&intent.RoutingHintsJSON,
		&intent.GovernanceState,
		&intent.BusinessState,
		&intent.DuplicateRiskFlag,
		&intent.MappingProfileID,
		&intent.MappingProfileVersion,
		&intent.SourceSystem,
		&intent.UpdatedAt,
		&intent.BusinessIdempotencyKey,
		&intent.BeneficiaryFingerprint,
		&intent.ProofReadinessScore,
		&intent.MatchabilityScore,
		&intent.IntentQualityScore,
		&intent.MappingConfidenceScore,
		&intent.SchemaCompletenessScore,
		&intent.GovernanceReasonCodesJSON,
		&intent.DuplicateReasonCode,
		&intent.ClientBatchRef,
		&intent.CanonicalSnapshotRef,
		&intent.NIRSnapshotRef,
		&intent.GovernanceSnapshotRef,
		&intent.GovernanceHash,
		&intent.BatchID,
		&intent.SourceRowNum,
		&intent.AggregateConfidenceScore, // NEW
		&intent.RequiredFieldsStatus,
		&intent.TokenizationStatus,
		&intent.GovernanceDecision,
		&intent.PaymentInstructionReceived,
		&intent.CanonicalIntentCreated,
		&intent.IntentLifecycleState,
		&intent.MappingProfileHash,
		&intent.PolicySource,
		&intent.PolicyVersion,
		&intent.PolicyHash,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return &intent, nil
}

func (r *PaymentIntentRepo) CheckIdempotencyRegistry(
	ctx context.Context,
	tenantID string,
	key string,
) (*models.BusinessIdempotencyEntry, error) {

	query := `
	SELECT
		tenant_id,
		business_idempotency_key,
		intent_id,
		beneficiary_fingerprint,
		amount_minor,
		currency_code,
		time_bucket,
		duplicate_reason_code,
		created_at
	FROM business_idempotency_registry
	WHERE tenant_id = $1
	  AND business_idempotency_key = $2
	LIMIT 1
	`

	var entry models.BusinessIdempotencyEntry

	err := r.db.QueryRowContext(
		ctx,
		query,
		tenantID,
		key,
	).Scan(
		&entry.TenantID,
		&entry.BusinessIdempotencyKey,
		&entry.IntentID,
		&entry.BeneficiaryFingerprint,
		&entry.AmountMinor,
		&entry.CurrencyCode,
		&entry.TimeBucket,
		&entry.DuplicateReasonCode,
		&entry.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return &entry, nil
}

// FindIntentIDByIdempotencyKey returns the oldest intent_id already using this
// tenant + idempotency_key, or "" if none exists. Backs strict duplicate
// detection (ledger item #11) — a reused idempotency_key is the strongest
// duplicate signal available, since a client is only supposed to reuse it
// when retrying the exact same logical operation.
func (r *PaymentIntentRepo) FindIntentIDByIdempotencyKey(ctx context.Context, tenantID, idempotencyKey string) (string, error) {
	if idempotencyKey == "" {
		return "", nil
	}
	var intentID string
	err := r.db.QueryRowContext(ctx, `
		SELECT intent_id FROM payment_intents
		WHERE tenant_id = $1 AND idempotency_key = $2
		ORDER BY created_at ASC LIMIT 1
	`, tenantID, idempotencyKey).Scan(&intentID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return intentID, nil
}

// FindIntentIDByClientPayoutRef returns the oldest intent_id already using
// this tenant + client_payout_ref, or "" if none exists. Backs strict
// duplicate detection (ledger item #11).
func (r *PaymentIntentRepo) FindIntentIDByClientPayoutRef(ctx context.Context, tenantID, clientPayoutRef string) (string, error) {
	if clientPayoutRef == "" {
		return "", nil
	}
	var intentID string
	err := r.db.QueryRowContext(ctx, `
		SELECT intent_id FROM payment_intents
		WHERE tenant_id = $1 AND client_payout_ref = $2
		ORDER BY created_at ASC LIMIT 1
	`, tenantID, clientPayoutRef).Scan(&intentID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return intentID, nil
}

// UpdateBatchAggregateConfidence computes the batch_quality_score using the FULL received
// population as denominator — including DLQ rows. This fixes the problem where
// 487 DLQ rows produced a healthy 0.77 batch score.
//
// Formula (doc section 7):
//
//	batch_quality_score =
//	  0.20 * canonicalization_success_rate
//	+ 0.20 * avg_intent_quality_score (normalized 0–100)
//	+ 0.20 * avg_matchability_score
//	+ 0.15 * avg_proof_readiness_score
//	+ 0.10 * (1 - duplicate_risk_rate)
//	+ 0.10 * (1 - low_matchability_rate)
//	+ 0.05 * (1 - review_rate)
//
// DLQ cap rules:
//
//	dlq_rate > 0.05  → cap at 75
//	dlq_rate > 0.10  → cap at 60
//	dlq_rate > 0.20  → cap at 40
func (r *PaymentIntentRepo) UpdateBatchAggregateConfidence(ctx context.Context, tenantID, batchID string) (float64, error) {
	if batchID == "" {
		return 0, nil
	}

	// Step 1: Gather batch counts from payment_intents (canonicalized rows)
	var canonicalized int
	var avgQuality, avgMatchability, avgProof, avgDupRisk, avgSchema, avgMapping sql.NullFloat64
	var totalAmount decimal.Decimal
	var lowMatchCount, lowProofCount, dupRiskCount int
	var dupRiskAmount int64
	var retrievedTenantID sql.NullString
	var sourceSystem sql.NullString
	var batchCurrency sql.NullString
	var batchMappingProfileHash sql.NullString

	err := r.db.QueryRowContext(ctx, `
        SELECT
            COUNT(*),
            AVG(intent_quality_score),
            AVG(matchability_score),
            AVG(proof_readiness_score),
            AVG(duplicate_risk_score),
            AVG(schema_completeness_score),
            AVG(mapping_confidence_score),
            SUM(CASE WHEN matchability_score < 0.40 THEN 1 ELSE 0 END),
            SUM(CASE WHEN proof_readiness_score < 0.40 THEN 1 ELSE 0 END),
            SUM(CASE WHEN duplicate_risk_flag = true THEN 1 ELSE 0 END),
            COALESCE(SUM(CASE WHEN duplicate_risk_score >= 0.31 THEN (amount * 100)::BIGINT ELSE 0 END), 0),
            MAX(tenant_id::TEXT),
            MAX(source_system),
            COALESCE(SUM(amount), 0),
            MAX(currency),
            MAX(mapping_profile_hash)
        FROM payment_intents
        WHERE tenant_id = $1 AND
		batchid=$2
    `, tenantID, batchID).Scan(
		&canonicalized,
		&avgQuality, &avgMatchability, &avgProof, &avgDupRisk, &avgSchema, &avgMapping,
		&lowMatchCount, &lowProofCount, &dupRiskCount, &dupRiskAmount,
		&retrievedTenantID, &sourceSystem, &totalAmount, &batchCurrency, &batchMappingProfileHash,
	)
	if err != nil {
		return 0, err
	}

	// Step 2: Get DLQ count for this batch from dlq_items
	var dlqCount int
	_ = r.db.QueryRowContext(ctx, `
        SELECT COUNT(*) FROM dlq_items WHERE tenant_id = $1 AND batch_id = $2
    `, tenantID, batchID).Scan(&dlqCount)

	// Fallback if tenantID/sourceSystem not in payment_intents (all DLQ'd)
	if !retrievedTenantID.Valid || retrievedTenantID.String == "" {
		_ = r.db.QueryRowContext(ctx, `
            SELECT MAX(tenant_id::TEXT) FROM dlq_items WHERE tenant_id = $1 AND batch_id = $2
        `, tenantID, batchID).Scan(&retrievedTenantID)
	}

	// Step 3: Get review count (FLAGGED governance state)
	var reviewCount int
	_ = r.db.QueryRowContext(ctx, `
        SELECT COUNT(*) FROM payment_intents
        WHERE tenant_id = $1 AND batchid = $2 AND governance_state IN ('FLAGGED','REQUIRES_REVIEW')
    `, tenantID, batchID).Scan(&reviewCount)

	// Step 4: Full denominator and invariant drift check
	// Look up the batch state in the ingest system to find actual rows sent (source truth).
	var totalRowsSource, acceptedRowsSource, failedRowsSource int
	var sourceStatus string
	err = r.db.QueryRowContext(ctx, `
		SELECT COALESCE(total_rows, 0), COALESCE(accepted_rows, 0), COALESCE(failed_rows, 0), COALESCE(status, '')
		FROM intent_ingest_runs
		WHERE tenant_id = $1::uuid AND batch_id = $2
	`, tenantID, batchID).Scan(&totalRowsSource, &acceptedRowsSource, &failedRowsSource, &sourceStatus)
	if err != nil && err != sql.ErrNoRows {
		log.Printf("⚠️ Ingest run lookup failed for tenant=%s batch=%s: %v", tenantID, batchID, err)
	}

	var pendingCount int
	if err == nil && totalRowsSource > 0 {
		// Pending is the remainder of rows not yet marked as final in either table.
		pendingCount = totalRowsSource - canonicalized - dlqCount
		if pendingCount < 0 {
			pendingCount = 0
		}
	}

	received := canonicalized + dlqCount + pendingCount
	if received == 0 {
		return 0, nil
	}

	// Validate invariant: received = canonicalized + dlq + pending.
	// If a drift is detected or source metadata doesn't exist, we fallback safely.
	if totalRowsSource > 0 && received != totalRowsSource {
		log.Printf("⚠️ BATCH_AGGREGATE_DRIFT: Batch %s has mismatch. Ingest source claims %d rows, but calculated %d (canonicalized: %d, DLQ: %d, pending: %d). Reconciling to source truth.",
			batchID, totalRowsSource, received, canonicalized, dlqCount, pendingCount)
		received = totalRowsSource
	}

	canonRate := float64(canonicalized) / float64(received)
	dlqRate := float64(dlqCount) / float64(received)
	reviewRate := float64(reviewCount) / float64(received)
	dupRiskRate := float64(dupRiskCount) / float64(received)
	lowMatchRate := float64(lowMatchCount) / float64(received)

	// intent_quality_score / matchability_score / proof_readiness_score are
	// persisted 0–1 (see IntentService.computeScores: the final schema/
	// mapping/refQuality/matchability/proof/dupRisk/quality values are all
	// divided by 100 before being assigned to the CanonicalIntent — compare
	// the `iScore < 0.70` / `iScore < 0.5` checks against the very same
	// IntentQualityScore field elsewhere in intent_service.go). AVG() over an
	// already-0–1 column is already 0–1, so no rescale needed here.
	avgQ := safeFloat(avgQuality)
	avgM := safeFloat(avgMatchability)
	avgP := safeFloat(avgProof)

	batchScore := (canonRate*0.20 +
		avgQ*0.20 +
		avgM*0.20 +
		avgP*0.15 +
		(1.0-dupRiskRate)*0.10 +
		(1.0-lowMatchRate)*0.10 +
		(1.0-reviewRate)*0.05)

	// DLQ caps — the critical fix for the 487-DLQ problem
	switch {
	case dlqRate > 0.20:
		if batchScore > 0.40 {
			batchScore = 0.40
		}
	case dlqRate > 0.10:
		if batchScore > 0.60 {
			batchScore = 0.60
		}
	case dlqRate > 0.05:
		if batchScore > 0.75 {
			batchScore = 0.75
		}
	}

	if batchScore < 0 {
		batchScore = 0
	}
	if batchScore > 1.0 {
		batchScore = 1.0
	}

	// Step 5: Update payment_intents with batch quality score + counters
	_, err = r.db.ExecContext(ctx, `
        UPDATE payment_intents
        SET aggregate_confidence_score = $1,
            updated_at = now()
        WHERE tenant_id = $2 AND batchid = $3
    `, batchScore, tenantID, batchID) // stored as 0–1
	if err != nil {
		return 0, err
	}

	// Step 6: Update outbox payload with all batch quality fields
	batchBreakdown := map[string]any{
		"batch_quality_score":         batchScore,
		"received_count":              received,
		"canonicalized_count":         canonicalized,
		"dlq_count":                   dlqCount,
		"pending_count":               pendingCount,
		"review_count":                reviewCount,
		"canonicalization_rate":       canonRate,
		"dlq_rate":                    dlqRate,
		"duplicate_risk_count":        dupRiskCount,
		"low_matchability_count":      lowMatchCount,
		"low_proof_readiness_count":   lowProofCount,
		"avg_intent_quality_score":    safeFloat(avgQuality),
		"avg_matchability_score":      safeFloat(avgMatchability),
		"avg_proof_readiness_score":   safeFloat(avgProof),
		"avg_schema_completeness":     safeFloat(avgSchema),
		"avg_mapping_confidence":      safeFloat(avgMapping),
		"duplicate_risk_amount_minor": dupRiskAmount,
		"score_version":               "service2_score_v2.0",
	}
	breakdownJSON, _ := json.Marshal(batchBreakdown)

	_, err = r.db.ExecContext(ctx, `
        WITH locked AS (
            SELECT event_id
            FROM outbox
            WHERE tenant_id = $3 AND batchid = $4
            ORDER BY event_id
            FOR UPDATE
        )
        UPDATE outbox o
        SET aggregate_confidence_score = $1,
            payload = jsonb_set(
                jsonb_set(o.payload, '{aggregate_confidence_score}', to_jsonb($1::numeric)),
                '{batch_quality_breakdown}', $2::jsonb
            )
        FROM locked l
        WHERE o.event_id = l.event_id
    `, batchScore, breakdownJSON, tenantID, batchID)
	if err != nil {
		return 0, err
	}

	// Step 6.5: Compute file_manifest_hash over this batch's rows.
	// manifest_schema_version/artifact_id/artifact_version_id/payload_type are
	// populated by upstream services, not by this code — whatever value is
	// already on the row (or empty, if none yet) is read as-is and hashed;
	// this call never writes those four columns.
	var manifestSchemaVersion, artifactID, artifactVersionID, payloadType sql.NullString
	_ = r.db.QueryRowContext(ctx, `
        SELECT manifest_schema_version, artifact_id, artifact_version_id, payload_type
        FROM canonical_batches WHERE tenant_id = $1 AND batch_id = $2
    `, tenantID, batchID).Scan(&manifestSchemaVersion, &artifactID, &artifactVersionID, &payloadType)

	// Rows are sorted deterministically by source_row_ref, then business_reference
	// (client_payout_ref), then canonical_row_hash, per the manifest hash spec —
	// not by source_row_num, since row ordering has no business meaning here.
	manifestRows := make([]canonicalizer.ManifestRow, 0, canonicalized)
	rowsRs, errRows := r.db.QueryContext(ctx, `
        SELECT COALESCE(source_row_ref, ''), COALESCE(canonical_row_hash, ''), amount, currency, COALESCE(client_payout_ref, '')
        FROM payment_intents
        WHERE tenant_id = $1 AND batchid = $2
        ORDER BY COALESCE(source_row_ref, ''), COALESCE(client_payout_ref, ''), COALESCE(canonical_row_hash, '')
    `, tenantID, batchID)
	if errRows != nil {
		log.Printf("⚠️ Failed to load rows for file_manifest_hash on batch=%s: %v", batchID, errRows)
	} else {
		for rowsRs.Next() {
			var mr canonicalizer.ManifestRow
			var rowAmount decimal.Decimal
			if err := rowsRs.Scan(&mr.SourceRowRef, &mr.CanonicalRowHash, &rowAmount, &mr.Currency, &mr.BusinessReference); err != nil {
				log.Printf("⚠️ Failed to scan row for file_manifest_hash on batch=%s: %v", batchID, err)
				continue
			}
			mr.AmountMinor = rowAmount.Mul(decimal.NewFromInt(100)).IntPart()
			manifestRows = append(manifestRows, mr)
		}
		rowsRs.Close()
	}

	fileManifestHash, errManifest := canonicalizer.ComputeFileManifestHash(canonicalizer.FileManifest{
		ManifestSchemaVersion:   manifestSchemaVersion.String,
		ArtifactID:              artifactID.String,
		ArtifactVersionID:       artifactVersionID.String,
		PayloadType:             payloadType.String,
		CanonicalizationVersion: canonicalizer.CanonicalizationVersion,
		MappingProfileHash:      batchMappingProfileHash.String,
		RowCount:                len(manifestRows),
		Rows:                    manifestRows,
	})
	if errManifest != nil {
		log.Printf("⚠️ Failed to compute file_manifest_hash for batch=%s: %v", batchID, errManifest)
	}

	// Step 7: UPSERT into canonical_batches (New Table)
	upsertBatchQuery := `
    INSERT INTO canonical_batches (
        batch_id, tenant_id, source_system, received_count, canonicalized_count, dlq_count, pending_count, review_count,
        low_matchability_count, low_proof_readiness_count, duplicate_risk_count,
        canonicalization_success_rate, avg_schema_completeness_score,
        avg_mapping_confidence_score, avg_matchability_score, avg_proof_readiness_score,
        avg_intent_quality_score, duplicate_risk_amount_minor, batch_quality_score,
        score_breakdown_json, total_amount, file_manifest_hash, updated_at
    ) VALUES (
        $1, $2, $3, $4, $5, $6, $7, $8,
        $9, $10, $11,
        $12, $13, $14, $15, $16,
        $17, $18, $19,
        $20, $21, $22, now()
    ) ON CONFLICT (tenant_id, batch_id) DO UPDATE SET
        tenant_id = EXCLUDED.tenant_id,
        source_system = EXCLUDED.source_system,
        received_count = EXCLUDED.received_count,
        canonicalized_count = EXCLUDED.canonicalized_count,
        dlq_count = EXCLUDED.dlq_count,
        pending_count = EXCLUDED.pending_count,
        review_count = EXCLUDED.review_count,
        low_matchability_count = EXCLUDED.low_matchability_count,
        low_proof_readiness_count = EXCLUDED.low_proof_readiness_count,
        duplicate_risk_count = EXCLUDED.duplicate_risk_count,
        canonicalization_success_rate = EXCLUDED.canonicalization_success_rate,
        avg_schema_completeness_score = EXCLUDED.avg_schema_completeness_score,
        avg_mapping_confidence_score = EXCLUDED.avg_mapping_confidence_score,
        avg_matchability_score = EXCLUDED.avg_matchability_score,
        avg_proof_readiness_score = EXCLUDED.avg_proof_readiness_score,
        avg_intent_quality_score = EXCLUDED.avg_intent_quality_score,
        duplicate_risk_amount_minor = EXCLUDED.duplicate_risk_amount_minor,
        batch_quality_score = EXCLUDED.batch_quality_score,
        score_breakdown_json = EXCLUDED.score_breakdown_json,
        total_amount = EXCLUDED.total_amount,
        file_manifest_hash = EXCLUDED.file_manifest_hash,
        updated_at = now()
    `
	_, err = r.db.ExecContext(ctx, upsertBatchQuery,
		batchID, tenantID, sourceSystem, received, canonicalized, dlqCount, pendingCount, reviewCount,
		lowMatchCount, lowProofCount, dupRiskCount,
		canonRate, safeFloat(avgSchema),
		safeFloat(avgMapping), safeFloat(avgMatchability), safeFloat(avgProof),
		safeFloat(avgQuality), dupRiskAmount, batchScore,
		breakdownJSON, totalAmount, fileManifestHash,
	)
	if err != nil {
		log.Printf("⚠️ Failed to upsert into canonical_batches for batchID=%s: %v", batchID, err)
		return batchScore, err
	}

	return batchScore, nil
}

func safeFloat(n sql.NullFloat64) float64 {
	if n.Valid {
		return n.Float64
	}
	return 0.0
}

func (r *PaymentIntentRepo) SaveBatch(
	ctx context.Context,
	items []models.SaveBatchItem,
) ([]models.CanonicalIntent, []models.DLQEntry, error) {
	var savedIntents []models.CanonicalIntent
	var savedDLQs []models.DLQEntry

	if len(items) == 0 {
		return savedIntents, savedDLQs, nil
	}

	const chunkSize = 500

	for start := 0; start < len(items); start += chunkSize {
		end := start + chunkSize
		if end > len(items) {
			end = len(items)
		}
		chunk := items[start:end]

		err := r.execSaveBatchChunk(ctx, chunk)
		if err != nil {
			log.Printf("⚠️ SaveBatch chunk insert failed, falling back to single-row inserts: %v", err)
			dlqRepo := NewDLQRepo(r.db)
			for _, item := range chunk {
				if item.DlqEntry != nil {
					savedDlq, err := dlqRepo.Save(ctx, *item.DlqEntry)
					if err != nil {
						log.Printf("⚠️ Fallback DLQ Save failed: %v", err)
					} else {
						savedDLQs = append(savedDLQs, savedDlq)
					}
				} else if item.Intent != nil && item.Outbox != nil {
					savedIntent, err := r.Save(ctx, item.Nir, *item.Intent, *item.Outbox, item.RegistryEntry, item.PolicyDecision, item.DuplicateDecision)
					if err != nil {
						log.Printf("⚠️ Fallback Intent Save failed: %v", err)
					} else {
						savedIntents = append(savedIntents, savedIntent)
					}
				}
			}
		} else {
			for _, item := range chunk {
				if item.DlqEntry != nil {
					savedDLQs = append(savedDLQs, *item.DlqEntry)
				} else if item.Intent != nil {
					savedIntents = append(savedIntents, *item.Intent)
				}
			}
		}
	}

	return savedIntents, savedDLQs, nil
}

func (r *PaymentIntentRepo) execSaveBatchChunk(ctx context.Context, chunk []models.SaveBatchItem) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// 1. Insert Normalized Ingest Records
	var nirs []*models.NormalizedIngestRecord
	for _, item := range chunk {
		if item.Nir != nil {
			nirs = append(nirs, item.Nir)
		}
	}
	if len(nirs) > 0 {
		const nirCols = 14
		var placeholders strings.Builder
		args := make([]interface{}, 0, len(nirs)*nirCols)
		for i, nir := range nirs {
			if i > 0 {
				placeholders.WriteString(",")
			}
			base := i * nirCols
			placeholders.WriteString(fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
				base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8, base+9, base+10, base+11, base+12, base+13, base+14))
			args = append(args,
				nir.NIRID, nir.EnvelopeID, nir.TenantID,
				nir.DetectedFormat, nir.ProfileID, nir.ProfileVersion,
				nir.FieldsJSON, nir.FieldConfidenceSummary, nir.UnmappedJSON, nir.MappingUncertainFlag,
				nir.RequiredFieldGapCount, nir.LowConfidenceFieldCount,
				nir.CreatedAt, nir.MappingProfileHash,
			)
		}
		q := fmt.Sprintf(`INSERT INTO normalized_ingest_records (
			nir_id, envelope_id, tenant_id,
			detected_format, profile_id, profile_version,
			fields_json, field_confidence_summary, unmapped_json, mapping_uncertain_flag,
			required_field_gap_count, low_confidence_field_count,
			created_at, mapping_profile_hash
		) VALUES %s`, placeholders.String())
		_, err = tx.ExecContext(ctx, q, args...)
		if err != nil {
			return fmt.Errorf("batch insert normalized_ingest_records: %w", err)
		}
	}

	// 2. Insert Payment Intents
	var intents []*models.CanonicalIntent
	for _, item := range chunk {
		if item.Intent != nil {
			intents = append(intents, item.Intent)
		}
	}
	if len(intents) > 0 {
		const piCols = 77
		var placeholders strings.Builder
		args := make([]interface{}, 0, len(intents)*piCols)
		for i, intent := range intents {
			if i > 0 {
				placeholders.WriteString(",")
			}
			base := i * piCols
			placeholders.WriteString("(")
			for j := 0; j < piCols; j++ {
				if j > 0 {
					placeholders.WriteString(",")
				}
				placeholders.WriteString(fmt.Sprintf("$%d", base+j+1))
			}
			placeholders.WriteString(")")
			args = append(args,
				intent.IntentID,                     // $1
				intent.EnvelopeID,                   // $2
				intent.TenantID,                     // $3
				intent.ContractID,                   // $4
				intent.TraceID,                      // $5
				intent.IdempotencyKey,               // $6
				intent.SalientHash,                  // $7
				intent.PayloadHash,                  // $8
				intent.IntentType,                   // $9
				intent.CanonicalVersion,             // $10
				intent.SchemaVersion,                // $11
				intent.Amount,                       // $12
				intent.Currency,                     // $13
				intent.IntendedExecutionAt,          // $14
				intent.Constraints,                  // $15
				intent.BeneficiaryType,              // $16
				intent.PIITokens,                    // $17
				intent.Beneficiary,                  // $18
				intent.Status,                       // $19
				intent.ConfidenceScore,              // $20
				intent.CanonicalSnapshotRef,         // $21
				intent.NIRSnapshotRef,               // $22
				intent.GovernanceSnapshotRef,        // $23
				intent.GovernanceHash,               // $24
				intent.CanonicalHash,                // $25
				intent.CreatedAt,                    // $26
				intent.ClientPayoutRef,              // $27
				intent.ProviderHint,                 // $28
				intent.RequestFingerprint,           // $29
				intent.RoutingHintsJSON,             // $30
				intent.GovernanceState,              // $31
				intent.BusinessState,                // $32
				intent.DuplicateRiskFlag,            // $33
				intent.MappingProfileID,             // $34
				intent.MappingProfileVersion,        // $35
				intent.SourceSystem,                 // $36
				intent.UpdatedAt,                    // $37
				intent.BusinessIdempotencyKey,       // $38
				intent.BeneficiaryFingerprint,       // $39
				intent.ProofReadinessScore,          // $40
				intent.MatchabilityScore,            // $41
				intent.IntentQualityScore,           // $42
				intent.MappingConfidenceScore,       // $43
				intent.SchemaCompletenessScore,      // $44
				intent.GovernanceReasonCodesJSON,    // $45
				intent.DuplicateReasonCode,          // $46
				intent.ClientBatchRef,               // $47
				intent.BatchID,                      // $48
				intent.SourceRowNum,                 // $49
				intent.AggregateConfidenceScore,     // $50
				intent.ReferenceQualityScore,        // $51
				intent.DuplicateRiskScore,           // $52
				intent.ScoreVersion,                 // $53
				intent.ScoreValidityStatus,          // $54
				intent.ScoreBreakdownJSON,           // $55
				intent.ScoreReasonCodesJSON,         // $56
				intent.ScoredAt,                     // $57
				intent.RequiredFieldsStatus,         // $58
				intent.TokenizationStatus,           // $59
				intent.TokenizationMetadata,         // $60
				intent.GovernanceDecision,           // $61
				intent.PaymentInstructionReceived,   // $62
				intent.CanonicalIntentCreated,       // $63
				intent.IntentLifecycleState,         // $64
				intent.MappingProfileHash,           // $65
				intent.PolicySource,                 // $66
				intent.PolicyVersion,                // $67
				intent.PolicyHash,                   // $68
				intent.SourceRowRef,                 // $69
				intent.CanonicalRowHash,             // $70
				intent.GovernanceInputFactsHash,     // $71
				intent.TokenizedDataHash,            // $72
				intent.RawRowEvidenceLeafHash,       // $73
				intent.CanonicalRowEvidenceLeafHash, // $74
				intent.RawRowHash,                   // $75
				intent.ArtifactID,                   // $76
				intent.ArtifactVersionID,            // $77
			)
		}
		q := fmt.Sprintf(`INSERT INTO payment_intents (
			intent_id, envelope_id, tenant_id, contract_id,
			trace_id, idempotency_key, salient_hash, payload_hash,
			intent_type, canonical_version, schema_version,
			amount, currency, intended_execution_at,
			constraints, beneficiary_type, pii_tokens, beneficiary,
			status, confidence_score,
			canonical_snapshot_ref, nir_snapshot_ref, governance_snapshot_ref, governance_hash,
			canonical_hash,
			created_at,
			client_payout_ref, provider_hint, request_fingerprint, routing_hints_json,
			governance_state, business_state, duplicate_risk_flag,
			mapping_profile_id, mapping_profile_version, source_system, updated_at,
			business_idempotency_key, beneficiary_fingerprint,
			proof_readiness_score, matchability_score, intent_quality_score,
			mapping_confidence_score,
			schema_completeness_score,
			governance_reason_codes_json,
			duplicate_reason_code, client_batch_ref,
			batchid,
			source_row_num,
			aggregate_confidence_score,
			reference_quality_score,
			duplicate_risk_score,
			score_version,
			score_validity_status,
			score_breakdown_json,
			score_reason_codes_json,
			scored_at,
			required_fields_status,
			tokenization_status,
			tokenization_metadata,
			governance_decision,
			payment_instruction_received,
			canonical_intent_created,
			intent_lifecycle_state,
			mapping_profile_hash,
			policy_source, policy_version, policy_hash,
			source_row_ref, canonical_row_hash,
			input_facts_hash, tokenized_data_hash,
			raw_row_evidence_leaf_hash, canonical_row_evidence_leaf_hash,
			raw_row_hash,
			artifact_id, artifact_version_id
		) VALUES %s`, placeholders.String())
		_, err = tx.ExecContext(ctx, q, args...)
		if err != nil {
			return fmt.Errorf("batch insert payment_intents: %w", err)
		}
	}

	// 3. Insert Outbox
	var outboxes []*models.OutboxEvent
	for _, item := range chunk {
		if item.Outbox != nil && item.Intent != nil {
			item.Outbox.ContractID = item.Intent.ContractID
			outboxes = append(outboxes, item.Outbox)
		}
	}
	if len(outboxes) > 0 {
		// INT-09: query text and per-row args both derive from
		// OutboxInsertColumns/OutboxInsertArgs (outbox_insert_contract.go) —
		// the same single source of truth Save's single-row path uses,
		// instead of a second hand-typed 81-column/81-arg copy here.
		q, argsPerRow := buildOutboxInsertQueryBatch(len(outboxes))
		args := make([]interface{}, 0, len(outboxes)*argsPerRow)
		for _, outbox := range outboxes {
			args = append(args, OutboxInsertArgs(*outbox)...)
		}
		_, err = tx.ExecContext(ctx, q, args...)
		if err != nil {
			return fmt.Errorf("batch insert outbox: %w", err)
		}
	}

	// 4. Insert Business Idempotency Registry
	var registryEntries []*models.BusinessIdempotencyEntry
	for _, item := range chunk {
		if item.RegistryEntry != nil {
			registryEntries = append(registryEntries, item.RegistryEntry)
		}
	}
	if len(registryEntries) > 0 {
		const regCols = 9
		var placeholders strings.Builder
		args := make([]interface{}, 0, len(registryEntries)*regCols)
		for i, reg := range registryEntries {
			if i > 0 {
				placeholders.WriteString(",")
			}
			base := i * regCols
			placeholders.WriteString(fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
				base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8, base+9))
			args = append(args,
				reg.TenantID, reg.BusinessIdempotencyKey, reg.IntentID,
				reg.BeneficiaryFingerprint, reg.AmountMinor, reg.CurrencyCode,
				reg.TimeBucket, reg.DuplicateReasonCode, reg.CreatedAt,
			)
		}
		q := fmt.Sprintf(`INSERT INTO business_idempotency_registry (
			tenant_id, business_idempotency_key, intent_id,
			beneficiary_fingerprint, amount_minor, currency_code,
			time_bucket, duplicate_reason_code, created_at
		) VALUES %s ON CONFLICT (tenant_id, business_idempotency_key) DO NOTHING
		RETURNING tenant_id, business_idempotency_key`, placeholders.String())

		rows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return fmt.Errorf("batch insert business_idempotency_registry: %w", err)
		}
		defer rows.Close()

		insertedKeys := make(map[string]bool)
		for rows.Next() {
			var tID uuid.UUID
			var bKey string
			if err := rows.Scan(&tID, &bKey); err == nil {
				insertedKeys[tID.String()+"|"+bKey] = true
			}
		}

		// Reconcile conflicts
		for _, item := range chunk {
			if item.RegistryEntry != nil && item.Intent != nil {
				key := item.RegistryEntry.TenantID.String() + "|" + item.RegistryEntry.BusinessIdempotencyKey
				if !insertedKeys[key] {
					item.Intent.DuplicateRiskFlag = true
					if item.Intent.DuplicateReasonCode == "" || item.Intent.DuplicateReasonCode == "NONE" {
						item.Intent.DuplicateReasonCode = "SAME_BENEFICIARY_AMOUNT_TIME"
					}
					item.Intent.GovernanceState = "FLAGGED"

					_, err = tx.ExecContext(ctx, `
						UPDATE payment_intents
						SET duplicate_risk_flag = true,
							duplicate_reason_code = $1,
							governance_state = 'FLAGGED',
							updated_at = now()
						WHERE tenant_id = $2 AND intent_id = $3`,
						item.Intent.DuplicateReasonCode,
						item.Intent.TenantID,
						item.Intent.IntentID,
					)
					if err != nil {
						return fmt.Errorf("update duplicate flags for intent %s: %w", item.Intent.IntentID, err)
					}
				}
			}
		}
	}

	// 4.5. Insert Intent Policy Decisions
	var policyDecisions []*models.IntentPolicyDecision
	for _, item := range chunk {
		if item.PolicyDecision != nil {
			policyDecisions = append(policyDecisions, item.PolicyDecision)
		}
	}
	if len(policyDecisions) > 0 {
		const policyCols = 10
		var placeholders strings.Builder
		args := make([]interface{}, 0, len(policyDecisions)*policyCols)
		for i, pd := range policyDecisions {
			if i > 0 {
				placeholders.WriteString(",")
			}
			base := i * policyCols
			placeholders.WriteString(fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
				base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8, base+9, base+10))
			args = append(args,
				pd.TenantID, pd.IntentID, pd.PolicySource, pd.PolicyVersion, pd.PolicyHash,
				pd.PolicyResult, pd.ReasonCodesJSON, pd.InputFactsHash, pd.InputFactsJSON, pd.EvaluatedAt,
			)
		}
		q := fmt.Sprintf(`INSERT INTO intent_policy_decisions (
			tenant_id, intent_id, policy_source, policy_version, policy_hash,
			policy_result, reason_codes_json, input_facts_hash, input_facts_json, evaluated_at
		) VALUES %s
		ON CONFLICT (tenant_id, intent_id, policy_source, policy_version) DO NOTHING`, placeholders.String())
		_, err = tx.ExecContext(ctx, q, args...)
		if err != nil {
			return fmt.Errorf("batch insert intent_policy_decisions: %w", err)
		}
	}

	// 4.6. Insert Duplicate Decisions
	var duplicateDecisions []*models.DuplicateDecision
	for _, item := range chunk {
		if item.DuplicateDecision != nil {
			duplicateDecisions = append(duplicateDecisions, item.DuplicateDecision)
		}
	}
	if len(duplicateDecisions) > 0 {
		const dupCols = 9
		var placeholders strings.Builder
		args := make([]interface{}, 0, len(duplicateDecisions)*dupCols)
		for i, dd := range duplicateDecisions {
			if i > 0 {
				placeholders.WriteString(",")
			}
			base := i * dupCols
			placeholders.WriteString(fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
				base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8, base+9))
			args = append(args,
				dd.TenantID, dd.IntentID, dd.Decision, dd.ReasonCode, dd.DuplicateScore,
				dd.ComparedIntentID, dd.DuplicateGroupID, dd.ComparisonFactsHash, dd.PolicyVersion,
			)
		}
		q := fmt.Sprintf(`INSERT INTO duplicate_decisions (
			tenant_id, intent_id, decision, reason_code, duplicate_score,
			compared_intent_id, duplicate_group_id, comparison_facts_hash, policy_version
		) VALUES %s`, placeholders.String())
		_, err = tx.ExecContext(ctx, q, args...)
		if err != nil {
			return fmt.Errorf("batch insert duplicate_decisions: %w", err)
		}
	}

	// 5. Insert DLQ Items
	var dlqEntries []*models.DLQEntry
	for _, item := range chunk {
		if item.DlqEntry != nil {
			if item.DlqEntry.DLQID == "" {
				item.DlqEntry.DLQID = uuid.NewString()
			}
			dlqEntries = append(dlqEntries, item.DlqEntry)
		}
	}
	if len(dlqEntries) > 0 {
		const dlqCols = 14
		var placeholders strings.Builder
		args := make([]interface{}, 0, len(dlqEntries)*dlqCols)
		for i, dlq := range dlqEntries {
			if i > 0 {
				placeholders.WriteString(",")
			}
			base := i * dlqCols
			placeholders.WriteString(fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
				base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8, base+9, base+10, base+11, base+12, base+13, base+14))
			args = append(args,
				dlq.DLQID,
				dlq.TenantID,
				dlq.EnvelopeID,
				dlq.Stage,
				dlq.ReasonCode,
				dlq.ErrorDetail,
				dlq.Replayable,
				dlq.ClientBatchRef,
				dlq.CreatedAt,
				dlq.BatchID,
				dlq.SourceRowNum,
				dlq.DLQStatus,
				dlq.IntentContext,
				dlq.TraceID,
			)
		}
		q := fmt.Sprintf(`INSERT INTO dlq_items (
			dlq_id, tenant_id, envelope_id,
			stage, reason_code, error_detail,
			replayable, client_batch_ref, created_at, batch_id,
			source_row_num, dlq_status, intent_context, trace_id
		) VALUES %s`, placeholders.String())
		_, err = tx.ExecContext(ctx, q, args...)
		if err != nil {
			return fmt.Errorf("batch insert dlq_items: %w", err)
		}
	}

	return tx.Commit()
}
